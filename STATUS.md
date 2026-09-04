# zentax-api — Build Status & Remaining Work

> Single source of truth for the state of the ZenTax Go backend. Updated as work
> lands. The cross-session roadmap lives in `../TaxFlowReports/PROJECT_PLAN.md`;
> architecture decisions in `../TaxFlowReports/docs/adr/` (ADRs 0001–0019).

**Last updated:** 2026-09-04 · **Toolchain:** Go 1.27 via gvm (`~/.gvm/gos/go1.27`;
the system `/usr/local/go` is a stale 1.19). **Gate:** `go build/vet/test ./...`
green on both the main module and `acceptance/`.

---

## What is built

### Platform / foundation
- **Scaffold** from `go-api-boilerplate`, renamespaced to `github.com/mohamadhallal/zentax-api`,
  pruned to bare `health` + `auth`. Private DTOne deps swapped: `applogger`→stdlib
  `slog` (`logger/`), `config`→stdlib JSON loader + fail-closed validation (`config/`). _(ADR-0019/0014/0015)_
- **Multi-tenancy (ADR-0004):** `tenant_id` on every domain table + Postgres **RLS**
  (`ENABLE` + `FORCE` + isolation policy). The tenant GUC (`app.tenant_id`) is set
  with `SET LOCAL` inside `database.Exec.WithinTransaction` (the Tx seam) — pooling-safe.
  A declarative `RouteDefinition.Tenant` flag wires the `RequireSession` middleware +
  the transaction. Cross-table FKs are **composite `(tenant_id, id)`** so a cross-tenant
  reference fails the FK check (→ 400): Postgres FK validation *bypasses* RLS, so an
  id-only FK would silently admit the cross-tenant link (found + fixed on the first
  live-Postgres run — see below).
- **Legal dates (ADR-0002):** `shared/dateonly.Date` — a timezone-agnostic `YYYY-MM-DD`
  type (sql Scanner/Valuer for pg `date`, JSON). Used for all legal-date columns.
- **Deadline engine:** `shared/deadline` — a Go port of the frontend's tested
  `fiscal-calendar.ts` + `deadline-utils.ts` (period-end per periodicity, offset with
  month-end clamping, weekend adjustment). **Standard calendars only; fails closed on
  non-standard patterns** rather than emit a wrong deadline.
- **Object storage seam (ADR-0009 adapter #1 / ADR-0022, 2026-09-04):** `platform/storage` —
  `Storage{Put, Get (ErrNotFound), Delete (idempotent)}` over opaque keys validated by one grammar
  (`[A-Za-z0-9/_.-]`, no leading `/`, no `.`/`..` segment); no cloud-SDK type leaves an adapter.
  Adapters: **`fs`** (root dir; atomic temp-file + rename, `mkdir -p` per key, 0600 files, content
  type in a `<key>.meta` JSON sidecar — no xattrs, so it survives every volume driver) and **`s3`**
  (`aws-sdk-go-v2`; bucket + region, optional endpoint + path-style for MinIO; credentials from the
  SDK default chain; integration test skipped unless `TEST_S3_ENDPOINT` / `TEST_S3_BUCKET` /
  `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` are set, `TEST_S3_CREATE_BUCKET=1` creates the
  bucket). Config `storage {driver fs|s3, maxUploadBytes (25 MiB), fs.root, s3.{bucket, region,
  endpoint, forcePathStyle}}` in `deployment/config_files/*.json`, env overrides `STORAGE_DRIVER`,
  `STORAGE_FS_ROOT`, `STORAGE_MAX_UPLOAD_BYTES`, `STORAGE_S3_BUCKET`, `STORAGE_S3_REGION`,
  `STORAGE_S3_ENDPOINT`, `STORAGE_S3_FORCE_PATH_STYLE` (empty values never override the file —
  compose passes empty strings); validation fails closed (unknown driver, fs without root, s3
  without bucket/region), an entirely omitted section defaults to fs `./var/documents`. Wired in
  `bootstrap.NewStorage` → `NewContainer(db, WithStorage(store, max))`; the compose api container
  runs `STORAGE_FS_ROOT=/var/lib/zentax/documents` on a volume. Unit tests: fs round trip / not
  found / idempotent delete / atomic overwrite / short write / key validation incl. traversal /
  sidecar fallback; config defaults, overrides, empty-env, fail-closed.

### Domain modules (all tenant-scoped, hexagonal, full CRUD + acceptance tests)
1. **entities** — tax-paying orgs (hierarchical, fiscal config).
2. **obligation-types** — VAT/CIT/TP/WHT/Custom definitions; `code` unique per tenant (→409).
3. **entity-obligations** — links entity ↔ obligation type; JSONB deadline rule.
   **Legacy builder + registration details (2026-09-03):** migration
   `20260903000014_entity_obligation_details` adds `tax_reference_number`, `jurisdiction_state`
   (`jurisdiction` stays the country) and `currency` (ISO 4217, `len=3,uppercase`) as nullable
   columns; `weekly` joins the periodicity vocabulary (recordable — the engine still fails closed on
   it at start). `domain.DeadlineRule` now carries the whole legacy builder as data (ADR-0017):
   `paymentFixedDates` (parallel to the filing `fixedDates`, MM-DD), `periodStart {day, month}`,
   `filingOffset` / `paymentOffset {months, days}` (nil payment offset = "same as filing") and
   `additionalDeadlines [{type, months, days}]`. Acceptance: full-builder round trip (POST → GET,
   nested objects exact), PUT replaces (offsets do not linger), bad currency → 400. The frontend
   maps its legacy form through `client/src/api/entity-obligations.ts`.
4. **workflows** — recurring/project; JSONB `selected_periods` + `due_date_rule`.
5. **workflow-tasks** — ordered task templates; per-task offset rule; JSONB `required_documents`.
6. **task-instances** — generated per-period tasks; `DATE` legal-date columns; JSONB `tax_data`.
   `POST /workflows/{id}/start` materializes an instance per period × template
   (period-end → filing deadline → task due date); idempotent-guarded (409),
   fail-closed on unsupported fiscal calendars. **Dry run (2026-09-03):**
   `GET /workflows/{id}/preview` (read capability) returns the summary + every
   planned row (`templateId`, `periodCode`, date-only dates, in selectedPeriods
   order) from the **same planner** start persists — pure read, no audit entry.
   Start accepts an optional `{ taskOverrides: { "<templateId>_<periodCode>":
   { dueDate?, periodEndDate? } } }` body: a period-end override recomputes that
   instance's filing deadline + due date, a due-date override replaces the due
   date; unknown keys / bad dates → 400 before anything is created. Start also
   transitions the workflow **draft → active** in the same transaction. This is
   what the frontend's "Run Workflow" dialog is built on. **Update (2026-09-03):**
   `PUT /task-instances/{id}` accepts an optional date-only `dueDate` override
   (omit = keep) and stamps `completed_at` on `completed` / clears it on reopen;
   approval statuses are never settable through PUT (submit/approve/reject only).
   **Tax-data authority (2026-09-04, ADR-0001):** an instance inherits `data_template_id` from its
   workflow task at start (`PUT` may also attach one — `dataTemplateId`, omit = keep; another
   tenant's id → 400, by the RLS-scoped resolver and again by the composite FK). With a template
   attached, `taxData` is validated server-side through a nil-safe `TemplateResolver` port
   (implemented in the data-templates module, like identity's `AssigneeChecker`): unknown key → 400
   `tax data field "<key>" is not in the template`; numeric must be a JSON number (a numeric string is
   refused), a whole number when `allowDecimals=false`, within `min`/`max`, ≤ `decimalPlaces`
   decimals; date = `YYYY-MM-DD`; boolean = bool; text ≤ 10 000 chars; file = a reference string
   ≤ 500 chars (not resolved); a `null` value clears the key. `taxDataStatus=final` requires every
   mandatory field present and non-null (400 listing the missing field *names*), and
   `submit-for-approval` requires the same **without** requiring `final`. No template (or a template
   that vanished) = tax data stored as sent, as before. Data templates themselves: **#9 below**.

**Read models (2026-09-03, hand-written SQL over RLS-scoped tables, no writes, no migrations):**
7. **reports** (`modules/reports`) — `GET /reports/task-instances` (`task:read`, paginated, default
   100 / max 500, sort `dueDate|createdAt:asc|desc`, filters `workflowId` / `entityId` / `status`):
   task instances JOINed to their workflow, LEFT JOINed to the entity, obligation type and assignee
   user — each row carries `workflowName`/`workflowCategory`/`projectType`/`financialYear`,
   `entityId`/`entityName`, `obligationTypeId`/`obligationTypeName`, `taxType` (= the obligation
   type's template) and `assigneeName` (users.name, resolved at read time), date-only legal dates and
   UTC instants like the other DTOs. `GET /reports/workflow-stats` (`workflow:read`, not paginated):
   one object keyed by workflow id for **every** workflow in the tenant → `{totalTasks,
   completedTasks, completionPercent (rounded, 0 when empty), nextDueDate}` (earliest due date among
   non-completed instances, or null) from a single LEFT JOIN + GROUP BY. Acceptance: 4 enriched rows
   sorted by due date, filters + paging, 25 % after one completion, zeros/null for an unstarted
   workflow, empty list / `{}` for the other tenant.
   **Compliance / financial reports (2026-09-04, ADR-0021 — reporting on Postgres):** the four
   legacy report endpoints now live in the same module, all `task:read`, with the legacy
   query-parameter names (`year` / `entityId` / `obligationTypeId`, `"all"` = no filter) and exactly
   the response shapes the React pages declare, so the Express proxy passes them through untouched.
   `GET /reports/compliance-heatmap` (`viewMode=period|tax-type`): one `GROUP BY` with `FILTER`
   aggregates → rows / cols / cells (green | amber | red) / summary. `GET /reports/compliance-status`
   (`status=on_time|late|missed|not_due`, `limit` / `offset`): one instance per row classified in
   SQL against `filing_deadline`, `CURRENT_DATE` and `completed_at`'s UTC date — the same expression
   the heatmap uses for overdue / completed-late (one Go const) — with `penaltyInterest` from a
   fixed set of `tax_data` keys, plus an exact `summary` + `totalCount` over the status-filtered
   set. `GET /reports/tax-financial` (`groupBy=entity|country|taxType|period|obligation`, `limit` /
   `offset`): figures extracted from a fixed `tax_data` key set with a safe numeric cast
   (non-numbers count as 0) in one CTE; `aggregated` / `chartData` / `summary` come from ONE
   `GROUPING SETS` statement over it, `rows` is a page with an exact `totalCount`. `GET
   /reports/export-raw` (`dataset=workflows|tasks|tax-data`, `category`, inclusive date-only
   `dateFrom` / `dateTo` on `workflows.created_at` resp. `due_date`, `limit` / `offset`): exactly
   the column keys of the export page. Row lists are capped (default 1 000, max 5 000) with exact
   totals from an aggregate over the same `WHERE`; only `active` / `completed` workflows take part
   in the three compliance reports, every workflow in the export. Migration
   `20260904000015_reporting_indexes` adds `task_instances (due_date)` (export window),
   `task_instances (workflow_id, period_code)` (replacing the prefix-redundant `(workflow_id)`
   index), `workflows (financial_year)`, `workflows (status)` (parent indexes propagate to every
   hash partition, ADR-0020); at pilot scale the aggregates scan the tenant's own partition, which
   is optimal — re-check with EXPLAIN as partitions grow (ADR-0021). Review fixes (2026-09-04):
   heatmap columns are ordered by the calendar (M2 before M10), penalty/interest treat JSON 0 /
   false / "" as absent (legacy truthiness), empty-string enum filters mean "no filter", the export
   dataset uses the legacy snake_case key pairs, and the tax-data predicate is planner-estimable
   (`IS NOT NULL AND <> '{}'`). Acceptance: 2 entities × VAT/CIT,
   late / on-time / missed classification, both heatmap view modes, status filter + paging totals,
   VAT/CIT figure extraction and group-bys, the three export datasets with window / category
   filters, viewer 200, anonymous 401, empty reports for the other tenant.
8. **auditlog** (`modules/auditlog`) — `GET /audit-log`, the read API for the ADR-0008 trail behind a
   **new capability `audit:read`** held by **reviewer, manager and tenant_admin only** (viewer and
   preparer → 403; a service principal may hold it like any read). Rows are the stored PII-free
   envelope (`id`=event_id, `seq`, `action`, `resourceType`, `resourceId`, `actorId`, `occurredAt`,
   `requestId`, `details`, `hash`) plus read-time enrichment: `actorName` (LEFT JOIN users — an erased
   user renders as null, exactly as the ADR intends) and the **resolved** `workflowId`/`workflowName`
   (the event's own id for workflow events, the parent workflow for workflow_task / task_instance
   events via LEFT JOINs, null otherwise). Filters: `workflowId` (against the *resolved* id, so a
   workflow's own + its templates' + its instances' events all match), `resourceType`, `resourceId`,
   `action`, `from`/`to` (inclusive YYYY-MM-DD, half-open UTC day bounds on `occurred_at` so
   partition pruning still applies); `occurred_at DESC, seq DESC`; paginated (100 / max 500).
   Acceptance: full flow lists 6 events newest-first with `actorName` = the seeded user, workflow
   filter picks 4 (not the entity), type/id/action/date-window filters, paging totals, role gate, RLS.

9. **data-templates** (`modules/datatemplates`, 2026-09-04) — reusable typed field sets (the
   frontend's exact `DataField`: `id` 1..64 unique within the template, `name`, `fieldType`
   text|numeric|date|boolean|file, `mandatory`, `description?`, `numericValidation?` {min, max
   (min ≤ max), allowDecimals (default true), decimalPlaces 0..10, formatAsCurrency} — only on
   numeric fields) stored as a JSONB array (order kept) in `data_templates` (migration
   `20260904000018`: HASH(tenant_id) × 16, composite PK, `UNIQUE (tenant_id, name)`, CHECKed
   `template_type` VAT|CIT|TP|WHT|Custom and `category` predefined|custom, actor columns). The same
   migration turns `workflow_tasks.data_template_id` / `task_instances.data_template_id` into
   **composite FKs** (`…_data_template_fk`, column-targeted `ON DELETE SET NULL`), closing the last
   "plain UUID, no FK" gap. Capabilities `data_template:read` (every role) / `data_template:write`
   (manager, tenant_admin). Routes (all tenant-level — a tenant-wide grant): `GET /data-templates`
   (`templateType` / `category` filters, not paginated, name-sorted), `GET /{id}`, `POST` (always
   `category=custom`; a `category` in the body is an unknown field → 400; duplicate name → 409),
   `PUT /{id}` (replaces name / templateType / description / fields), `DELETE /{id}` (204; referenced
   by any workflow task or task instance → 409 "template is in use"), and `POST
   /data-templates/predefined` — idempotently inserts the missing **predefined** templates (VAT Return
   / Corporate Income Tax / Withholding Tax, ported verbatim from the frontend fixtures with the
   same field ids, matched by name + category) and returns the predefined list; `cmd/seed-admin` runs
   the same use case after creating the tenant. Predefined rows are **immutable** (PUT / DELETE →
   403). Cross-field rules live in `domain.ValidateFields` (one rule set for API + seeding). Audit:
   `data_template.created {templateType, fields}` / `.updated` / `.deleted` /
   `.predefined_seeded {inserted}` (resource = the tenant, only when something was inserted).
   Acceptance (`acceptance/modules/datatemplates`, 3 tests): seed twice → same 3 ids, list/filters,
   role gate; custom CRUD with every validation error, unknown-field `category`, 409 duplicate,
   predefined 403s, audit trail; and the tax-data authority flow — template on a workflow task →
   start → instance carries it → the whole PUT rule matrix + final/submit mandatory checks → in-use
   409 → other tenant 404 / 400 (+ the FK asserted directly in SQL). Unit tests cover every
   validation rule, seeding idempotency and the generator's template inheritance.
   **Review hardening (2026-09-04):** (1) an **in-use** template (referenced by a workflow task or an
   instance) keeps its existing field ids and types — `PUT` may rename, describe, flag mandatory
   and add fields, but removing or retyping one → 409 "template is in use: existing fields cannot be
   removed or change type…" (`domain.FieldsCompatible`), so recorded values are never orphaned or
   invalidated by a template edit; (2) `PUT /task-instances/{id}` validates tax data only when it
   **changes** (or a template is attached / `final` requested) — clients replay the stored record on
   every status / assignee save — and a key the template no longer knows is carried through
   untouched when the stored record already holds it (a new unknown key is still 400); an emptied
   typed input (`""` for a date / number / boolean / file) clears the field like `null`; (3)
   predefined seeding skips a name already taken by a **custom** template (names are unique per
   tenant across categories) instead of failing the whole seed.

10. **documents** (`modules/documents`, 2026-09-04, ADR-0022) — files attached to a workflow and
   optionally one of its task instances, behind the **`platform/storage` seam** (below). Model
   (migration `20260904000017_documents`, HASH(tenant_id) × 16, composite PKs / FKs, RLS forced):
   `documents` (`category` compliance|project, `document_type` CHECKed against the frontend's
   `workflowDocumentTypeValues` verbatim — `domain.DocumentTypes`, `label`, `notes`,
   `current_version`, soft `deleted_at`, actor columns; `task_instance_id` composite FK with
   column-targeted `ON DELETE SET NULL`) + `document_versions` (immutable rows: opaque
   `storage_key` = `tenants/<tenant>/documents/<document>/<version>`, `file_name`, `file_size`,
   `mime_type`, `sha256`, uploader; `UNIQUE (tenant_id, document_id, version)`). Capabilities
   `document:read` (every role) / `document:write` (preparer, manager, tenant_admin), writes narrowed
   to the workflow's entity subtree via `Authorizer.EnsureWorkflow`. **Transfer goes through the
   API** (no presigned URLs): `POST /workflows/{id}/documents` (multipart `file` + `documentType`,
   `label?`, `notes?`, `taskInstanceId?` — must belong to that workflow, else 400 — `category?`) →
   201 `DocumentView`; `POST /documents/{id}/versions` (multipart `file`, `label?`) → `current_version
   + 1` on the request tx (row-locked UPDATE, so concurrent uploads serialize); `GET
   /workflows/{id}/documents`, `GET /task-instances/{id}/documents` (latest versions, newest first,
   404 for a foreign id); `GET /documents` (paginated 100 / max 500; `entityId`, `workflowId`,
   `taskInstanceId`, `documentType`, `year` = `workflows.financial_year`, `search` ILIKE over file
   name / label / notes with `%`/`_` escaped; `"all"`/empty = no filter; exact totals); `GET
   /documents/{id}`, `GET /documents/{id}/versions` (desc); **downloads** `GET /documents/{id}/download`
   (latest) and `GET /documents/{id}/versions/{versionId}/download` stream the blob with the stored
   MIME type, `Content-Length`, `Content-Disposition: attachment; filename="<ASCII>";
   filename*=UTF-8''<pct>`, `Cache-Control: private, no-store`, `X-Content-Type-Options: nosniff` —
   the handler returns `(nil, nil)` (router writes nothing); every 404 (unknown / other tenant /
   deleted) is a JSON error before the first byte, a blob missing from storage is a logged 500; `PUT
   /documents/{id}` partial metadata (omitted = unchanged, `""` clears label / notes); `DELETE` = soft
   delete (204; versions + blobs retained for the ADR-0007 purge, second delete 404). **Upload rules
   (ADR-0001):** body bounded by `http.MaxBytesReader(maxUploadBytes + 64 KiB)` and the part's declared
   size → **413 `FILE_TOO_LARGE`** (the `AppError` carries the status directly); declared type
   (part `Content-Type`, fallback `application/octet-stream`) must be on the allowlist (pdf, png,
   jpeg, gif, plain, csv, json, xml, zip, the MS Office + OOXML trio, octet-stream) and the first
   512 bytes are sniffed: a concrete sniff (pdf, png, …) must equal the declared type, a generic
   one (octet-stream, zip — every OOXML file, text/plain) accepts it → otherwise **415
   `UNSUPPORTED_MEDIA_TYPE`**; sha256 + size computed while streaming (TeeReader) with a capped
   reader as defence in depth; file name = base name only, trimmed, ≤255 runes; storage `Put`
   failure → error before any row, a DB failure after `Put` deletes the blob best-effort.
   **ADR-0018:** a document attached to an **approved** task instance refuses new versions,
   metadata changes, deletion — and new attachments — with 409 "documents of an approved task are
   immutable". Views resolve `workflowName` / `entityId` / `entityName` / `financialYear` via
   RLS-scoped LEFT JOINs and `uploadedByName` via users pinned to the row's tenant. Audit
   (PII-free): `document.created {documentType, category, version, fileSize}`, `.version_added
   {version, fileSize}`, `.updated {documentType, category}`, `.deleted {}`. Acceptance
   (`acceptance/modules/documents`, 5 tests, fs adapter on a temp dir, 2 MiB test cap): upload → view
   shape → both lists → byte-identical download + every header → v2 (UTF-8 name → ASCII fallback +
   RFC 5987) → versions desc / latest / v1 by id → PUT semantics → audit; repository filters + search
   escaping + paging totals; viewer 403 / preparer 201 / anonymous 401 / other tenant 404 on every id
   route; 413 / 415 / the 400 matrix; soft delete → 404s + gone from lists + rows retained (SQL) →
   real submit/approve → 409s → workflow-level doc still editable. Unit tests (mocks for repo,
   storage, authorizer): version numbering, immutability 409, soft-delete 404, size / MIME rejection,
   instance-must-belong-to-workflow, blob cleanup on DB failure, file-name sanitising.
   **Review hardening (2026-09-04, adversarial review of the increment):** (1) downloads are truly
   **streamed** — `RouteDefinition.Stream` makes the transaction middleware write through
   (`middlewares.StreamingTransaction`) instead of buffering the body until commit, so N concurrent
   25 MiB downloads no longer hold N × 25 MiB of heap; a 4xx written before any body still rolls
   back, and a commit error after a streamed body is never glued onto it (3 middleware tests); (2)
   the ADR-0018 lock also covers a **pending** instance: once submitted for approval its documents
   refuse new versions / metadata / deletion / new attachments (409 "…awaiting approval…") until the
   reviewer decides, mirroring the task-instance freeze (acceptance asserts submit → 409s → approve →
   409s). Presigned transfer stays the ADR-0022 escape hatch for very large files.

Migrations (sqitch): `tenants`, `entities`, `obligation_types`, `entity_obligations`,
`workflows`, `workflow_tasks`, `task_instances`, `task_instance_approvals`, `actor_columns`,
`audit_log`, `service_accounts`, `entity_obligation_details`, `reporting_indexes`, `invite_tokens`,
`documents`, `data_templates` (+ boilerplate `appschema`,
`internal_api_keys`, `nexus_accounts_api_keys`).

**Auth / identity (Increments A + B):** first-party email/password + server-side sessions + TOTP MFA
(`modules/identity`, `platform/crypto`); `RequireSession` supplies the tenant from the session (the
`X-Tenant-ID` header is gone); `cmd/seed-admin` bootstraps the first tenant + admin. Plus **scoped RBAC**
(`platform/authz`): a role→capability matrix + `RequireCapability` gate every domain route (B-1), and an
`authz.Authorizer` narrows a scoped grant to its entity subtree on every write (B-2). See the Auth section
under "remaining" for what is left (read-side scope, submit/approve actions, WorkOS).

Commits: `4cdcd4e` scaffold · `2851179` tenancy+entities · `d074780` obligation-types ·
`94070d9` entity-obligations · `d91158c` workflows · `f85970a` workflow-tasks ·
`c52c25a` task-instances+generation · `b63852d` identity module · `e1c186b` wire auth (Increment A).

---

## What is missing / remaining

### 🟠 Auth & authorization
- **Authentication + sessions — DONE (Increment A, ADR-0011).** First-party email/password
  (argon2id) + server-side sessions (httpOnly + SameSite=Strict cookie, token stored hashed,
  rotation on login/MFA, idle + absolute TTLs, failed-attempt lockout) + **TOTP MFA**
  (enroll/enable/verify). The tenant now comes from the authenticated **session** (`RequireSession`);
  the interim `X-Tenant-ID` header is **gone**. First user via **`cmd/seed-admin`**. `modules/identity`
  + `platform/crypto`; auth endpoints under `/auth/*`.
- **Capability enforcement — DONE (Increment B-1, ADR-0012).** A capability model + role→capability
  matrix (`platform/authz`) and a `RequireCapability` middleware — which runs *inside* the tenant tx so
  its read of the RLS'd `user_grants` sees the session tenant — gate every domain route via a declarative
  `RouteDefinition.Capability`. Role-based **separation of duties** is enforced and tested (against live
  Postgres): a viewer cannot write, a preparer cannot approve, a manager/admin can, and a user with **no
  grant is denied (fail-closed)**. The identity `GrantRepo` loads grants on the request tx.
- **Entity-subtree scope — DONE (Increment B-2, ADR-0012).** A scoped grant (`user_grants.scope_entity_id`)
  now only authorizes **writes within its entity subtree**. An `authz.Authorizer` (injected into every write
  use case) resolves the target's owning entity + ancestor chain (recursive CTE over `parent_entity_id`,
  RLS-scoped) and admits a scoped grant only when its `scope_entity_id` is the target or one of its
  ancestors; a tenant-wide grant short-circuits without a DB lookup. Tenant-level resources (obligation
  types, project workflows, root entities) require a tenant-wide grant. Enforced across entities /
  obligation-types / entity-obligations / workflows / workflow-tasks / task-instances / workflow-start, and
  tested live (a manager scoped to A acts on A + descendants, never sibling B). `entity_closure` stays
  unused — the parent-walk CTE is the source of truth; the closure is a future perf optimization.
- **Remaining:**
  - **Read-side scope** — list/get endpoints stay tenant-wide; narrowing what a *scoped* user can *see*
    (filtering lists to their subtree) is a later increment. Write scope (above) is the SoD-critical half.
  - **Approval amendment path (rest of ADR-0018)** — an approved record is now *frozen* (in-place edits
    rejected), but the versioned re-approved *amendment* chain (lawfully correcting a filed record) + the
    document-version / rule-version snapshots need documents + audit first. The freeze + preparer≠approver
    SoD + the submit/approve/reject actions are **done** (see Domain features).
  - **WorkOS SSO/SCIM (Phase 2).** `IdentityBroker` seam is stubbed only.
  - Breach-checked passwords (HIBP), password-reset flow, Redis session store (Postgres for now).
    ~~Invite flow~~ — **done (2026-09-04, see Member administration below).**
- **Member administration — DONE (2026-09-04).** `modules/identity` grows a members surface backed by the
  same users / user_grants rails. **Capabilities:** a new `member:read` joins the read set every role holds
  (the tenant directory — names, emails, roles — is visible to every member so tasks can be assigned);
  `member:manage` (tenant_admin only) guards every mutation. **Routes** (all `Tenant:true`, tenant from the
  session — users is not RLS'd, so every members query pins `tenant_id = requester tenant` explicitly and a
  foreign user id is a plain 404): `GET /members?kind=human|service&status=&limit&offset` (default 100 /
  max 500, sorted name → email, grants embedded via ONE `user_grants ⟕ entities` query — no N+1 —
  `scopeEntityName` from the RLS-scoped join) + `GET /members/{id}`; `POST /members` (email lowercased,
  duplicate → 409, `scopeEntityId` of another tenant → 400 via the composite FK) creates a `kind=human,
  status=invited` user with no password, its grant and an **invite token**; `POST /members/{id}/invite`
  re-issues for a still-invited member (409 otherwise) and tombstones the earlier unused tokens;
  `PUT /members/{id}` (name + `active|disabled`): self-disable → 400, invited → active → 400 (activation is
  accept-invite only), disabling revokes every session, API token and outstanding invite; `PUT
  /members/{id}/role` replaces all grants, `POST /members/{id}/grants` adds one (201), `DELETE
  /members/{id}/grants/{grantId}` removes one (204). **Guards:** the tenant must keep ≥1 ACTIVE HUMAN
  member holding a tenant-wide `tenant_admin` grant — checked on the request tx after every grant mutation
  (and on disable), 409 rolls the whole request back; a service account can never be granted
  `tenant_admin` (400, at creation and by later grant — no self-replication). **Invite token model**
  (migration `20260904000016_invite_tokens`, RANGE(created_at) monthly + `_default`, in `migrate.sh`'s
  create-ahead): `"zti_" + 32 random bytes base64url`, cleartext returned ONCE (`inviteToken` +
  `inviteExpiresAt`, 7 days), stored as SHA-256 only, `accepted_at` / `revoked_at` tombstones, **not**
  RLS-scoped (looked up by hash before any context, like api_tokens). **`POST /auth/accept-invite`
  (public):** `{token, password (12..200), name?}` → sets the argon2id hash, `status=active`,
  `accepted_at`, returns `{email}`; single use (a second accept → 400); every failure is the same generic
  400 "invite is invalid or has expired". The handler carries no Tx flag — the use case resolves the token
  first, then runs the writes through `database.Exec.WithinTransaction` with the tenant **taken from the
  token row** and the activating user as requester, so `audit_log` (RLS'd) receives `member.activated`
  in the right tenant chain. Audit actions (PII-free details): `member.invited {role, scoped}`,
  `member.invite_reissued`, `member.updated {status}`, `member.role_changed {role, scoped, grants}`,
  `member.activated`. **Task assignment:** `PUT /task-instances/{id}` with `assigneeId` now requires an
  ACTIVE HUMAN user of the caller's tenant (400 "assignee is not an active member of this tenant") via a
  nil-safe `AssigneeChecker` port on the task-instance use cases, implemented in identity's pg repo (users
  pinned to the requester's tenant) and wired in the container; `/reports/task-instances` resolves such
  users' `assigneeName`. Acceptance (`acceptance/modules/members`, 8 tests): invite → list → accept →
  real login → `/auth/me`, single-use / wrong / expired / short-password 400s, every role reads but only
  admins mutate, cross-tenant 404s + scope 400, re-issue rotates the token, disable kills sessions +
  login and re-enable restores it, last-admin guard on role / grant delete, grant add/remove with
  resolved scope names, service-account admin 400, and the whole task-assignment matrix (foreign / invited
  / service / disabled → 400, active → 200 with `assigneeName`). Unit tests cover the use-case guards with
  mocks. **Review hardening (2026-09-04, adversarial review of the increment):** (1) `tenant_admin` is
  **tenant-wide only** — a scoped admin grant is refused (400) on invite / role / add-grant, because
  capability checks on tenant-level routes (`/members`, `/service-accounts`) are scope-agnostic and a
  "limited admin" would have administered the whole tenant; (2) `member:manage` is **human-only at the
  gate** (`authz.HumanOnly`, like `task:approve`), so a machine can never administer members even through
  a legacy grant; (3) the last-admin guard is serialised per tenant with a transaction-scoped advisory
  lock (`pg_advisory_xact_lock`, `MemberRepository.LockAdminGuard`) taken before every admin-guard
  mutation — two concurrent demotions can no longer each see the other admin still standing; (4)
  re-enabling a disabled human that never accepted its invite (no password) returns it to `invited`
  (re-issue the invite) instead of minting an `active` account nobody can sign in to (service accounts
  stay `active`); (5) `PUT /task-instances/{id}` validates `assigneeId` only when it **changes** —
  clients send the current assignee back on every save, so a task whose assignee was since disabled
  stays editable and reassignable. Acceptance +3 (scoped admin 400s, invitee restore, machine admin
  403), unit +3. **Known limitation (logged):** `users.email` is globally unique, so `POST /members`
  answers 409 for an address registered in *another* tenant — a cross-tenant email-existence oracle
  inherent to v1 identity; ADR-0005's domain-scoped identity is the fix. **Remaining:** e-mail delivery
  of the invite link (the token is returned to the admin for now), invite expiry configurability,
  read-side scope narrowing of the directory, rate limiting on `POST /auth/accept-invite`.
- **Machine identity — DONE (agentic-AI B1).** Service accounts are `users.kind='service'` rows (grants,
  audit actor_id, and created_by attribution reuse the same rails; synthetic internal email; login rejects
  them). Bearer **API tokens** (`ztx_...`, sha256-hashed at rest, mandatory expiry, revocation tombstones)
  authenticate via `Authorization: Bearer` in the same `RequireAuth` middleware, then flow through the
  identical grants → capability → subtree-scope → RLS chain. **Approval is human-only**: a service
  principal is denied `task:approve` regardless of role (enforced at the capability gate + the Authorizer),
  and machines cannot hold `member:manage` implicitly (no self-replication). Admin surface (gated by
  member:manage): `POST/GET /service-accounts`, `POST /service-accounts/{id}/tokens` (cleartext shown
  once), `POST /tokens/{id}/revoke`. Verified live incl. attribution, revocation, expiry, cross-tenant 404s.
  **Remaining:** OAuth 2.1 client credentials (MCP-aligned, Phase 2 with WorkOS); per-principal rate limits.
- Consequence: the API is authenticated (humans **and machines**) + tenant-isolated + **role- and
  scope-authorized on writes**, with the **preparer→reviewer approval flow + SoD + immutable-approval
  lock** in place and approval human-only. Reads remain tenant-wide (list-scope narrowing is a later
  increment).

### 🟠 Domain features still to build
- ~~**Data Templates** module~~ — **done (2026-09-04, see Domain modules #9)**; the
  `data_template_id` columns are now composite FKs and tax data is validated server-side.
- **Approvals — DONE (core, ADR-0018/0012).** `POST /task-instances/{id}/{submit-for-approval,approve,reject}`
  drive the lifecycle: a preparer (`task:submit`) submits → `pending_approval` (records `submitted_by`); a
  different reviewer (`task:approve`) approves → `completed` (`approved_by`/`approved_at`/`completed_at`), or
  rejects → back to `in_progress` with a reason. **Server-enforced SoD** (approver ≠ submitter) + an
  **immutable lock** (an approved instance rejects in-place edits). Verified live. **Remaining:** the
  versioned *amendment* chain + snapshotting linked document/rule versions (audit is now built —
  waits on documents + rule versioning).
- ~~**Documents / workflow-documents** — versioned, backed by object storage.~~ **Done (2026-09-04,
  see Domain modules #10 + the storage seam).** Remaining: the ADR-0007 purge job for soft-deleted
  documents, per-tenant envelope encryption at the adapter (ADR-0006), frontend wiring.
- **Audit log — DONE (stream 1 core, ADR-0008).** `platform/audit` + the `audit_log` table: every
  domain mutation (20 use-case sites) appends a **PII-free, actor-by-ID envelope** (action,
  resource, UTC instant, request_id, whitelisted `details` only — status transitions/counts, never
  free text) **on the same transaction** — the write and its evidence commit or roll back together.
  **Append-only at the database** (RLS policies exist only for INSERT/SELECT → UPDATE/DELETE affect
  zero rows even for the app role) + a **per-tenant hash chain** (sha256 over the canonical envelope;
  per-tenant advisory-lock-serialized `seq`; `VerifyChain` recomputes it). **Actor attribution:**
  `created_by`/`updated_by` on all domain tables, defaulted/stamped from the `app.user_id` GUC bound
  at the Tx seam. Verified live incl. tamper attempts + cross-tenant isolation. **Remaining:** WORM
  export to S3 Object Lock (Phase 2), the centralized **security stream** (auth events — Phase 2;
  interim: structured slog). The read API for the Audit Trail page is **done** (2026-09-03:
  `GET /audit-log`, `modules/auditlog`, capability `audit:read` — see "Read models" above).
- ~~**Team members / roles**~~ (done 2026-09-04 — see Member administration under Auth), **notifications / email / digests**, **reports**
  (compliance-heatmap / status / tax-financial / export-raw — the enriched task-instance list and
  per-workflow stats under `/reports` are done, 2026-09-03; the compliance aggregates are not).

### 🟠 Deadline engine
- **Non-standard fiscal patterns** (445 / 454 / 544 / 13-period / weekly / custom) —
  currently fail-closed. Port the rest of the frontend fiscal-calendar, reusing its ~129 test vectors.
- **Payment / additional deadlines** beyond the filing deadline are not yet computed during generation.

### 🟡 Platform / infra (mostly Phase 2 per the ADRs)
- ~~Object storage behind a `Storage` interface — S3 / filesystem·MinIO (ADR-0009). None wired.~~
  **Done (2026-09-04):** `platform/storage` + `fs` / `s3` adapters, wired for documents (see
  Platform / foundation). Remaining: SSE-KMS / per-tenant envelope keys (ADR-0006), MinIO compose profile.
- Encryption: KMS envelope + per-tenant keys behind `KeyProvider` (ADR-0006).
- Observability: OpenTelemetry → Grafana LGTM + PII redaction (ADR-0015; `logger.go` has the TODO).
- Secrets injection + backup/DR (ADR-0014) — config validates fail-closed, but Secrets Manager wiring is absent.
- Supply chain + CI: govulncheck, SBOM, signed images, OIDC, migrations-in-CI (ADR-0016/0013). **No CI pipeline yet.**
- Feature flags / entitlements (ADR-0010); region/residency cells + control-plane (ADR-0005).

### 🟡 Frontend integration
- **OpenAPI contract: FINISHED (B3, 2026-08-23).** `/swagger/spec.json` now describes the real API:
  **security schemes are `sessionCookie` (apiKey-in-cookie, named from config) + `bearerToken`
  (`ztx_...`)** — the boilerplate gateway schemes are gone; every tenant route declares
  cookie-OR-bearer alternatives; public routes (login, health) carry none. Every operation surfaces
  its RBAC permission as **`x-required-capability`** (36 of 44 ops). **Responses are schematized**
  via shared component envelopes — `SuccessEnvelope` / `PaginatedEnvelope` (driven by the
  `Paginated` flag) / `ErrorEnvelope` — with the accurate error contract per route (401/403 on
  authed routes, 404 on id-addressed, 409 on tenant mutations). **Proven generator-ready:**
  `openapi-typescript` consumed the live spec cleanly (typed operations + envelopes). The `data`
  payloads are deliberately untyped until per-module response DTOs land with the client-generation
  pass. **Generator fix (2026-09-03):** request-body fields backed by JSONB value types
  (`domain.Periods`, `domain.DueDateRule`, `DocumentRequirements`) were rendered as `string`;
  `typeToSchema` now follows slices → `array`+items, structs → nested `object` (with their own
  required/enum), `time.Time` → `date-time`, `[]byte` → string, and validator rules after `dive`
  apply to array items (`generator_nested_test.go`). The frontend's workflow wizard is typed off
  these shapes. **The frontend IS wired (2026-08-23, hybrid):** the React app authenticates and runs the core
  chain against this API through a transitional Express adapter (`TaxFlowReports/server/go-proxy.ts` —
  path rewrites, envelope unwrapping, cookie passthrough, per-endpoint body whitelists because this API
  rightly rejects unknown fields). Verified live in the browser (login → entities from Postgres → UI
  create → audit entry). Unmigrated surfaces (dashboard aggregates, documents, templates, team,
  notifications, reports) remain on MSW/legacy Express until their Go modules exist; remaining forms'
  payloads migrate module-by-module.
- **`zentax-mcp` sidecar — DEFERRED (logged 2026-08-23).** Both readiness blockers are closed
  (machine identity + audit) and the spec now carries `x-required-capability` for tool generation,
  so the sidecar is buildable when picked up — see
  `../TaxFlowReports/docs/assessments/agentic-ai-mcp-readiness.md` for the design (thin stateless
  translator, tools from the route registry, approval never exposed as a tool).
- **Strip `server/` from `TaxFlowReports`** — deferred until the Go API + client replace the Express dev server.

---

## ✅ Verified against live Postgres (2026-08-21)
First real end-to-end run on **Postgres 14** (local Homebrew cluster, connecting as a
**non-superuser `zentax_app` role** so RLS is genuinely enforced):
- All **13 migrations apply cleanly**; **all 8 acceptance suites pass** against the live DB
  (`TEST_DATABASE_URL` → the non-`BYPASSRLS` role).
- **Live server boot** (`cmd/server` on :3000) + **`cmd/seed-admin`** (tenant + admin + grant) +
  real HTTP: `POST /auth/login` sets the session cookie, `GET /auth/me` is **200 with it / 401
  without**, authed CRUD works, and a **second tenant sees zero of the first tenant's rows**
  (RLS tenant isolation, demonstrated live).
- **Two bugs found + fixed on this run:**
  1. **Cross-tenant FK bypass** — Postgres FK checks bypass RLS, so id-only FKs admitted
     cross-tenant references. Fixed with **composite `(tenant_id, id)` FKs** (parents carry a
     `UNIQUE (tenant_id, id)` target; children reference `(tenant_id, fk_id)`).
  2. **List ordering** was blanket-`DESC`. Added a per-repo `DefaultOrderDesc` flag:
     `created_at` stays newest-first; **task-instances (`due_date`) + workflow-tasks
     (`order_index`) now sort ASC** — earliest deadline / natural step order.

### Runbook — local Postgres (no Docker needed)
- **Postgres 15+ required** since the partition retrofit (column-targeted
  `ON DELETE SET NULL (parent_entity_id)` on `entities`); the compose stack runs PG16. The old
  Homebrew PG14 cluster no longer works — easiest scratch DB now: `CREATE DATABASE` inside the
  running compose postgres (`localhost:5433`, superuser `postgres`).
- Migrations run as a privileged role (pgcrypto needs superuser); the **app + tests must connect
  as a non-owner, non-`BYPASSRLS` role** — `FORCE ROW LEVEL SECURITY` binds the owner too, but a
  `BYPASSRLS`/superuser connection silently defeats isolation.
- `initdb` a throwaway cluster (or reuse one), apply `migrations/deploy/*.sql` in **lexical order**
  via `psql` (= dependency order; sqitch not required for a scratch DB), then
  `GRANT SELECT,INSERT,UPDATE,DELETE,TRUNCATE ON ALL TABLES IN SCHEMA public TO zentax_app`.
- Point the app/tests at the app role via `DATABASE_URL` / `TEST_DATABASE_URL =
  postgres://zentax_app:...@host/zentax?sslmode=disable` (`DATABASE_URL` overrides config).
- Build with gvm Go 1.27: `export GOROOT="$HOME/.gvm/gos/go1.27"; export PATH="$GOROOT/bin:$PATH"`.

## ⚠️ Standing schema directive (ADR-0020, 2026-08-23)
**Partition new tables by default** — HASH(tenant_id) for tenant-scoped OLTP (the composite
`(tenant_id, id)` unique/FK pattern already satisfies partition-key rules), RANGE(time) for
append-only/expiring streams (audit/email logs, sessions, api_tokens). Not partitioning requires a
stated reason in the migration comment (fine for small registries: tenants, users, grants).
**Retrofit gate: CLOSED (2026-09-03)** — baseline migrations rewritten in place (pre-release history
edit; existing DBs must be reset — `docker compose down -v`): 6 domain tables HASH(tenant_id, 16)
with composite `(tenant_id, id)` PKs; `audit_log`/`sessions`/`api_tokens` RANGE(month) + `_default`
backstop. Use the helpers from the tenants migration for every new table:
`SELECT create_hash_partitions('<table>', 16)` / `SELECT ensure_month_partitions('<table>', <first
month>, 3)` — both set **RLS ENABLE+FORCE with no policies on each partition** (direct partition
access denied; only the parent, whose policies apply, is reachable). `migrate.sh` runs the
create-ahead maintenance on every start; add any new range table to that block. Bonus fix in the
retrofit: `entities.parent_entity_id` is now a composite FK with column-targeted SET NULL — the last
id-only FK, which (FK checks bypass RLS) had admitted cross-tenant parents. 11/11 acceptance suites
+ unit tests green on the partitioned schema; direct-partition denial, cross-tenant-parent rejection,
and pointer-only SET NULL verified live via psql.

## Minor tech debt
- Generation loops `Create` (N inserts) — could batch.
- `strip server/` and the audit-actor columns both wait on later work (auth).
