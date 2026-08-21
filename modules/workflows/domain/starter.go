package domain

import "context"

// Starter generates task instances for a workflow (POST /workflows/{id}/start).
// It is implemented by the task-instances generator and injected in bootstrap;
// declared here so the workflows module does not depend on task-instances.
type Starter interface {
	StartWorkflow(ctx context.Context, workflowID string) (int, error)
}
