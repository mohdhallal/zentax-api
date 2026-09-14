-- Deploy imports
BEGIN;

-- Spreadsheet ingest (PB-C5): the two tables behind upload → dry run → commit.
--
-- The product exists to replace spreadsheets and until now had no way to read
-- one. These tables are what makes reading one SAFE rather than merely
-- possible:
--
--   import_batches   one row per uploaded file: what was promised, and (later)
--                    that it was applied.
--   import_rows      the PLAN, one row per file row, frozen at the dry run.
--
-- The honesty property the whole feature rests on is enforced by the shapes
-- here, not by the use case remembering:
--
--   the plan cannot be edited after it is shown
--       import_rows has no UPDATE path at all (the guard below), and a batch
--       may move only from 'validated' to 'committed'. So "the dry run said
--       twelve creates" is a claim the database will not let anyone revise
--       after the fact; the commit either matches the frozen plan or refuses
--       the whole file.
--
--   a file with one bad row can never be committed
--       status 'rejected' is terminal — the state guard permits no transition
--       out of it. The customer fixes the file and uploads again. A partially
--       imported tax book is worse than a rejected file.
--
--   a record that moved under the plan is not silently overwritten
--       target_updated_at. The commit updates WHERE updated_at = the version
--       the plan was computed from; a record someone edited in between fails
--       to match and the batch is refused rather than reverted to the state
--       the plan assumed.
--
-- Deliberately NOT here, and each absence is a decision:
--
--   the uploaded bytes — parsed once, never stored. The batch keeps the file's
--       name, size and sha256, which is enough for a customer to prove which
--       file produced which plan, and adds no blob to keep, encrypt or erase.
--
--   a natural-key table — the key is a fact about the RECORDS, not about the
--       import: an entity is matched on its name (folded), an obligation on
--       its (entity, obligation type) pair. Keeping the key in the records
--       themselves is what lets an import update an entity somebody created
--       through the UI, which a private key table could never match.
--
-- Deliberately partitioned by HASH(tenant_id), the ADR-0020 default, unlike the
-- small ledgers that take its exception: a tenant that imports weekly writes
-- thousands of rows a year here, and a tenant offboarding delete has to stay
-- bounded.

-- ── import_batches ──────────────────────────────────────────────────────────
--
-- `status` is the whole lifecycle:
--
--   validated  every row parsed and passed validation — committable, once.
--   rejected   at least one row is invalid — TERMINAL.
--   committed  applied. Frozen.
CREATE TABLE import_batches (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,

    -- What the file is being imported AS. The vocabulary is closed and is the
    -- same word the URL carries (/imports/entities).
    kind VARCHAR(24) NOT NULL CHECK (kind IN ('entities', 'entity_obligations')),

    -- What was uploaded. The bytes are NOT kept; these three identify the file
    -- well enough for a customer to prove which one produced this plan.
    file_name TEXT     NOT NULL,
    byte_size BIGINT   NOT NULL CHECK (byte_size >= 0),
    checksum  CHAR(64) NOT NULL, -- sha256 (hex) of the uploaded bytes

    -- The plan's shape, denormalized from import_rows so listing batches is one
    -- index scan rather than a per-batch aggregate.
    row_count       INTEGER NOT NULL CHECK (row_count       >= 0),
    create_count    INTEGER NOT NULL CHECK (create_count    >= 0),
    update_count    INTEGER NOT NULL CHECK (update_count    >= 0),
    unchanged_count INTEGER NOT NULL CHECK (unchanged_count >= 0),
    invalid_count   INTEGER NOT NULL CHECK (invalid_count   >= 0),

    status VARCHAR(12) NOT NULL CHECK (status IN ('validated', 'rejected', 'committed')),

    created_by   UUID NOT NULL DEFAULT NULLIF(current_setting('app.user_id', true), '')::uuid,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    committed_by UUID,
    committed_at TIMESTAMPTZ,

    PRIMARY KEY (tenant_id, id),

    CONSTRAINT import_batches_counts CHECK (
        create_count + update_count + unchanged_count + invalid_count = row_count
    ),
    -- A rejected batch is one with invalid rows, and a validated one has none.
    -- Stated as a constraint so the status can never disagree with the plan it
    -- summarizes.
    CONSTRAINT import_batches_status_matches_plan CHECK (
        (status = 'rejected' AND invalid_count > 0)
     OR (status <> 'rejected' AND invalid_count = 0)
    ),
    CONSTRAINT import_batches_commit_state CHECK (
        (status =  'committed' AND committed_at IS NOT NULL AND committed_by IS NOT NULL)
     OR (status <> 'committed' AND committed_at IS NULL     AND committed_by IS NULL)
    )
) PARTITION BY HASH (tenant_id);

SELECT create_hash_partitions('import_batches', 16);

CREATE INDEX idx_import_batches_kind_created ON import_batches (tenant_id, kind, created_at DESC, id);

COMMENT ON TABLE import_batches IS
    'Spreadsheet ingest: one uploaded file, its dry-run plan summary and (once applied) its commit.';
COMMENT ON COLUMN import_batches.checksum IS
    'sha256 (hex) of the uploaded bytes. The bytes themselves are never stored.';

-- ── import_rows ─────────────────────────────────────────────────────────────
--
-- The plan, one row per file row, written at the dry run and never rewritten.
--
-- `payload` is the validated RECORD the file asks for — the same body the
-- single-record create route would have been given by hand, with the defaults
-- it applies already applied. Its references (a parent entity, an obligation
-- type) are still written as the file writes them, by name; they are resolved
-- against the tenant at the moment of writing, because that is the moment the
-- answer has to be true.
--
-- What makes the report exact is not the payload but the three columns beside
-- it: `action` says what will happen, `target_id` says to which record, and
-- `target_updated_at` says from which version. All three are re-derived inside
-- the commit's transaction and must agree, or nothing is written at all.
CREATE TABLE import_rows (
    tenant_id UUID NOT NULL DEFAULT NULLIF(current_setting('app.tenant_id', true), '')::uuid
        REFERENCES tenants(id) ON DELETE CASCADE,
    batch_id UUID NOT NULL,

    -- The row number the CUSTOMER sees in their own spreadsheet, so an error
    -- can be found by looking at the file. It is not a dense 1..n sequence:
    -- title blocks and blank lines are skipped without shifting it.
    row_number INTEGER NOT NULL CHECK (row_number >= 1),

    action VARCHAR(10) NOT NULL CHECK (action IN ('create', 'update', 'unchanged', 'invalid')),

    -- The natural key this row resolved to, rendered for a human: an entity's
    -- folded name, an obligation's "entity | obligation type". '' only for a
    -- row so malformed that no key could be read from it.
    natural_key TEXT NOT NULL,

    -- The record the key resolved to, for 'update' and 'unchanged'. NULL for a
    -- create (nothing to point at yet) and for an invalid row.
    target_id         UUID,
    target_updated_at TIMESTAMPTZ,

    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Which fields of the existing record the update moves — the difference
    -- between "3 updates" and "3 updates, and here is what each one replaces".
    -- Empty for every other action: a create changes nothing that existed.
    -- JSONB rather than TEXT[]: every other structured column in this schema is
    -- jsonb, and one array type that needs a driver-specific scanner is not
    -- worth a second way of reading a list.
    changed_fields JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The issues the reading half raised for this row, errors and warnings
    -- alike; an invalid row carries at least one error.
    issues JSONB NOT NULL DEFAULT '[]'::jsonb,

    PRIMARY KEY (tenant_id, batch_id, row_number),
    FOREIGN KEY (tenant_id, batch_id) REFERENCES import_batches(tenant_id, id) ON DELETE CASCADE,

    CONSTRAINT import_rows_target CHECK (
        (action IN ('update', 'unchanged') AND target_id IS NOT NULL AND target_updated_at IS NOT NULL)
     OR (action IN ('create', 'invalid')   AND target_id IS NULL     AND target_updated_at IS NULL)
    ),
    CONSTRAINT import_rows_invalid_is_explained CHECK (
        action <> 'invalid' OR jsonb_array_length(issues) > 0
    ),
    CONSTRAINT import_rows_changed_fields CHECK (
        jsonb_typeof(changed_fields) = 'array'
        AND ((action =  'update' AND jsonb_array_length(changed_fields) > 0)
          OR (action <> 'update' AND jsonb_array_length(changed_fields) = 0))
    )
) PARTITION BY HASH (tenant_id);

SELECT create_hash_partitions('import_rows', 16);

COMMENT ON TABLE import_rows IS
    'Spreadsheet ingest: the dry-run plan, one row per file row, frozen at validation and never rewritten.';
COMMENT ON COLUMN import_rows.payload IS
    'The validated record the file asks for, defaults applied; references are resolved when it is written.';
COMMENT ON COLUMN import_rows.target_updated_at IS
    'The version the plan was computed from; the commit refuses if the record has moved since.';

-- ── Row-level security ──────────────────────────────────────────────────────
--
-- Ordinary tenant isolation (ADR-0004) on both. No cross-tenant door of any
-- kind: every import runs inside one tenant's request transaction.
ALTER TABLE import_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_batches FORCE  ROW LEVEL SECURITY;
ALTER TABLE import_rows    ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_rows    FORCE  ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON import_batches
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY tenant_isolation ON import_rows
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── The plan is frozen ──────────────────────────────────────────────────────
--
-- SECURITY INVOKER (the default), so a psql session and a superuser are bound
-- by it too. Without these two triggers the honesty of the dry run would be a
-- property of the use case rather than of the record: anyone able to write the
-- table could bring the plan into line with what the commit actually did.
CREATE FUNCTION import_row_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'import row %/% is the plan shown to the customer and cannot be changed', OLD.batch_id, OLD.row_number
        USING ERRCODE = 'ZT036',
              HINT = 'upload the corrected file again; a plan is never revised in place';
END;
$$;

COMMENT ON FUNCTION import_row_immutable() IS
    'Imports: a planned row is frozen at the dry run (SQLSTATE ZT036); only a tenant/batch cascade removes it.';

CREATE TRIGGER import_row_immutable
    BEFORE UPDATE ON import_rows
    FOR EACH ROW EXECUTE FUNCTION import_row_immutable();

CREATE FUNCTION import_batch_state_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status <> 'validated' THEN
        RAISE EXCEPTION 'import batch % is %, which is final', OLD.id, OLD.status
            USING ERRCODE = 'ZT036',
                  HINT = 'a rejected batch is never committable and a committed batch is never re-run';
    END IF;
    IF NEW.status <> 'committed' THEN
        RAISE EXCEPTION 'import batch % may only move from validated to committed', OLD.id
            USING ERRCODE = 'ZT036';
    END IF;

    IF NEW.tenant_id       IS DISTINCT FROM OLD.tenant_id
    OR NEW.kind            IS DISTINCT FROM OLD.kind
    OR NEW.file_name       IS DISTINCT FROM OLD.file_name
    OR NEW.byte_size       IS DISTINCT FROM OLD.byte_size
    OR NEW.checksum        IS DISTINCT FROM OLD.checksum
    OR NEW.row_count       IS DISTINCT FROM OLD.row_count
    OR NEW.create_count    IS DISTINCT FROM OLD.create_count
    OR NEW.update_count    IS DISTINCT FROM OLD.update_count
    OR NEW.unchanged_count IS DISTINCT FROM OLD.unchanged_count
    OR NEW.invalid_count   IS DISTINCT FROM OLD.invalid_count
    OR NEW.created_by      IS DISTINCT FROM OLD.created_by
    OR NEW.created_at      IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'import batch % may only record its commit', OLD.id
            USING ERRCODE = 'ZT036',
                  HINT = 'the file it describes and the plan it promised are frozen at upload';
    END IF;

    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION import_batch_state_only() IS
    'Imports: a batch may only move validated → committed, recording who committed it and when (SQLSTATE ZT036).';

CREATE TRIGGER import_batch_state_only
    BEFORE UPDATE ON import_batches
    FOR EACH ROW EXECUTE FUNCTION import_batch_state_only();

COMMIT;
