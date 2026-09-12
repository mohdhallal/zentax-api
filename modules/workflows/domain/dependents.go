package domain

// WorkflowDependents is the census a delete takes before it runs: everything
// the workflow chain would carry away with the workflow, counted by kind.
//
// It exists for two reasons. ApprovedTaskInstances decides whether the delete
// happens at all (ADR-0018: an approved instance is attested evidence, not
// collateral). The rest lands in the audit envelope, so the trail says what a
// permitted delete actually removed instead of recording a bare
// "workflow.deleted" with empty details.
//
// Documents counts soft-deleted rows too: they are cascading away for real, so
// the trail — and the blob reclaim that goes with it — must account for them.
type WorkflowDependents struct {
	ApprovedTaskInstances int `db:"approved_task_instances"`
	TaskInstances         int `db:"task_instances"`
	WorkflowTasks         int `db:"workflow_tasks"`
	Documents             int `db:"documents"`
	DocumentVersions      int `db:"document_versions"`
}

// HasAttestedWork reports whether approved (attested) work hangs off the
// workflow, which makes the delete a refusal.
func (d WorkflowDependents) HasAttestedWork() bool { return d.ApprovedTaskInstances > 0 }

// AuditDetails is the ADR-0008 details payload for workflow.deleted: counts
// only — no names, no free text, nothing that could carry PII. blobsQueued is
// how many storage keys went to the reclaim queue for the purge job.
func (d WorkflowDependents) AuditDetails(blobsQueued int) map[string]any {
	return map[string]any{
		"taskInstances":         d.TaskInstances,
		"workflowTasks":         d.WorkflowTasks,
		"documents":             d.Documents,
		"documentVersions":      d.DocumentVersions,
		"blobsQueuedForReclaim": blobsQueued,
	}
}
