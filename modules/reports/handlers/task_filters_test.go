package handlers

import (
	"reflect"
	"testing"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
)

func str(s string) *string { return &s }

// financialYear values that mean "no filter" ("" and the legacy "all") are
// dropped; `none` and a real year pass through; `status=open` is handed to
// the repository as the domain constant.
func TestTaskFilters_NormalizesTheSharedQuery(t *testing.T) {
	f, err := taskFilters(dto.TaskFilterQuery{
		FinancialYear: []string{"", "all", "2025", domain.FinancialYearNone},
		Status:        str(domain.StatusOpen),
	}, &app.Requester{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.FinancialYears) != 2 || f.FinancialYears[0] != "2025" || f.FinancialYears[1] != domain.FinancialYearNone {
		t.Fatalf("FinancialYears = %v", f.FinancialYears)
	}
	if f.Status == nil || *f.Status != domain.StatusOpen {
		t.Fatalf("Status = %v", f.Status)
	}

	empty, err := taskFilters(dto.TaskFilterQuery{FinancialYear: []string{"all"}}, &app.Requester{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.FinancialYears != nil {
		t.Fatalf("\"all\" alone must mean no filter, got %v", empty.FinancialYears)
	}
	if !reflect.DeepEqual(empty, domain.TaskFilters{}) {
		t.Fatalf("an empty query must map to no filter at all, got %+v", empty)
	}
}

// Every new filter is carried over verbatim; the date bounds become legal
// dates; `me` becomes the requester's id and `unassigned` stays the sentinel.
func TestTaskFilters_CarriesEveryFilter(t *testing.T) {
	q := dto.TaskFilterQuery{
		WorkflowID:       str("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		EntityID:         str("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		AssigneeID:       str("me"),
		ObligationTypeID: str("6ba7b810-9dad-11d1-80b4-00c04fd430ca"),
		TaxType:          str("VAT"),
		PeriodCode:       str("M1"),
		WorkflowCategory: str("recurring"),
		Due:              str(domain.DueThisWeek),
		DueFrom:          str("2025-01-01"),
		DueTo:            str("2025-12-31"),
		Search:           str("100% GmbH"),
	}
	f, err := taskFilters(q, &app.Requester{ID: "user-42"})
	if err != nil {
		t.Fatal(err)
	}
	if *f.WorkflowID != *q.WorkflowID || *f.EntityID != *q.EntityID || *f.ObligationTypeID != *q.ObligationTypeID {
		t.Fatalf("ids must pass through, got %+v", f)
	}
	if f.AssigneeID == nil || *f.AssigneeID != "user-42" {
		t.Fatalf("me must resolve to the requester, got %v", f.AssigneeID)
	}
	if *f.TaxType != "VAT" || *f.PeriodCode != "M1" || *f.WorkflowCategory != "recurring" || *f.Due != domain.DueThisWeek {
		t.Fatalf("enums must pass through, got %+v", f)
	}
	if f.DueFrom == nil || f.DueFrom.String() != "2025-01-01" || f.DueTo == nil || f.DueTo.String() != "2025-12-31" {
		t.Fatalf("date bounds must parse, got %v / %v", f.DueFrom, f.DueTo)
	}
	if f.Search == nil || *f.Search != "100% GmbH" {
		t.Fatalf("the raw search term travels to the repository (which escapes it), got %v", f.Search)
	}

	f, err = taskFilters(dto.TaskFilterQuery{AssigneeID: str(domain.AssigneeUnassigned)}, &app.Requester{ID: "user-42"})
	if err != nil {
		t.Fatal(err)
	}
	if f.AssigneeID == nil || *f.AssigneeID != domain.AssigneeUnassigned {
		t.Fatalf("unassigned is the domain sentinel, got %v", f.AssigneeID)
	}

	// A bare uuid is passed as-is, never mistaken for the requester.
	f, err = taskFilters(dto.TaskFilterQuery{AssigneeID: str("6ba7b810-9dad-11d1-80b4-00c04fd430cb")}, &app.Requester{ID: "user-42"})
	if err != nil {
		t.Fatal(err)
	}
	if *f.AssigneeID != "6ba7b810-9dad-11d1-80b4-00c04fd430cb" {
		t.Fatalf("got %v", f.AssigneeID)
	}
}

// `me` without a principal cannot be resolved: a validation error, never a
// silent "no filter" (which would list everyone's work as the caller's).
func TestTaskFilters_MeNeedsARequester(t *testing.T) {
	if _, err := taskFilters(dto.TaskFilterQuery{AssigneeID: str("me")}, nil); err == nil {
		t.Fatal("expected an error for me without a requester")
	}
	if _, err := taskFilters(dto.TaskFilterQuery{AssigneeID: str("me")}, &app.Requester{}); err == nil {
		t.Fatal("expected an error for me with an empty requester id")
	}
}

// The date bounds are strict (like the feed's uuid params): absent is nil,
// anything that is not a legal YYYY-MM-DD date — a blank, the legacy "all",
// an impossible day — is a validation error, never a silent "no bound".
func TestTaskFilters_DateBounds(t *testing.T) {
	f, err := taskFilters(dto.TaskFilterQuery{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.DueFrom != nil || f.DueTo != nil {
		t.Fatalf("absent bounds must be nil, got %v / %v", f.DueFrom, f.DueTo)
	}
	for _, bad := range []string{"", "all", "2025-02-30", "yesterday", "2025-1-1"} {
		if _, err := taskFilters(dto.TaskFilterQuery{DueFrom: str(bad)}, nil); err == nil {
			t.Fatalf("dueFrom=%q: expected a validation error", bad)
		}
		if _, err := taskFilters(dto.TaskFilterQuery{DueTo: str(bad)}, nil); err == nil {
			t.Fatalf("dueTo=%q: expected a validation error", bad)
		}
	}
}

// The workflow-stats filters normalize financialYear the same way.
func TestWorkflowStatsFilters(t *testing.T) {
	f := workflowStatsFilters(dto.WorkflowStatsQuery{
		WorkflowID:       str("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
		EntityID:         str("6ba7b810-9dad-11d1-80b4-00c04fd430c9"),
		FinancialYear:    []string{"all", "", "2026", domain.FinancialYearNone},
		Status:           str("active"),
		WorkflowCategory: str("project"),
	})
	if *f.WorkflowID != "6ba7b810-9dad-11d1-80b4-00c04fd430c8" || *f.EntityID != "6ba7b810-9dad-11d1-80b4-00c04fd430c9" {
		t.Fatalf("ids must pass through, got %+v", f)
	}
	if len(f.FinancialYears) != 2 || f.FinancialYears[0] != "2026" || f.FinancialYears[1] != domain.FinancialYearNone {
		t.Fatalf("FinancialYears = %v", f.FinancialYears)
	}
	if *f.Status != "active" || *f.WorkflowCategory != "project" {
		t.Fatalf("enums must pass through, got %+v", f)
	}
	if empty := workflowStatsFilters(dto.WorkflowStatsQuery{FinancialYear: []string{"all"}}); empty.FinancialYears != nil {
		t.Fatalf("\"all\" alone must mean no filter, got %v", empty.FinancialYears)
	}
}
