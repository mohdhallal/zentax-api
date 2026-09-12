package domain

// EntityDependents is the census an entity delete takes before it runs. An
// entity sits at the top of the cascade: workflows, their task templates and
// instances, their documents and document versions, the entity's obligations,
// and — less obviously — the RBAC grants scoped to it all go with it, while
// child entities are detached (parent_entity_id SET NULL) rather than removed.
//
// ApprovedTaskInstances decides whether the delete happens at all (ADR-0018);
// the rest lands in the audit envelope so the trail says what was removed.
//
// Documents counts soft-deleted rows too: they are cascading away for real, so
// the trail — and the blob reclaim that goes with it — must account for them.
type EntityDependents struct {
	ApprovedTaskInstances int `db:"approved_task_instances"`
	Workflows             int `db:"workflows"`
	WorkflowTasks         int `db:"workflow_tasks"`
	TaskInstances         int `db:"task_instances"`
	EntityObligations     int `db:"entity_obligations"`
	Documents             int `db:"documents"`
	DocumentVersions      int `db:"document_versions"`
	ChildEntitiesDetached int `db:"child_entities_detached"`
	UserGrantsRevoked     int `db:"user_grants_revoked"`
}

// HasAttestedWork reports whether approved (attested) work hangs off the
// entity, which makes the delete a refusal.
func (d EntityDependents) HasAttestedWork() bool { return d.ApprovedTaskInstances > 0 }

// AuditDetails is the ADR-0008 details payload for entity.deleted: counts only
// — no names, no free text, nothing that could carry PII. blobsQueued is how
// many storage keys went to the reclaim queue for the purge job.
func (d EntityDependents) AuditDetails(blobsQueued int) map[string]any {
	return map[string]any{
		"workflows":             d.Workflows,
		"workflowTasks":         d.WorkflowTasks,
		"taskInstances":         d.TaskInstances,
		"entityObligations":     d.EntityObligations,
		"documents":             d.Documents,
		"documentVersions":      d.DocumentVersions,
		"childEntitiesDetached": d.ChildEntitiesDetached,
		"userGrantsRevoked":     d.UserGrantsRevoked,
		"blobsQueuedForReclaim": blobsQueued,
	}
}
