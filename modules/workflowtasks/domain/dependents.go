package domain

// WorkflowTaskDependents is the census a workflow-task delete takes before it
// runs. Removing a step from a template looks like an editing action, but
// task_instances.workflow_task_id is NOT NULL and cascades, so the delete
// destroys every instance ever generated from that step.
//
// Documents do not appear here: a document pointing at one of those instances
// is unlinked (task_instance_id SET NULL), never deleted, so no blob is lost.
type WorkflowTaskDependents struct {
	ApprovedTaskInstances int `db:"approved_task_instances"`
	TaskInstances         int `db:"task_instances"`
}

// HasAttestedWork reports whether approved (attested) work was generated from
// this template row, which makes the delete a refusal.
func (d WorkflowTaskDependents) HasAttestedWork() bool { return d.ApprovedTaskInstances > 0 }

// AuditDetails is the ADR-0008 details payload for workflow_task.deleted:
// counts only — no names, no free text, nothing that could carry PII.
func (d WorkflowTaskDependents) AuditDetails() map[string]any {
	return map[string]any{"taskInstances": d.TaskInstances}
}
