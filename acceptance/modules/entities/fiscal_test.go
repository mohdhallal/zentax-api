package entities_test

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type entityView struct {
	ID                    string `json:"id"`
	FiscalCalendarPattern string `json:"fiscalCalendarPattern"`
	FinancialYearEnd      string `json:"financialYearEnd"`
	FiscalWeekEndDay      string `json:"fiscalWeekEndDay"`
	FiscalYearEndRule     string `json:"fiscalYearEndRule"`
	CustomPeriods         []struct {
		Code      string `json:"code"`
		Name      string `json:"name"`
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	} `json:"customPeriods"`
}

// TestFiscalCalendarRoundTrip (ADR-0023): the week anchor (fiscalWeekEndDay /
// fiscalYearEndRule) and customPeriods round-trip through POST → GET → PUT,
// default to saturday / nearest / [] when omitted, and every cross-field rule
// is a 400 before anything is written.
func (s *EntitiesSuite) TestFiscalCalendarRoundTrip() {
	tenant := s.InsertTenant("fiscal-a", "Fiscal Tenant").String()
	post := func(body any) *acceptance.TestResponse { return s.As(tenant).POST(s.T(), "/entities", body) }

	// Defaults.
	var plain entityView
	r := post(map[string]any{"name": "Plain", "country": "Germany"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &plain)
	s.Require().Equal("standard", plain.FiscalCalendarPattern)
	s.Require().Equal("saturday", plain.FiscalWeekEndDay)
	s.Require().Equal("nearest", plain.FiscalYearEndRule)
	s.Require().NotNil(plain.CustomPeriods)
	s.Require().Empty(plain.CustomPeriods)

	// A 4-4-5 retailer with an explicit anchor; a non-custom entity may carry
	// a period list too.
	periods := []map[string]any{
		{"code": "T1", "name": "Trimester 1", "startDate": "01-01", "endDate": "04-30"},
		{"code": "T2", "name": "Trimester 2", "startDate": "05-01", "endDate": "08-31"},
		{"code": "T3", "name": "Trimester 3", "startDate": "09-01", "endDate": "12-31"},
	}
	var retail entityView
	r = post(map[string]any{
		"name": "Retail", "country": "United States", "fiscalCalendarPattern": "445",
		"financialYearEnd": "01-31", "fiscalWeekEndDay": "sunday", "fiscalYearEndRule": "last",
		"customPeriods": periods,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &retail)
	s.Require().Equal("445", retail.FiscalCalendarPattern)
	s.Require().Equal("sunday", retail.FiscalWeekEndDay)
	s.Require().Equal("last", retail.FiscalYearEndRule)
	s.Require().Len(retail.CustomPeriods, 3)

	var got entityView
	s.As(tenant).GET(s.T(), "/entities/"+retail.ID).DecodeData(s.T(), &got)
	s.Require().Equal("sunday", got.FiscalWeekEndDay)
	s.Require().Equal("last", got.FiscalYearEndRule)
	s.Require().Equal("T2", got.CustomPeriods[1].Code)
	s.Require().Equal("Trimester 2", got.CustomPeriods[1].Name)
	s.Require().Equal("05-01", got.CustomPeriods[1].StartDate)
	s.Require().Equal("08-31", got.CustomPeriods[1].EndDate)

	// PUT switches it to a custom calendar; omitted anchor fields fall back
	// to the defaults (PUT is a full replacement).
	r = s.As(tenant).PUT(s.T(), "/entities/"+retail.ID, map[string]any{
		"name": "Retail", "country": "United States", "fiscalCalendarPattern": "custom",
		"financialYearEnd": "12-31", "customPeriods": periods[:2],
	})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &got)
	s.Require().Equal("custom", got.FiscalCalendarPattern)
	s.Require().Equal("saturday", got.FiscalWeekEndDay)
	s.Require().Equal("nearest", got.FiscalYearEndRule)
	s.Require().Len(got.CustomPeriods, 2)

	// Validation: every failure is a 400 and creates nothing.
	bad := []struct {
		name string
		body map[string]any
		msg  string
	}{
		{"custom without periods", map[string]any{"name": "X", "country": "DE", "fiscalCalendarPattern": "custom"}, "at least one custom period"},
		{"bad financialYearEnd", map[string]any{"name": "X", "country": "DE", "financialYearEnd": "13-01"}, "financialYearEnd"},
		{"bad week end day", map[string]any{"name": "X", "country": "DE", "fiscalWeekEndDay": "someday"}, ""},
		{"bad year end rule", map[string]any{"name": "X", "country": "DE", "fiscalYearEndRule": "closest"}, ""},
		{"bad period start", map[string]any{"name": "X", "country": "DE", "fiscalCalendarPattern": "custom",
			"customPeriods": []map[string]any{{"code": "A", "name": "A", "startDate": "1-1", "endDate": "04-30"}}}, ""},
		{"impossible period end", map[string]any{"name": "X", "country": "DE", "fiscalCalendarPattern": "custom",
			"customPeriods": []map[string]any{{"code": "A", "name": "A", "startDate": "01-01", "endDate": "04-31"}}}, "customPeriods[0].endDate"},
		{"duplicate code", map[string]any{"name": "X", "country": "DE", "fiscalCalendarPattern": "custom",
			"customPeriods": []map[string]any{
				{"code": "A", "name": "A", "startDate": "01-01", "endDate": "06-30"},
				{"code": "A", "name": "B", "startDate": "07-01", "endDate": "12-31"},
			}}, "not unique"},
		{"empty code", map[string]any{"name": "X", "country": "DE", "fiscalCalendarPattern": "custom",
			"customPeriods": []map[string]any{{"code": "", "name": "A", "startDate": "01-01", "endDate": "06-30"}}}, ""},
	}
	for _, tc := range bad {
		r := post(tc.body)
		r.AssertStatus(s.T(), http.StatusBadRequest)
		if tc.msg != "" {
			s.Require().Contains(r.BodyString(), tc.msg, tc.name)
		}
	}
	var list []map[string]any
	s.As(tenant).GET(s.T(), "/entities").DecodeData(s.T(), &list)
	s.Require().Len(list, 2, "invalid bodies must not create entities")
}
