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
	CreatedAt        time.Time   `json:"createdAt"        db:"created_at"`
	UpdatedAt        time.Time   `json:"updatedAt"        db:"updated_at"`
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
