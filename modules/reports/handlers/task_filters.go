package handlers

import (
	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// assigneeMe is the API's pseudo-assignee for the session user. It is resolved
// here, at the edge: the domain only ever sees a user id or AssigneeUnassigned.
const assigneeMe = "me"

// taskFilters maps the validated shared query (ids, enums and date layouts
// already checked by the schema tags) onto the domain filter set. financialYear
// values that mean "no filter" ("" and the legacy "all") are dropped; the
// `none` sentinel, `status=open` and `assigneeId=unassigned` pass through as
// the domain constants the repository translates; `assigneeId=me` becomes the
// requester's user id (a service account is user-shaped too — its own id).
func taskFilters(q dto.TaskFilterQuery, requester *app.Requester) (domain.TaskFilters, error) {
	f := domain.TaskFilters{
		WorkflowID:       q.WorkflowID,
		EntityID:         q.EntityID,
		AssigneeID:       q.AssigneeID,
		ObligationTypeID: q.ObligationTypeID,
		TaxType:          q.TaxType,
		FinancialYears:   financialYears(q.FinancialYear),
		PeriodCode:       q.PeriodCode,
		Status:           q.Status,
		WorkflowCategory: q.WorkflowCategory,
		Due:              q.Due,
		Search:           q.Search,
	}
	if q.AssigneeID != nil && *q.AssigneeID == assigneeMe {
		if requester == nil || requester.ID == "" {
			return domain.TaskFilters{}, apperrors.NewValidation("assigneeId=me requires an authenticated user")
		}
		me := requester.ID
		f.AssigneeID = &me
	}
	var err error
	if f.DueFrom, err = queryDate("dueFrom", q.DueFrom); err != nil {
		return domain.TaskFilters{}, err
	}
	if f.DueTo, err = queryDate("dueTo", q.DueTo); err != nil {
		return domain.TaskFilters{}, err
	}
	return f, nil
}

// queryDate turns an optional YYYY-MM-DD query value into a legal date
// (ADR-0002); nil stays nil. Unlike the legacy report filters, a blank or
// "all" value is NOT "absent" here — the feed's contract (like its uuid
// params) is strict: the DTO's layout tag has already rejected it with a 400,
// so this only re-parses a layout-valid value into the date type.
func queryDate(name string, s *string) (*dateonly.Date, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // absent filter
	}
	d, err := dateonly.Parse(*s)
	if err != nil {
		return nil, apperrors.NewValidation(name + " must be a YYYY-MM-DD date")
	}
	return &d, nil
}

// workflowStatsFilters maps the validated workflow-stats query onto the domain
// filters, normalizing financialYear the same way the task filters do.
func workflowStatsFilters(q dto.WorkflowStatsQuery) domain.WorkflowStatsFilters {
	return domain.WorkflowStatsFilters{
		WorkflowID:       q.WorkflowID,
		EntityID:         q.EntityID,
		FinancialYears:   financialYears(q.FinancialYear),
		Status:           q.Status,
		WorkflowCategory: q.WorkflowCategory,
	}
}

// financialYears drops the values that mean "no filter" ("" and the legacy
// "all") and keeps the rest — real years and the `none` sentinel — in order.
// nil when nothing remains, so an "all"-only request is no filter at all.
func financialYears(values []string) []string {
	var years []string
	for _, year := range values {
		if year == "" || year == "all" {
			continue
		}
		years = append(years, year)
	}
	return years
}
