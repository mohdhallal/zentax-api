package domain

import "time"

type WorkflowID = string

// Workflow is either a 'recurring' obligation workflow (tied to an entity +
// obligation type, periods auto-calculated) or a one-off 'project'. TenantID is
// infrastructure (RLS), absent from the model.
type Workflow struct {
	ID               WorkflowID  `json:"id"               db:"id"`
	Name             string      `json:"name"             db:"name"`
	Description      *string     `json:"description"      db:"description"`
	WorkflowCategory string      `json:"workflowCategory" db:"workflow_category"`
	ProjectType      *string     `json:"projectType"      db:"project_type"`
	FinancialYear    *string     `json:"financialYear"    db:"financial_year"`
	Periodicity      *string     `json:"periodicity"      db:"periodicity"`
	SelectedPeriods  Periods     `json:"selectedPeriods"  db:"selected_periods"`
	EntityID         *string     `json:"entityId"         db:"entity_id"`
	ObligationTypeID *string     `json:"obligationTypeId" db:"obligation_type_id"`
	DueDateRule      DueDateRule `json:"dueDateRule"      db:"due_date_rule"`
	StartDate        *string     `json:"startDate"        db:"start_date"`
	EndDate          *string     `json:"endDate"          db:"end_date"`
	TasksSequential  bool        `json:"tasksSequential"  db:"tasks_sequential"`
	Status           string      `json:"status"           db:"status"`
	CreatedBy        *string     `json:"createdBy" db:"created_by"`
	UpdatedBy        *string     `json:"updatedBy" db:"updated_by"`
	CreatedAt        time.Time   `json:"createdAt"        db:"created_at"`
	UpdatedAt        time.Time   `json:"updatedAt"        db:"updated_at"`
	// EntityName / ObligationTypeName are the referenced rows' display names,
	// resolved at read time through RLS-scoped LEFT JOINs (nil when the
	// reference is nil — a project workflow). Read-model fields: never written.
	EntityName         *string `json:"entityName"         db:"entity_name"`
	ObligationTypeName *string `json:"obligationTypeName" db:"obligation_type_name"`
}

type CreateWorkflowInput struct {
	Name             string
	Description      *string
	WorkflowCategory string
	ProjectType      *string
	FinancialYear    *string
	Periodicity      *string
	SelectedPeriods  Periods
	EntityID         *string
	ObligationTypeID *string
	DueDateRule      DueDateRule
	StartDate        *string
	EndDate          *string
	TasksSequential  bool
}

type UpdateWorkflowInput struct {
	Name             string
	Description      *string
	WorkflowCategory string
	ProjectType      *string
	FinancialYear    *string
	Periodicity      *string
	SelectedPeriods  Periods
	EntityID         *string
	ObligationTypeID *string
	DueDateRule      DueDateRule
	StartDate        *string
	EndDate          *string
	TasksSequential  bool
	Status           string
}
