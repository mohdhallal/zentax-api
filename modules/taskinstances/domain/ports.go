package domain

import (
	"context"

	datatemplatesdomain "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// TemplateResolver loads the data template a task instance's tax data is
// validated against (ADR-0001: the server, not the form, is the authority on
// what a valid record is). Tenant-scoped through the request context: another
// tenant's template id resolves to nil, like a missing one. Implemented by
// the data-templates module; optional on the use cases (nil = no validation).
type TemplateResolver interface {
	ResolveTemplate(ctx context.Context, templateID string) (*datatemplatesdomain.DataTemplate, error)
}

type TaskInstanceRepository interface {
	Create(ctx context.Context, input CreateTaskInstanceInput) (*TaskInstance, error)
	GetById(ctx context.Context, id TaskInstanceID) (*TaskInstance, error)
	Update(ctx context.Context, id TaskInstanceID, input UpdateTaskInstanceInput) (*TaskInstance, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]TaskInstance, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
	// CountByWorkflow reports how many instances already exist for a workflow —
	// used to keep workflow-start idempotent (refuse re-generation).
	CountByWorkflow(ctx context.Context, workflowID string) (int, error)
	// Approval-flow transitions (ADR-0018). Preconditions / SoD are enforced in
	// the use cases; these write the state change and return the updated row.
	SubmitForApproval(ctx context.Context, id TaskInstanceID, submittedBy string) (*TaskInstance, error)
	Approve(ctx context.Context, id TaskInstanceID, approvedBy string) (*TaskInstance, error)
	Reject(ctx context.Context, id TaskInstanceID, reason *string) (*TaskInstance, error)
}

// ObligationResolver finds the entity obligation that links a workflow's
// entity and obligation type — the source of the payment rule the generator
// derives paymentDeadline from (ADR-0023 §5). RLS-scoped through the request
// context; nil when the pair has no obligation (payment = filing). Implemented
// by the entity-obligations module; optional on the generator (nil = no lookup).
type ObligationResolver interface {
	FindByEntityAndType(ctx context.Context, entityID, obligationTypeID string) (*entityobligationsdomain.EntityObligation, error)
}

// AssigneeChecker answers whether a user id may be assigned a task in the
// requester's tenant (an ACTIVE HUMAN member of that tenant). Implemented by
// the identity module; optional on the use cases (nil = no check).
type AssigneeChecker interface {
	IsAssignable(ctx context.Context, userID string) (bool, error)
}

// TaskInstanceUseCases is the read/update surface. Instances are created by
// workflow-start generation, not via a public create endpoint.
type TaskInstanceUseCases interface {
	GetById(ctx context.Context, id TaskInstanceID) (*TaskInstance, error)
	Update(ctx context.Context, id TaskInstanceID, input UpdateTaskInstanceInput) (*TaskInstance, error)
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[TaskInstance], error)
	// Approval flow (ADR-0018 attestation + ADR-0012 SoD): a preparer submits, a
	// different reviewer approves or rejects.
	SubmitForApproval(ctx context.Context, id TaskInstanceID, actorID string) (*TaskInstance, error)
	Approve(ctx context.Context, id TaskInstanceID, actorID string) (*TaskInstance, error)
	Reject(ctx context.Context, id TaskInstanceID, reason *string) (*TaskInstance, error)
}
