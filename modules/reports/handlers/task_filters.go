package handlers

import (
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
)

// taskFilters maps the validated shared query (ids and enums already checked
// by the schema tags) onto the domain filter set. financialYear values that
// mean "no filter" ("" and the legacy "all") are dropped; the `none` sentinel
// and `status=open` pass through as the domain constants the repository
// translates.
func taskFilters(q dto.TaskFilterQuery) domain.TaskFilters {
	f := domain.TaskFilters{
		WorkflowID:       q.WorkflowID,
		EntityID:         q.EntityID,
		AssigneeID:       q.AssigneeID,
		Status:           q.Status,
		WorkflowCategory: q.WorkflowCategory,
	}
	for _, year := range q.FinancialYear {
		if year == "" || year == "all" {
			continue
		}
		f.FinancialYears = append(f.FinancialYears, year)
	}
	return f
}
