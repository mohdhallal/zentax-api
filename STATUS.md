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
  A declarative `RouteDefinition.Tenant` flag wires the `RequireTenant` middleware +
  the transaction. Cross-table FKs are RLS-scoped, so a cross-tenant reference fails
  the FK check (→ 400) instead of leaking.
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

**Auth / identity (Increment A):** first-party email/password + server-side sessions + TOTP MFA
(`modules/identity`, `platform/crypto`); `RequireSession` supplies the tenant from the session (the
`X-Tenant-ID` header is gone); `cmd/seed-admin` bootstraps the first tenant + admin. See the Auth
section under "remaining" for what is left (scoped-RBAC enforcement, WorkOS).

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
- **Remaining:**
  - **Scoped-RBAC enforcement (Increment B).** The `user_grants` + `entity_closure` schema exist, but
    per-endpoint capability checks over entity subtrees **do not run yet** — so **any logged-in user of
    a tenant currently has full access within that tenant** (preparer≠approver SoD, scoped advisors: not
    enforced). (ADR-0012)
  - **WorkOS SSO/SCIM (Phase 2).** `IdentityBroker` seam is stubbed only.
  - Breach-checked passwords (HIBP), password-reset / invite flows, Redis session store (Postgres for now).
- Consequence: the API is authenticated + tenant-isolated, but **not yet intra-tenant authorized**.

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

## ⚠️ Operational caveats — read before running
- **Never actually run against Postgres yet.** Everything is green under `go build/vet/test`,
  and the acceptance tests encode the SQL + RLS + generation behavior — but **no live boot +
  `sqitch deploy` has been executed**. First real run needs verifying.
- **Running requires** a Postgres with the sqitch migrations applied. The app must connect as a
  **non-owner, non-`BYPASSRLS` role** — `FORCE ROW LEVEL SECURITY` applies to the table owner, but a
  `BYPASSRLS`/superuser connection would silently defeat tenant isolation. (Migrations run as a
  privileged role; the app must not.)
- **Acceptance tests** (`acceptance/`, separate Go module) need `TEST_DATABASE_URL` + migrations.
  They compile in CI/sandbox but only *run* against a live DB.
- Build with gvm Go 1.27: `export GOROOT="$HOME/.gvm/gos/go1.27"; export PATH="$GOROOT/bin:$PATH"`.

## Minor tech debt
- Generation loops `Create` (N inserts) — could batch.
- `strip server/` and the audit-actor columns both wait on later work (auth).
