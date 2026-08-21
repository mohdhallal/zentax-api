package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

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
