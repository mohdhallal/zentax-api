package pg

// documentColumns is the documents row projection — WITHOUT tenant_id (RLS infra).
const documentColumns = `id, workflow_id, task_instance_id, category, document_type, label, notes, ` +
	`current_version, deleted_at, created_at, updated_at, created_by, updated_by`

// versionSelect projects a version row plus the uploader's display name. users
// is NOT RLS-scoped, so the join is pinned to the row's tenant: a foreign uuid
// resolves to NULL, never to a name.
const versionSelect = `
SELECT v.id, v.document_id, v.version, v.storage_key, v.file_name, v.file_size, v.mime_type, v.sha256,
       v.created_at, v.created_by, u.name AS uploaded_by_name
FROM document_versions v
LEFT JOIN users u ON u.id = v.created_by AND u.tenant_id = v.tenant_id`

// viewFrom joins a LIVE document to its latest version (the row whose version
// equals current_version — exactly one by the unique constraint), its workflow
// and entity (RLS-scoped LEFT JOINs) and the uploader (users pinned to the
// tenant). Filters are NULL-tolerant so one statement serves every
// combination; the search pattern arrives pre-escaped with '\' as the escape.
const viewFrom = `
FROM documents d
JOIN document_versions v ON v.tenant_id = d.tenant_id AND v.document_id = d.id AND v.version = d.current_version
LEFT JOIN workflows w ON w.id = d.workflow_id
LEFT JOIN entities e ON e.id = w.entity_id
LEFT JOIN users u ON u.id = v.created_by AND u.tenant_id = v.tenant_id
WHERE d.deleted_at IS NULL
  AND ($1::uuid IS NULL OR d.workflow_id = $1::uuid)
  AND ($2::uuid IS NULL OR d.task_instance_id = $2::uuid)
  AND ($3::uuid IS NULL OR w.entity_id = $3::uuid)
  AND ($4::varchar IS NULL OR d.document_type = $4::varchar)
  AND ($5::varchar IS NULL OR w.financial_year = $5::varchar)
  AND ($6::text IS NULL
       OR v.file_name ILIKE $6::text ESCAPE '\'
       OR d.label ILIKE $6::text ESCAPE '\'
       OR d.notes ILIKE $6::text ESCAPE '\')`

const viewSelect = `
SELECT d.id, d.workflow_id, d.task_instance_id, d.category, d.document_type, d.label, d.notes,
       v.file_name, v.file_size, v.mime_type, v.sha256,
       d.current_version AS version, v.id AS version_id,
       v.created_by AS uploaded_by, u.name AS uploaded_by_name,
       d.created_at, d.updated_at,
       w.name AS workflow_name, w.entity_id, e.name AS entity_name, w.financial_year` + viewFrom

const viewOrder = ` ORDER BY d.created_at DESC, d.id DESC`

const viewCount = `SELECT COUNT(*)::int` + viewFrom

const insertDocument = `
INSERT INTO documents (id, workflow_id, task_instance_id, category, document_type, label, notes, current_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, 1)`

const insertVersion = `
INSERT INTO document_versions (id, document_id, version, storage_key, file_name, file_size, mime_type, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// bumpVersion increments current_version of a live document (row-locked, so
// two concurrent uploads serialize and the unique (document, version) holds)
// and optionally relabels it.
const bumpVersion = `
UPDATE documents
SET current_version = current_version + 1,
    label = CASE WHEN $2::text IS NULL THEN label ELSE NULLIF($2::text, '') END,
    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING current_version`

// updateMetadata: NULL argument = unchanged; an empty string clears label/notes.
const updateMetadata = `
UPDATE documents
SET label = CASE WHEN $2::text IS NULL THEN label ELSE NULLIF($2::text, '') END,
    notes = CASE WHEN $3::text IS NULL THEN notes ELSE NULLIF($3::text, '') END,
    document_type = COALESCE($4::varchar, document_type),
    category = COALESCE($5::varchar, category),
    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL`

const softDelete = `
UPDATE documents
SET deleted_at = NOW(),
    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL`
