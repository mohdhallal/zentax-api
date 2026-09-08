package handlers

import (
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
)

// financialYear values that mean "no filter" ("" and the legacy "all") are
// dropped; `none` and a real year pass through; `status=open` is handed to
// the repository as the domain constant.
func TestTaskFilters_NormalizesTheSharedQuery(t *testing.T) {
	open := domain.StatusOpen
	f := taskFilters(dto.TaskFilterQuery{
		FinancialYear: []string{"", "all", "2025", domain.FinancialYearNone},
		Status:        &open,
	})
	if len(f.FinancialYears) != 2 || f.FinancialYears[0] != "2025" || f.FinancialYears[1] != domain.FinancialYearNone {
		t.Fatalf("FinancialYears = %v", f.FinancialYears)
	}
	if f.Status == nil || *f.Status != domain.StatusOpen {
		t.Fatalf("Status = %v", f.Status)
	}

	empty := taskFilters(dto.TaskFilterQuery{FinancialYear: []string{"all"}})
	if empty.FinancialYears != nil {
		t.Fatalf("\"all\" alone must mean no filter, got %v", empty.FinancialYears)
	}
}
