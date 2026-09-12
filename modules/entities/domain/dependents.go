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

// ScopedGrant is one RBAC grant an entity delete revokes: who held it, at which
// role. Read before the DELETE, because the cascade takes the row with no
// tombstone and no history table behind it.
//
// The scope entity is not repeated — it is the entity being deleted, i.e.
// resource_id on the envelope — and the scope entity's NAME is deliberately
// absent, for the same reason identity's grant projection omits it: it is the
// entity's free text.
type ScopedGrant struct {
	UserID string `db:"user_id"`
	Role   string `db:"role"`
}

// AuditDetails is the ADR-0008 details payload for entity.deleted: counts, plus
// the one thing a count cannot carry — WHICH access was revoked. No names, no
// free text, nothing that could carry PII. blobsQueued is how many storage keys
// went to the reclaim queue for the purge job.
//
// revokedGrants sits beside userGrantsRevoked rather than replacing it: the
// count comes from the census's own round trip and the list from a second read,
// so keeping both makes a disagreement between them visible instead of quietly
// substituting one for the other.
//
// Both halves of a grant are constrained — the role by authz.KnownRole, the
// user id by a uuid and a composite FK — and a user id is a value the trail
// already carries as actor_id and as the resource_id of every member entry, so
// naming the grants introduces no new class of value into the log.
func (d EntityDependents) AuditDetails(blobsQueued int, revoked []ScopedGrant) map[string]any {
	details := map[string]any{
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
	if len(revoked) > 0 {
		grants := make([]map[string]any, 0, len(revoked))
		for _, g := range revoked {
			grants = append(grants, map[string]any{"userId": g.UserID, "role": g.Role})
		}
		details["revokedGrants"] = grants
	}
	return details
}
