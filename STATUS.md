# zentax-api — Build Status & Remaining Work

> Single source of truth for the state of the ZenTax Go backend. Updated as work
> lands. The cross-session roadmap lives in `../TaxFlowReports/PROJECT_PLAN.md`;
> architecture decisions in `../TaxFlowReports/docs/adr/` (ADRs 0001–0019).

**Last updated:** 2026-08-21 · **Toolchain:** Go 1.27 via gvm (`~/.gvm/gos/go1.27`;
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

### Domain modules (all tenant-scoped, hexagonal, full CRUD + acceptance tests)
1. **entities** — tax-paying orgs (hierarchical, fiscal config).
2. **obligation-types** — VAT/CIT/TP/WHT/Custom definitions; `code` unique per tenant (→409).
3. **entity-obligations** — links entity ↔ obligation type; JSONB deadline rule.
4. **workflows** — recurring/project; JSONB `selected_periods` + `due_date_rule`.
5. **workflow-tasks** — ordered task templates; per-task offset rule; JSONB `required_documents`.
6. **task-instances** — generated per-period tasks; `DATE` legal-date columns; JSONB `tax_data`.
   `POST /workflows/{id}/start` materializes an instance per period × template
   (period-end → filing deadline → task due date); idempotent-guarded (409),
   fail-closed on unsupported fiscal calendars.

Migrations (sqitch): `tenants`, `entities`, `obligation_types`, `entity_obligations`,
`workflows`, `workflow_tasks`, `task_instances` (+ boilerplate `appschema`,
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
  - **Task-lifecycle SoD** (preparer *submits* → reviewer *approves*): the `task:submit` / `task:approve`
    capabilities exist, but the submit/approve **actions** don't yet — enforcement lands with them.
  - **WorkOS SSO/SCIM (Phase 2).** `IdentityBroker` seam is stubbed only.
  - Breach-checked passwords (HIBP), password-reset / invite flows, Redis session store (Postgres for now).
- Consequence: the API is authenticated + tenant-isolated + **role- and scope-authorized on writes**. Reads
  remain tenant-wide (list-scope narrowing is a later increment); the submit/approve actions remain.

### 🟠 Domain features still to build
- **Data Templates** module — `workflow_tasks.data_template_id` / `task_instances.data_template_id`
  are plain nullable UUIDs with **no FK** until this module exists.
- **Approvals** — submit-for-approval / approve / reject on task instances, with the
  immutable approval snapshot (ADR-0018). Today only a basic status update exists;
  `approved_by` / `approved_at` / `completed_at` are columns but not driven by actions.
- **Documents / workflow-documents** — versioned, backed by object storage.
- **Audit log** (ADR-0008) — two streams, append-only, per-tenant hash-chained, PII-free
  (actor-by-ID). Not built; domain records also omit `created_by_id`/`updated_by_id` for now
  (add with auth).
- **Team members / roles**, **notifications / email / digests**, **reports**
  (compliance-heatmap / status / tax-financial / export-raw).

### 🟠 Deadline engine
- **Non-standard fiscal patterns** (445 / 454 / 544 / 13-period / weekly / custom) —
  currently fail-closed. Port the rest of the frontend fiscal-calendar, reusing its ~129 test vectors.
- **Payment / additional deadlines** beyond the filing deadline are not yet computed during generation.

### 🟡 Platform / infra (mostly Phase 2 per the ADRs)
- Object storage behind a `Storage` interface — S3 / filesystem·MinIO (ADR-0009). None wired.
- Encryption: KMS envelope + per-tenant keys behind `KeyProvider` (ADR-0006).
- Observability: OpenTelemetry → Grafana LGTM + PII redaction (ADR-0015; `logger.go` has the TODO).
- Secrets injection + backup/DR (ADR-0014) — config validates fail-closed, but Secrets Manager wiring is absent.
- Supply chain + CI: govulncheck, SBOM, signed images, OIDC, migrations-in-CI (ADR-0016/0013). **No CI pipeline yet.**
- Feature flags / entitlements (ADR-0010); region/residency cells + control-plane (ADR-0005).

### 🟡 Frontend integration
- **OpenAPI spec (httpkit) → generated TS client** — not generated. The React frontend
  (`../TaxFlowReports`) is still on MSW mocks.
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
- Migrations run as a privileged role (pgcrypto needs superuser); the **app + tests must connect
  as a non-owner, non-`BYPASSRLS` role** — `FORCE ROW LEVEL SECURITY` binds the owner too, but a
  `BYPASSRLS`/superuser connection silently defeats isolation.
- `initdb` a throwaway cluster (or reuse one), apply `migrations/deploy/*.sql` in **lexical order**
  via `psql` (= dependency order; sqitch not required for a scratch DB), then
  `GRANT SELECT,INSERT,UPDATE,DELETE,TRUNCATE ON ALL TABLES IN SCHEMA public TO zentax_app`.
- Point the app/tests at the app role via `DATABASE_URL` / `TEST_DATABASE_URL =
  postgres://zentax_app:...@host/zentax?sslmode=disable` (`DATABASE_URL` overrides config).
- Build with gvm Go 1.27: `export GOROOT="$HOME/.gvm/gos/go1.27"; export PATH="$GOROOT/bin:$PATH"`.

## Minor tech debt
- Generation loops `Create` (N inserts) — could batch.
- `strip server/` and the audit-actor columns both wait on later work (auth).
