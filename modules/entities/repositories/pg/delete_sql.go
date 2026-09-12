package pg

// Delete-side SQL (ADR-0018 attested-delete guard, ADR-0007 blob reclaim). Kept
// out of sql.go because it is not part of the CRUD projection the base
// repository drives — these are the two statements the Delete use case runs
// before the DELETE itself, on the same transaction.
//
// Neither statement names tenant_id: RLS scopes every table to the caller's
// tenant (ADR-0004), so the counts and the queued keys can only ever be the
// tenant's own.

// entityWorkflows is the sub-select both the census and the reclaim queue hang
// off: the workflows an entity delete would cascade away.
const entityWorkflows = `SELECT id FROM workflows WHERE entity_id = $1`

// countEntityDependentsSQL is the pre-delete census. child_entities_detached
// counts the children the self-FK will UNPARENT (ON DELETE SET NULL), not
// delete — they survive, orphaned at the top level, and the trail should say
// how many. user_grants_revoked is the quiet one: an RBAC grant scoped to this
// entity cascades away with it, so deleting an entity also removes people's
// access rows.
const countEntityDependentsSQL = `
	SELECT
		(SELECT COUNT(*)::int FROM task_instances ti
		  WHERE ti.workflow_id IN (` + entityWorkflows + `) AND ti.approved_at IS NOT NULL)
		                                                          AS approved_task_instances,
		(SELECT COUNT(*)::int FROM workflows WHERE entity_id = $1) AS workflows,
		(SELECT COUNT(*)::int FROM workflow_tasks wt
		  WHERE wt.workflow_id IN (` + entityWorkflows + `))       AS workflow_tasks,
		(SELECT COUNT(*)::int FROM task_instances ti
		  WHERE ti.workflow_id IN (` + entityWorkflows + `))       AS task_instances,
		(SELECT COUNT(*)::int FROM entity_obligations
		  WHERE entity_id = $1)                                    AS entity_obligations,
		(SELECT COUNT(*)::int FROM documents d
		  WHERE d.workflow_id IN (` + entityWorkflows + `))        AS documents,
		(SELECT COUNT(*)::int FROM document_versions dv
		  WHERE dv.document_id IN (
		    SELECT d.id FROM documents d WHERE d.workflow_id IN (` + entityWorkflows + `)))
		                                                           AS document_versions,
		(SELECT COUNT(*)::int FROM entities
		  WHERE parent_entity_id = $1)                             AS child_entities_detached,
		(SELECT COUNT(*)::int FROM user_grants
		  WHERE scope_entity_id = $1)                              AS user_grants_revoked`

// queueEntityBlobReclaimSQL hands the purge job the storage keys it is about to
// lose the metadata for. Soft-deleted documents are included on purpose: their
// rows are cascading away too, so their blobs would be just as unreachable.
// The queue's tenant_id defaults from the app.tenant_id GUC.
const queueEntityBlobReclaimSQL = `
	INSERT INTO storage_reclaim (storage_key, reason, resource_type, resource_id)
	SELECT dv.storage_key, 'entity.deleted', 'entity', $1::uuid
	FROM document_versions dv
	JOIN documents d ON d.id = dv.document_id
	WHERE d.workflow_id IN (SELECT id FROM workflows WHERE entity_id = $1::uuid)`
