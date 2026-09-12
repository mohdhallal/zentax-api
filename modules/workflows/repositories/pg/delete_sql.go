package pg

// Delete-side SQL (ADR-0018 attested-delete guard, ADR-0007 blob reclaim). Kept
// out of sql.go because it is not part of the CRUD projection the base
// repository drives — these are the two statements the Delete use case runs
// before the DELETE itself, on the same transaction.
//
// Neither statement names tenant_id: RLS scopes every table to the caller's
// tenant (ADR-0004), so the counts and the queued keys can only ever be the
// tenant's own.

// countWorkflowDependentsSQL is the pre-delete census: everything the cascade
// from this workflow would take with it, in one round trip. Written as scalar
// subqueries so one plan answers all five; each one is an index lookup on the
// workflow_id / document_id foreign key.
const countWorkflowDependentsSQL = `
	SELECT
		(SELECT COUNT(*)::int FROM task_instances
		  WHERE workflow_id = $1 AND approved_at IS NOT NULL)      AS approved_task_instances,
		(SELECT COUNT(*)::int FROM task_instances
		  WHERE workflow_id = $1)                                  AS task_instances,
		(SELECT COUNT(*)::int FROM workflow_tasks
		  WHERE workflow_id = $1)                                  AS workflow_tasks,
		(SELECT COUNT(*)::int FROM documents
		  WHERE workflow_id = $1)                                  AS documents,
		(SELECT COUNT(*)::int FROM document_versions dv
		  WHERE dv.document_id IN (SELECT id FROM documents WHERE workflow_id = $1))
		                                                           AS document_versions`

// queueWorkflowBlobReclaimSQL hands the purge job the storage keys it is about
// to lose the metadata for. Soft-deleted documents are included on purpose:
// their rows are cascading away too, so their blobs would be just as
// unreachable. The queue's tenant_id defaults from the app.tenant_id GUC.
const queueWorkflowBlobReclaimSQL = `
	INSERT INTO storage_reclaim (storage_key, reason, resource_type, resource_id)
	SELECT dv.storage_key, 'workflow.deleted', 'workflow', $1::uuid
	FROM document_versions dv
	JOIN documents d ON d.id = dv.document_id
	WHERE d.workflow_id = $1::uuid`
