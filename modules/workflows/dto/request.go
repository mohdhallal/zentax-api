package dto

import "github.com/mohamadhallal/zentax-api/modules/workflows/domain"

type CreateWorkflowBody struct {
	Name             string             `json:"name"             validate:"required,min=1,max=200" example:"Monthly VAT Germany"`
	Description      *string            `json:"description"      validate:"omitempty,max=2000"`
	WorkflowCategory string             `json:"workflowCategory" validate:"omitempty,oneof=recurring project" example:"recurring"`
	ProjectType      *string            `json:"projectType"      validate:"omitempty,oneof=dispute audit_verification due_diligence market_expansion advisory restructuring custom"`
	FinancialYear    *string            `json:"financialYear"    validate:"omitempty,max=9" example:"2025"`
	Periodicity      *string            `json:"periodicity"      validate:"omitempty,oneof=weekly monthly quarterly bi-annual annual consolidated-annual" example:"monthly"`
	SelectedPeriods  domain.Periods     `json:"selectedPeriods"  validate:"omitempty,dive,max=16"` // custom period codes may be 16 chars (entities.customPeriods)
	EntityID         *string            `json:"entityId"         validate:"omitempty,uuid"`
	ObligationTypeID *string            `json:"obligationTypeId" validate:"omitempty,uuid"`
	DueDateRule      domain.DueDateRule `json:"dueDateRule"      validate:"omitempty"`
	StartDate        *string            `json:"startDate"        validate:"omitempty,len=10" example:"2025-01-01"`
	EndDate          *string            `json:"endDate"          validate:"omitempty,len=10" example:"2025-12-31"`
	TasksSequential  bool               `json:"tasksSequential"`
}

type UpdateWorkflowBody struct {
	Name             string             `json:"name"             validate:"required,min=1,max=200"`
	Description      *string            `json:"description"      validate:"omitempty,max=2000"`
	WorkflowCategory string             `json:"workflowCategory" validate:"omitempty,oneof=recurring project"`
	ProjectType      *string            `json:"projectType"      validate:"omitempty,oneof=dispute audit_verification due_diligence market_expansion advisory restructuring custom"`
	FinancialYear    *string            `json:"financialYear"    validate:"omitempty,max=9"`
	Periodicity      *string            `json:"periodicity"      validate:"omitempty,oneof=weekly monthly quarterly bi-annual annual consolidated-annual"`
	SelectedPeriods  domain.Periods     `json:"selectedPeriods"  validate:"omitempty,dive,max=16"` // custom period codes may be 16 chars (entities.customPeriods)
	EntityID         *string            `json:"entityId"         validate:"omitempty,uuid"`
	ObligationTypeID *string            `json:"obligationTypeId" validate:"omitempty,uuid"`
	DueDateRule      domain.DueDateRule `json:"dueDateRule"      validate:"omitempty"`
	StartDate        *string            `json:"startDate"        validate:"omitempty,len=10"`
	EndDate          *string            `json:"endDate"          validate:"omitempty,len=10"`
	TasksSequential  bool               `json:"tasksSequential"`
	Status           string             `json:"status"           validate:"omitempty,oneof=draft active completed archived"`
}

type WorkflowIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// ListWorkflowsQuery pages and filters the workflow list. status and
// financialYear are repeatable (?status=active&status=draft is "ongoing";
// ?financialYear=2026&financialYear=none is one fiscal year plus the project
// workflows, which carry no year — `none` selects a NULL financial_year);
// each repeated value is validated on its own (dive). search is a literal,
// case-insensitive substring of the workflow name (% _ \ match themselves).
// Filter columns are `w.`-qualified: the list is a LEFT-JOINed select over
// workflows w (entity / obligation-type names), and `status` / `name` exist on
// the joined tables too.
type ListWorkflowsQuery struct {
	Limit            int      `json:"limit"            default:"20" validate:"min=1,max=100" example:"20"`
	Offset           int      `json:"offset"           default:"0"  validate:"min=0" example:"0"`
	Sort             []string `json:"sort"             validate:"omitempty,dive,oneof=createdAt:asc createdAt:desc name:asc name:desc" example:"createdAt:desc"`
	WorkflowCategory *string  `json:"workflowCategory" filter:"w.workflow_category" validate:"omitempty,oneof=recurring project" example:"recurring"`
	Status           []string `json:"status"           filter:"w.status" validate:"omitempty,dive,oneof=draft active completed archived" example:"active"`
	FinancialYear    []string `json:"financialYear"    filter:"w.financial_year" validate:"omitempty,dive,max=9" example:"2025"`
	EntityID         *string  `json:"entityId"         filter:"w.entity_id" validate:"omitempty,uuid"`
	Search           string   `json:"search"           validate:"omitempty,max=200" example:"VAT"`
}
