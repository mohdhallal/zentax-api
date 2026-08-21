-- Deploy actor_columns
BEGIN;

-- Actor attribution (ADR-0008 actor-by-ID): created_by / updated_by on every
-- domain table, defaulting from the app.user_id GUC that the Tx seam binds
-- alongside app.tenant_id — so INSERTs never spell them out (same pattern as
-- tenant_id). UPDATE statements set updated_by from the GUC explicitly.
-- ON DELETE SET NULL: attribution survives user erasure as "a deleted user"
-- (ADR-0007) — the audit log, not this column, is the durable record.

ALTER TABLE entities
    ADD COLUMN created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE obligation_types
    ADD COLUMN created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE entity_obligations
    ADD COLUMN created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE workflows
    ADD COLUMN created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE workflow_tasks
    ADD COLUMN created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE task_instances
    ADD COLUMN created_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN updated_by UUID DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid
        REFERENCES users(id) ON DELETE SET NULL;

COMMIT;
