-- Deploy attested_delete_guard
BEGIN;

-- ADR-0018 at the database: an APPROVED task instance is attested evidence and
-- must not be destroyed as collateral of a routine delete.
--
-- The workflow chain is wired ON DELETE CASCADE throughout
-- (entities → workflows → workflow_tasks → task_instances, and
-- workflows → documents → document_versions), so before this migration one
-- `DELETE FROM workflows` or `DELETE FROM entities` silently erased every
-- approved filing beneath it. The use cases refuse such a delete with a counted
-- error message; this trigger makes the refusal true for every other path too —
-- a psql session, a future endpoint, a data fix that forgets.
--
-- WHERE the guard sits, and why not on task_instances itself:
--   * Tenant off-boarding (platform/seed.DeleteTenant) removes a tenant
--     wholesale by deleting the tenant-scoped tables CHILDREN FIRST —
--     task_instances, then workflow_tasks, then workflows. A guard ON
--     task_instances would block that legitimate removal; guards on the two
--     PARENTS do not, because by the time those statements run the instances
--     are already gone and the count is zero. The tenant-level cascades are
--     left exactly as they were.
--   * workflow_tasks carries the second guard because task_instances.
--     workflow_task_id is NOT NULL and cascades: deleting one template row —
--     "remove a step" — otherwise destroys every instance generated from it.
--   * entities needs no trigger of its own: an entity delete cascades into
--     workflows, and the workflows trigger fires for each cascaded row (RI
--     cascade deletes fire row triggers).
--
-- SECURITY INVOKER (the default) on purpose: the count then runs under the same
-- RLS the deleter is subject to, so it can never see across tenants, while a
-- superuser or BYPASSRLS connection — which sees everything — is guarded too.
-- TRUNCATE does not fire row triggers, which is what the test suite's teardown
-- relies on.
CREATE FUNCTION refuse_delete_with_attested_work() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    approved INT;
BEGIN
    -- TG_ARGV[0] names the kind: TG_TABLE_NAME is the PARTITION ('workflows_p12'),
    -- never the parent, so it cannot be branched on.
    IF TG_ARGV[0] = 'workflow' THEN
        SELECT COUNT(*)::int INTO approved
        FROM task_instances ti
        WHERE ti.tenant_id = OLD.tenant_id
          AND ti.workflow_id = OLD.id
          AND ti.approved_at IS NOT NULL;
    ELSE
        SELECT COUNT(*)::int INTO approved
        FROM task_instances ti
        WHERE ti.tenant_id = OLD.tenant_id
          AND ti.workflow_task_id = OLD.id
          AND ti.approved_at IS NOT NULL;
    END IF;

    IF approved > 0 THEN
        RAISE EXCEPTION 'cannot delete % %: % approved task instance(s) depend on it',
            TG_ARGV[0], OLD.id, approved
            USING ERRCODE = 'ZT018',
                  HINT = 'approved task instances are attested evidence (ADR-0018); archive instead of deleting';
    END IF;

    RETURN OLD;
END;
$$;

COMMENT ON FUNCTION refuse_delete_with_attested_work() IS
    'ADR-0018: refuses a delete that would cascade away approved task instances (SQLSTATE ZT018).';

-- Row triggers on a partitioned table are cloned to every partition (PG13+).
CREATE TRIGGER refuse_delete_with_attested_work
    BEFORE DELETE ON workflows
    FOR EACH ROW EXECUTE FUNCTION refuse_delete_with_attested_work('workflow');

CREATE TRIGGER refuse_delete_with_attested_work
    BEFORE DELETE ON workflow_tasks
    FOR EACH ROW EXECUTE FUNCTION refuse_delete_with_attested_work('workflow_task');

COMMIT;
