# zentax-api — Build Status & Remaining Work

> Single source of truth for the state of the ZenTax Go backend. Updated as work
> lands. The cross-session roadmap lives in `../zentax-ui/PROJECT_PLAN.md` (the UI
> repository, `github.com/mohdhallal/zentax-ui`, formerly `TaxFlowReports`);
> architecture decisions in `../zentax-ui/docs/adr/` (ADRs 0001–0025).

**Last updated:** 2026-09-06 · **Toolchain:** Go 1.27 via gvm (`~/.gvm/gos/go1.27`;
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
- **Deadline engine (ADR-0023, 2026-09-05 — every pattern):** `shared/deadline` — pure civil-date
  arithmetic over `dateonly.Date`, the single authority for periods and deadlines (the generator
  and `GET /entities/{id}/periods` build the same `Calendar`). `NewCalendar(pattern,
  financialYearEnd MM-DD, weekEndDay, yearEndRule, customPeriods, fiscalYear)` — the fiscal year
  is the calendar year it ENDS in — yields `Periods(periodicity)` / `Period(code, periodicity)` /
  `PeriodEnd` as `{Code, Label, Start, End}`. Rules: **standard** = calendar months from the
  fiscal start month (annual = the FY-end month's month end, the v1 simplification the ADR
  records; weekly = seven-day weeks from the FY start, W52 absorbs the remainder and ends on the
  FY end). **445 / 454 / 544 / 13-period / weekly** = week-based years anchored on
  `financialYearEnd`: the year end is the `fiscalWeekEndDay` on/before the anchor (`last`) or
  the one closest to it (`nearest`, a three-day gap resolves backwards), the year runs from the
  day after the previous year end, and it must span 364 (52 weeks) or 371 (53 weeks) days —
  anything else is an error; months are `[4,4,5]` / `[4,5,4]` / `[5,4,4]` × 4, `[4]` × 13
  (`P1..P13`) or `[4,4,5]` × 4 for the weekly pattern, quarters 13 weeks, halves 26, and the
  53rd week goes to the LAST period (M12 / P13 / Q4 / H2 / Y1 / W53). **custom** = the entity's
  ordered `customPeriods` are the only codes (monthly / quarterly / bi-annual return the same
  list), each `MM-DD` placed by the standard year rule (a start after its end straddles the year
  boundary and moves back a year), annual = Y1 on the FY end, weekly refused. Codes are stable
  across patterns (`M1..M12` / `P1..P13` / custom, `Q1..Q4`, `H1..H2`, `Y1`, `CY1`,
  `W1..W52/53`); labels are short and deterministic (`M12 (24 Dec 2023 – 3 Feb 2024)`, custom
  periods carry their name). Also `ApplyMonthDayOffset` (months clamped, then days) and
  `FirstFixedDateOnOrAfter` for the payment rules; `ApplyOffset` / `ApplyWeekendAdjustment` /
  `FiscalYearStartMonth` / `PeriodEndDate` unchanged. `IsSupportedPattern` is true for every
  pattern — **fail closed moved into the engine** (unknown periodicity, custom without periods,
  a code outside the pattern, an impossible week year → error → 400). Unit tests: hand-verified
  tables for NRF fiscal 2023 (Saturday nearest 31 Jan → 29 Jan 2023 – 3 Feb 2024, 53 weeks, all
  12 months / 4 quarters / W53), the following 52-week year, `last` vs `nearest` on a Sunday
  anchor (same year end, 53 vs 52 weeks), the nearest boundary (−3 wins, −4 loses), all seven
  week-end days, 445 / 454 / 544 boundaries on one year, 13-period with the 53rd week in P13,
  the weekly pattern, standard weekly remainder (common + leap year, April start), the UK
  April-start year, custom periods (plain, year-straddling, April start), and every fail-closed
  case.
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
- **Deployment readiness (staging / production, ADR-0014 — 2026-09-05):** the API boots on ECS
  from a shipped config file + injected environment, and refuses to boot otherwise.
  - **Config files** `deployment/config_files/{development,staging,production}.json` (the Dockerfile
    copies the whole directory; `APP_ENV` picks one). `staging` / `production` are production-shaped:
    `database.url ""`, `auth.encryptionKey ""`, `cors.allowedOrigins []` (all three come from the
    environment), `auth.sessionCookieSecure true`, `log {json, info}`, `metrics.enabled true`,
    `storage.driver s3` (bucket + region from `STORAGE_S3_*`; a self-host production sets
    `STORAGE_DRIVER=fs` + `STORAGE_FS_ROOT`), `swagger.enabled` **true in staging, false in
    production** (new flag — `development.json` says `true`, an omitted flag means off; `bootstrap`
    only mounts `/swagger` when it is on).
  - **Environment variables** (an EMPTY value never overrides the file, like `STORAGE_*`):
    `DATABASE_URL` — wins when set; otherwise `DB_HOST` + `DB_PORT` (default `5432`) + `DB_NAME` +
    `DB_USER` + `DB_PASSWORD` + `DB_SSLMODE` (default `require`) compose
    `postgres://user:pass@host:port/name?sslmode=…` with the user, password and name percent-encoded
    (a Secrets-Manager-generated password with `@ / : % #` or spaces survives; IPv6 hosts bracketed).
    `AUTH_ENCRYPTION_KEY` — base64 of exactly 32 bytes, **or** a passphrase of ≥ 32 characters derived
    into the 32-byte key with SHA-256 (managed secret stores generate alphanumeric strings, not raw
    key bytes; the derivation is deterministic, so rotation = a new passphrase + re-encrypt).
    `AUTH_SESSION_COOKIE_SECURE` (true/false), `CORS_ALLOWED_ORIGINS` (comma-separated, trimmed),
    `LOG_FORMAT` / `LOG_LEVEL`, `SWAGGER_ENABLED`, plus the existing `STORAGE_*`.
    `PUBLIC_BASE_URL` (2026-09-05) → `app.publicBaseUrl`: the product's public origin — the web
    tier's hostname (`https://eu.staging.zentax.software`), **not** this API's listener — the base
    for every absolute link the API hands out of band (the e-mailed invite link, once delivery
    exists; nothing renders absolute URLs yet). Optional (a cell without its custom domain passes
    `""`); when set it is trimmed, a trailing slash stripped, and must parse as an absolute http(s)
    URL without userinfo/query/fragment — **https** in staging/production (a Secure cookie never
    travels over http). The SaaS task sets it from the cell's `publicHostname`; `development.json`
    says `http://localhost:5000` (the web tier's `npm run dev` origin). `nexusInternalApi.baseUrl` is
    unrelated (the boilerplate's outbound API-key-provisioning client). `cmd/seed-admin`
    goes through the same `config.Load`, so it accepts the `DB_*` parts and is held to the same rules;
    its admin password comes from `SEED_ADMIN_PASSWORD` (preferred — ECS injects it from Secrets
    Manager, so it never appears in a task definition or CloudTrail) or from `--password` for local
    use, refusing to run when neither is set, holding it to the accept-invite length policy
    (12–200 chars, `resolvePassword` unit-tested), and never printing it.
  - **Fail-closed rules** for `APP_ENV=staging|production` (every violation reported in ONE startup
    error, each naming the variable to set): encryption key present, decodable, and **not the
    development key** (decoded bytes compared against `config.DevelopmentEncryptionKey`, which a
    test pins to `development.json`); `sessionCookieSecure` true; `database.url` without
    `sslmode=disable` (URL or key=value DSN); `cors.allowedOrigins` non-empty **and** without `"*"`
    (`go-chi/cors` treats an empty list as `*`, so empty is refused too); `log.format json`.
    Proven live: `APP_ENV=production` with a compose `DATABASE_URL …?sslmode=disable` is refused by
    the rule; with `sslmode=require` (or the `DB_*` default) config validates and the boot then
    fails at connect because the compose Postgres has no TLS — validation runs before any I/O.
  - **Probes:** `GET /health` stays the liveness probe (no dependencies; the Dockerfile
    `HEALTHCHECK` keeps it). **`GET /health/ready`** is the readiness probe: `SELECT 1` through the
    pool with a 2 s timeout → `200 {status:true, data:{db:"ok"}}` or `503 {status:false, error:{code:
    UNAVAILABLE}}` (new `httperr.ErrUnavailable` → 503; the driver error goes to the log, not the
    body). Same exposure as `/health` (external + internal, unauthenticated).
  - **`Dockerfile.migrate`** (repo root): `postgres:16-alpine` + `migrations/deploy` +
    `deployment/docker/migrate.sh`, runs as the image's unprivileged `postgres` user, same env as
    the compose job (`PGHOST PGPORT PGUSER PGPASSWORD PGDATABASE PGSSLMODE APP_DB_USER
    APP_DB_PASSWORD`). `migrate.sh` now prints the target, every migration it applies, a
    `N newly applied / M already applied / K in ledger` summary and the app role's
    `superuser/bypassrls/login` flags; the app role's password is **re-set on every run** (a role that
    pre-exists with another password — even as a superuser — is brought to
    `NOSUPERUSER NOBYPASSRLS` + the current secret; verified against a scratch database on the compose
    network: 24 files applied, login with the new password, old password refused, second run
    idempotent). Role name and password travel as psql variables (`:"user"` / `:'pw'` + `format()` +
    `\gexec`), so a generated password with quotes or `$` cannot break or inject into the SQL.
  - Unit tests (`config/deploy_test.go`, `modules/health/handlers/ready_test.go`): both key forms,
    the 32-char boundary, short/empty rejection, dev-key rejection by bytes, every deployed rule +
    the all-at-once report, development exemption, every env override + the empty-env rule, URL
    composition (escaping round trip through `url.Parse`, IPv6, defaults, `DATABASE_URL` wins), the
    shipped files (production-shaped, refuse to boot alone, boot with env), ready 200 / 503 /
    timeout / no-db.
- **Infrastructure (`zentax-api/infra`, ADR-0024 decision 8 — 2026-09-05):** this repository owns the
  **foundations** of each AWS cell as a CDK v2 (TypeScript) app with its own `package.json`; the UI
  repository (`../zentax-ui/infra`) owns only the web tier and the front door, and the two apps meet
  on a named CloudFormation export contract — no third repo, no construct shared across apps.
  - **Stacks:** account-level `ZenTax-GithubOidc` (`CliCredentialsStackSynthesizer`, deployed by the
    admin identity, never by CI: the `token.actions.githubusercontent.com` provider, **four** deploy
    roles — `zentax-deploy-<env>-api` trusting `repo:mohdhallal/zentax-api:environment:<env>` (push
    `zentax/<env>/{api,migrate}`, `ecs:RunTask` on the `zentax-<env>-migrate` / `-seed` task
    definitions in cluster `zentax-<env>`, update the api service, `iam:PassRole` on
    `zentax-<env>-{api,migrate,seed}-*` to ECS only, read the api/migrate/seed logs) and
    `zentax-deploy-<env>-web` trusting `repo:mohdhallal/zentax-ui:environment:<env>` (push
    `zentax/<env>/web`, update the web service, `PassRole` on `zentax-<env>-web-*`, read the web
    logs); both may assume the `cdk-hnb659fds-*` bootstrap roles and read stacks — the repositories
    are constants `DEPLOY_REPOSITORIES` in `lib/github-oidc-stack.ts`; plus the managed policy
    `ZenTaxCfnExecutionPolicy` for `cdk bootstrap --cloudformation-execution-policies`, exported as
    `zentax-cfn-execution-policy-arn`). Per environment (`--context env=staging-eu|production-eu`;
    `<Env>` / `<env>` in this block read `StagingEu` / `staging-eu` since ADR-0025 below):
    `ZenTax-<Env>-Network` (VPC, endpoints, flow logs, **every** security group incl. the ALB's and
    the web tasks' — the Web stack imports and attaches them, immutably), `ZenTax-<Env>-Data` (KMS,
    RDS Postgres 16, the db-master / app-db / auth-encryption-key / seed-admin secrets, documents
    bucket, `api` + `migrate` ECR, api/migrate/seed log groups, the `zentax-<env>-alarms` topic + RDS
    alarms), `ZenTax-<Env>-Cluster` (ECS cluster `zentax-<env>`, Cloud Map `zentax-<env>.local`,
    migrate + seed task definitions with pinned-revision outputs) and `ZenTax-<Env>-Api`
    (`lib/api-stack.ts`: the api task definition — env + `ValueFrom` secrets exactly as before —
    Fargate service on the Cluster stack's cluster with the Network api SG, Cloud Map `api`, CPU
    autoscaling + circuit breaker, S3/KMS grants, `zentax-<env>-api-running-tasks` alarm). The web
    ECR repository, the web log group and the origin-verify secret **moved** to the UI's Web stack.
  - **Exports (`lib/exports.ts`, byte-identical to `../zentax-ui/infra/lib/exports.ts` — a test
    compares them when the sibling checkout exists; the file is printed in ADR-0024):**
    `zentax-<env>-` + Network `vpc-id, vpc-cidr, availability-zones, public-subnet-ids,
    private-subnet-ids, alb-sg-id, web-sg-id, api-sg-id, jobs-sg-id`; Data `alarm-topic-arn,
    documents-bucket, ecr-api, ecr-migrate`; Cluster `cluster-name, cluster-arn, namespace-name,
    namespace-id, namespace-arn, migrate-task-family, migrate-task-arn, seed-task-family,
    seed-task-arn, seed-admin-secret-arn`; Api `api-service-name, api-internal-url
    (http://api.zentax-<env>.local:3000), api-task-arn`; OIDC `zentax-cfn-execution-policy-arn,
    zentax-deploy-role-<env>-{api,web}`. The UI's Web stack emits `web-service-name, alb-dns,
    ecr-web, web-task-arn`; its Edge stack (us-east-1) `cloudfront-url, cloudfront-id` (nothing
    imports those two). `CORS_ALLOWED_ORIGINS` is now the optional
    context key `corsAllowedOrigins` (validated `scheme://host` list, `*` refused; placeholder
    `https://zentax-<env>.invalid` until the UI deploy publishes `cloudfront-url` — the web tier
    strips `Origin`, so nothing depends on it; README step 8).
  - **Pipeline (`.github/workflows/deploy.yml`, OIDC only):** push to `main` → staging; dispatch →
    staging or production (gated by the GitHub `production` environment's reviewers). Role
    `zentax-deploy-<env>-api` → build + push api + migrate images tagged with this repo's 12-char sha
    → `cdk deploy --exclusively Network Data Cluster --context imageTag=<sha>` → migrate task from
    the pinned `MigrateTaskDefinitionArn` (`scripts/ecs-pin-taskdef.sh` verifies the
    `zentax-<env>-*` roles + ECR origin, re-registers only if the tag differs; `ecs-run-task.sh`
    waits, checks the exit code, tails the log — non-zero stops the run before Api changes) →
    `cdk deploy --exclusively Api` → `ecs wait services-stable` → `seed` job on dispatch input
    `seed=true` (`SEED_ADMIN_EMAIL` / `SEED_TENANT_SLUG` / `SEED_TENANT_NAME` / `SEED_TIMEZONE`
    environment secrets as a command override; the password is injected from the
    `zentax/<env>/seed-admin` secret, never through GitHub). `ci.yml` gained an `infra` job (`npm ci`,
    `tsc`, `jest`, synth of both cells with `CDK_DEFAULT_ACCOUNT` / `CDK_DEFAULT_REGION` only, no
    credentials) and a `workflows` job (actionlint, shellcheck, the helper-script tests against a
    fake `aws`). First deploy of a cell: OIDC → bootstrap `eu-central-1` + `us-east-1` with both
    execution policies → Network + Data + Cluster by hand → this pipeline → the UI pipeline (Web +
    Edge) → seed here. Runbook: `../zentax-ui/docs/ops/environments.md`; costs and the per-resource table:
    `infra/README.md`.
- **Custom domain, DNS and regional cell names (ADR-0025 — 2026-09-05):** settled before the first
  deploy, while every name is still free to change.
  - **Cells are keyed by tier + region label:** environments are `staging-eu` / `production-eu`
    (`^(staging|production)-[a-z]{2}$`, explicit `tier` + `regionLabel` context validated against the
    name); stacks `ZenTax-StagingEu-*` / `ZenTax-ProductionEu-*`, exports `zentax-staging-eu-<key>`
    through the unchanged `exportName` (`lib/exports.ts` still byte-identical to the UI copy), roles
    `zentax-deploy-{staging,production}-eu-{api,web}` trusting GitHub environments of the same names,
    Cloud Map `zentax-staging-eu.local`, ECR `zentax/staging-eu/{api,migrate}`, tags `Environment` +
    `Tier` + `RegionLabel`. `deploy.yml` / `ci.yml` and the README use the new names.
  - **`ZenTax-Dns`** (`lib/`, account-level next to `ZenTax-GithubOidc`: `CliCredentialsStackSynthesizer`,
    admin-deployed, never by the pipeline, `RETAIN`, stack region `eu-central-1`): the `zentax.software`
    hosted zone — registrar stays Squarespace, name servers switched to the zone's four NS (outputs
    `HostedZoneId`, `NameServers`) — with today's records as code (Squarespace site `A` × 4 + `CNAME
    www`, the five Workspace `MX`, DKIM at `google._domainkey` emitted as 255-char chunks and proven
    on the synthesized template, `google-site-verification`) plus the missing **SPF**
    (`v=spf1 include:_spf.google.com ~all`, sharing the apex TXT set with the verification value —
    one TXT set per name) and **DMARC** (`_dmarc`, `p=none`, `rua=mailto:dmarc@zentax.software`).
    The two panel-truncated values come from top-level `dns.{googleSiteVerification,
    googleDkimPublicKey}` context; empty = record omitted + a `cdk.Annotations` warning, so CI synth
    stays green and the runbook pastes both before the first deploy. The HTTPS/SVCB and
    `_domainconnect` records are intentionally not recreated.
  - **Per-environment `publicHostname`** (`eu.staging.zentax.software` / `eu.app.zentax.software`;
    the UI app adds `app.zentax.software` as the entry hostname + the Edge-stack certificate, aliases
    and 301): `corsAllowedOrigins` defaults to `https://<publicHostname>` (an explicit list still
    wins, the `.invalid` placeholder only when unset) and the api task receives
    `PUBLIC_BASE_URL=https://<publicHostname>` (omitted when unset). **Go:** `config` gains the new
    field `app.publicBaseUrl` (`AppConfig.PublicBaseURL`) with the `PUBLIC_BASE_URL` env override
    (trimmed, trailing slash stripped, https-only in staging/production, an empty value never
    overrides — unit-tested, listed in the env table). **Nothing consumes it yet:** invite links are
    built by the UI from `window.location.origin`; the API-side link (`app.publicBaseUrl` +
    `/accept-invite?token=`) arrives with e-mail delivery, and the OpenAPI `servers` entry is
    deliberately *not* wired to it. `nexusInternalApi.baseUrl` is unrelated and untouched.
  - **Execution policies:** `ZenTaxCfnExecutionPolicy` is **unchanged** (6121 of the 6144-character
    cap, no `acm:` action). The six ACM actions the Edge stack's custom-domain certificate needs —
    `acm:{Request,Describe,Delete}Certificate`, `acm:{Add,Remove}TagsToCertificate`,
    `acm:ListTagsForCertificate` on `*` (certificate ARNs are unknowable in advance) — live only in
    the second managed policy **`ZenTaxCfnExecutionPolicyEdge`** (`makeCfnExecutionPolicyEdge` in
    `lib/github-oidc-stack.ts`, exported as `zentax-cfn-execution-policy-edge-arn`); record writes on
    `hostedzone/*` were already in the first, hosted-zone creation stays out of both (admin-only),
    and both caps are enforced by the jest test. The bootstrap takes **both ARNs, comma-separated**:
    `--cloudformation-execution-policies "arn:aws:iam::160117555326:policy/ZenTaxCfnExecutionPolicy,arn:aws:iam::160117555326:policy/ZenTaxCfnExecutionPolicyEdge"`
    (an account bootstrapped with one policy is fixed by re-running the bootstrap with both — it
    updates the execution role in place). **Still not deployed** — the Dns deploy and the Squarespace
    NS change are the first two runbook steps once a non-root identity exists.
  - **Zone safety + real Google values (2026-09-05):** `ZenTax-Dns` has `terminationProtection: true`
    and an aspect RETAINs every `AWS::Route53::RecordSet` (DeletionPolicy + UpdateReplacePolicy) next
    to the RETAINed zone, so a stack delete never empties the delegated zone (jest checks every record
    set and the flag). The untruncated `googleSiteVerification` token and `googleDkimPublicKey`
    (`p=`) are now pasted in `infra/cdk.json` (public DNS values, not secrets): synth emits the apex
    TXT set with the token **and** SPF, `google._domainkey` in 255-char chunks, and no warning; the
    Dns tests assert only what holds in either state on the committed file, with explicit
    empty-values / full fixtures, plus one test pinning that the committed values are non-empty and
    well-formed.

### Domain modules (all tenant-scoped, hexagonal, full CRUD + acceptance tests)
1. **entities** — tax-paying orgs (hierarchical, fiscal config). **Fiscal calendars (2026-09-05,
   ADR-0023):** migration `20260905000019_fiscal_calendar` adds `fiscal_week_end_day` (CHECKed
   monday…sunday, default `saturday`), `fiscal_year_end_rule` (CHECKed `last` | `nearest`, default
   `nearest`) and `custom_periods` (JSONB, NULL for every non-custom entity) — the API fields
   `fiscalWeekEndDay`, `fiscalYearEndRule`, `customPeriods [{code 1..16 unique within the entity,
   name 1..100, startDate MM-DD, endDate MM-DD}]` (always an array in the view). Cross-field rules
   in `domain.ValidateFiscalConfig` (create + update, 400 before any write): strict `MM-DD` for
   `financialYearEnd` and every period date (`04-31` refused, `02-29` accepted and clamped when
   placed), unique codes, and `custom` requires ≥ 1 period (other patterns may keep a list).
   `domain.CalendarFor(entity, fiscalYear)` is THE way the generator and the periods endpoint obtain
   the engine's `Calendar`. **`GET /entities/{id}/periods?periodicity=&financialYear=`**
   (`entity:read`, swagger-visible) → `[{code, label, startDate, endDate}]` (date-only strings)
   from that calendar — the single source of period options for the UI; 404 for a foreign /
   unknown entity, 400 with the engine's message for a combination it cannot compute (weekly under
   custom, a bad week year). Acceptance (`entities` +1, `fiscal` suite): defaults saturday /
   nearest / `[]`, POST → GET → PUT round trip of the three fields, every validation 400 (nothing
   created), the periods endpoint for standard / UK April-start / 445 / 13-period / custom entities,
   viewer 200, anonymous 401, other tenant 404, query 400s.
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
   **Every fiscal calendar + the payment deadline (2026-09-05, ADR-0023):** the generator builds the
   entity's `Calendar` (`entitiesdomain.CalendarFor`) and resolves each selected code through it —
   445 / 454 / 544 / 13-period / weekly / custom now generate; a code outside the pattern (an `M1`
   on a 13-period entity, an unlisted custom code, `M13`) is a 400 naming it, before anything is
   created — the old "pattern not supported" refusal is gone. **`paymentDeadline`** (column
   `task_instances.payment_deadline DATE`, NULL only on rows generated before the migration →
   `null` in the view) is derived per instance from the entity obligation linking the workflow's
   entity and obligation type, read through a nil-safe `ObligationResolver` port
   (`FindByEntityAndType`, implemented in the entity-obligations pg repo, RLS-scoped, active first
   then oldest): `paymentOffset` → period end + months (month-end clamped) + days, then weekend
   adjustment (the rule's own, else the workflow rule's); else `paymentFixedDates` → the first
   `MM-DD` on/after the period end (its year, then the next), no adjustment; else, or no obligation
   → equal to the filing deadline. Task templates' `dueDateReference` gains **`payment_deadline`**
   (a `payment`-type task created without a reference defaults to it), and start's
   `taskOverrides` gain `paymentDeadline` (a period-end override recomputes filing + payment +
   due; a payment override replaces the derived value and moves a task referencing it). Emitted
   on every instance view (list, get, preview rows). `weekly` joins the workflow periodicity
   vocabulary. Acceptance (`acceptance/modules/fiscal`, 8 tests): 445 monthly FY2024 → 12
   instances matching the engine table + the same rows from the periods endpoint (+ W53, quarters);
   13-period quarterly (Q4 = 14 weeks, P1..P13, `M1` → 400); custom trimesters (3 instances, same
   list for three periodicities, weekly 400, unlisted code 400); weekly under standard (52 rows,
   W52 ends 31 Dec); payment offset 1 month 10 days next-business-day → 12 May 2025 on M3, the
   payment task due 3 days before it, preview agrees, override replaces, malformed override 400;
   fixed payment dates incl. the next-year wrap and no weekend adjustment; no obligation → payment
   = filing on a UK April-start year + a legacy NULL row renders `null`. Unit tests cover the same
   rules with mocks (445 through the generator, 13-period refusing M codes, all three payment
   branches, the workflow-adjustment fallback, both overrides, custom, fail-closed).
   **Review hardening (2026-09-05, adversarial review of the increment):** custom periods must
   **tile the year** — every period after the first starts the day after its predecessor ends,
   which pins its calendar year (a trailing period running into January lands in the next year)
   and makes a gap or an out-of-order table a 400 naming the period; `selectedPeriods` accept the
   16-character custom codes the entity may define (was 10); a cleared override date (`""`) means
   "no override" instead of a 400; `/reports/task-instances` rows carry `paymentDeadline` too.
   **Project workflows generate instances (2026-09-06):** `GET /workflows/{id}/preview` and `POST
   /workflows/{id}/start` accept `workflowCategory=project` (the generator used to refuse it).
   A project has no fiscal periods: the planner materializes **one instance per template** (orderIndex
   order) under the single period code **`PROJECT`** (`workflowsdomain.ProjectPeriodCode`, the
   legacy engine's convention), with `periodEndDate = filingDeadline = endDate`, **`paymentDeadline`
   NULL** (`null` in every view — `PreviewTask.paymentDeadline` is now nullable too), and `dueDate` =
   end date ± the template offset, every `dueDateReference` (period end, filing, payment) resolving to
   the end date. `endDate` is required — absent / blank → 400 `project workflows need an end date`,
   not a date → 400 — shared by preview and start; `entityId` stays optional and the entity calendar,
   the entity obligation and the workflow `dueDateRule` play no part. Overrides address
   `<templateId>_PROJECT` (`dueDate`, `periodEndDate`; a `paymentDeadline` override → 400 "project
   workflows have no payment deadline"); the draft → active flip, the 409 idempotency guard and the
   `workflow.started` audit entry (`periods: 1`) are the recurring ones. A workflow of any other
   category is a 400. Project instances take part in `/task-instances`, `/reports/task-instances`,
   `/reports/workflow-stats` and every `export-raw` dataset, but **not** in the three compliance /
   financial reports (see #7). `GET /task-instances?status=` now also accepts `pending_approval`
   (the reports list already did). Acceptance (`taskinstances`, +3): preview + start of a 3-template
   project (dates per reference, `PROJECT` filter, audit details, 409, preview after start), the
   end-date 400 on both routes with nothing created and start after `PUT` sets the date, overrides
   (dueDate applied; paymentDeadline / period-shaped key 400 before anything is created). Unit tests
   cover the project planner (one per template, references, nil payment deadline, no entity lookup,
   preview, end-date guards, idempotency, overrides, unknown category).

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
   SQL against `filing_deadline`, **the tenant's "today"** and `completed_at`'s UTC date — the same expression
   the heatmap uses for overdue / completed-late (one Go const) — with `penaltyInterest` from a
   fixed set of `tax_data` keys (`shared/taxkeys`), plus an exact `totalCount` over the
   status-filtered set and a `summary` over the whole classified set (the 2026-09-06 note below).
   `GET /reports/tax-financial` (`groupBy=entity|country|taxType|period|obligation`, `limit` /
   `offset`): figures extracted from a fixed `tax_data` key set with a safe numeric cast
   (non-numbers count as 0) in one CTE; `aggregated` / `chartData` / `summary` come from ONE
   `GROUPING SETS` statement over it, `rows` is a page with an exact `totalCount`. `GET
   /reports/export-raw` (`dataset=workflows|tasks|tax-data`, `category`, inclusive date-only
   `dateFrom` / `dateTo` on `workflows.created_at` resp. `due_date`, `limit` / `offset`): exactly
   the column keys of the export page. Row lists are capped (default 1 000, max 5 000) with exact
   totals from an aggregate over the same `WHERE`; only `active` / `completed` **recurring**
   workflows take part in the three compliance reports, every workflow (and every instance, project
   ones included) in the export. Migration
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
   **Report semantics (2026-09-06):** (1) **`compliance-status` summary is global** — `summary
   {total, onTime, late, missed, notDue}` is computed over the classified set BEFORE the `status`
   filter (the `year` / `entityId` / `obligationTypeId` filters still narrow it), while `rows` and
   `totalCount` describe the status-filtered page — so `?status=late` returns the late rows with
   `totalCount = late` and cards that still sum to the whole population (`total = onTime + late +
   missed + notDue`); the legacy "summary after the filter" is gone (`domain.ComplianceStatusResult`,
   one `FILTER` aggregate for the filtered total). (2) **`tax-financial` periods follow the
   calendar**: `chartData` and `groupBy=period` order their buckets by `MIN(period_end_date)` then
   code (M1, M2, …, M9, M10, M12 — never text order), other groupings keep label order. (3) **Only
   recurring workflows participate** in heatmap / compliance-status / tax-financial
   (`w.workflow_category = 'recurring'` in the shared participating-workflows predicate): project
   instances (period `PROJECT`, generated since 2026-09-06) have no obligation period to be compliant
   against; they stay in `/reports/task-instances`, `/reports/workflow-stats` and `export-raw`.
   (4) The figure keys the tax-financial CTE, the export and the penalty/interest text read are now
   the **canonical tax-data keys + alias chains of `shared/taxkeys`** (`outputVat|outputTax|salesVat`,
   `inputVat|…`, `netVat|vatPayable`, `taxableIncome|taxableProfit`, `taxLiability|taxPayable|
   corporateTax`, `whtAmount|withholdingTax|taxWithheld`, `amount|totalAmount|taxAmount`,
   `engagementCost|cost|filingCost`, `penaltyAmount|penalty`, `interestAmount|interest`; export pairs
   camelCase|snake_case) — same SQL, one source of truth shared with the predefined data templates
   (#9), so a template-bound instance finally feeds the report. Acceptance: the compliance fixture
   re-pinned (summary 13/1/1/11/0 under every status filter, chart + period groups M1, M2, M12),
   `TestTaxFinancialPeriodsFollowTheCalendar` (M12, M9, M1, M11, M10 listed out of order → M1, M9,
   M10, M11, M12 for the chart under every groupBy), `TestProjectWorkflowsStayOutOfTheComplianceReports`
   (project + recurring side by side: task list 3 / stats / export 1-3-2-1 datasets vs heatmap,
   status and financial = recurring only, project tax data never counted).
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

   **Task summary + shared task filters (2026-09-08, pagination increment 1):** `GET /reports/task-summary`
   (`task:read`, not paginated) answers the dashboard / tasks-page tiles as ONE aggregate over
   `task_instances ⋈ workflows` — `{today, total, completed, active, overdue, dueToday, dueThisWeek,
   awaitingApproval, completionRate, byStatus{not_started, in_progress, in_review, pending_approval,
   completed, blocked}}`, every key always present — so the numbers stay exact past any page cap
   instead of being computed in the browser over the first 500 rows. `today` is the tenant's civil
   date (ADR-0023 §6, the same `tenantToday` sub-select the compliance classification uses, hoisted by
   the planner into InitPlans evaluated once per statement); `overdue` = open AND due < today,
   `dueToday` = open AND due = today, `dueThisWeek` = open AND today < due ≤ the Saturday ending the
   week (`sql_shared.go`: `tenantWeekEnd`, `dueOverdue`, `dueToday`, `dueThisWeek`, `statusRank`) — a
   completed instance is never bucketed, `awaitingApproval` = `pending_approval`, `completionRate`
   rounds like `workflow-stats` (`domain.roundedPercent`, 0 on an empty set). The summary and
   `/reports/task-instances` share one filter set (`dto.TaskFilterQuery`): `workflowId`, `entityId`,
   `assigneeId`, `financialYear` (repeatable; `none` = workflows with no financial year, i.e. project
   workflows), `status` (stored values plus the pseudo-value **`open`** = not completed) and
   `workflowCategory`; predicates are assembled dynamically for the filters that are set (never
   `($n IS NULL OR col = $n)`, which pgx's statement cache would turn into a generic plan without the
   index), the feed's exact total COUNTs over the base join without the display LEFT JOINs, and the
   dashboard's priority list is `status=open&sort=dueDate:asc&limit=5`. Unit: predicates carry
   `tenantToday` and never `CURRENT_DATE`, week end is `6 - EXTRACT(DOW …)`, the WHERE builder emits
   only set filters, rounding 1/3 → 33 and 0/0 → 0, byStatus always six keys. Acceptance
   (`TestTaskSummary`): three tenants in UTC / Pacific/Kiritimati / Pacific/Pago_Pago with due dates
   pinned to yesterday / today / tomorrow / next week UTC plus a completed, a submitted, an in-progress
   and a blocked instance — every tile and byStatus per zone with `today` = the tenant's civil date
   (at least one zone flips against UTC at any hour), summary ⇔ feed consistency (`active` = the
   `status=open` total, `overdue` = open rows due before `today`), every filter incl.
   `financialYear=2025&financialYear=none`, `status=open`, viewer 200, anonymous 401, another tenant
   at zeros with `today` set; `status=open` on the feed. Oracle: `seed-demo verify` gained
   `checkTaskSummary` (today, eight counters, completionRate, byStatus incl. missing/unknown keys) —
   403 checks per full run, 0 differences on acme / globex / initech against the API built from this
   tree.

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
   / Corporate Income Tax / Withholding Tax — the frontend fixtures' names, labels, flags and
   validation, with the **canonical tax-data keys** as field ids since 2026-09-06, see below; matched
   by name + category) and returns the predefined list; `cmd/seed-admin` runs
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
   **Canonical tax-data keys (2026-09-06, product bug fix):** the predefined templates stored their
   figures under fixture-era ids (`f-vat-output`, `f-cit-due`, `f-wht-amount`, …) that no report
   read, and `ValidateTaxData` refuses keys outside the attached template — so a template-bound
   instance could never feed the tax-financial report (it showed as a zero row). The field ids are
   now the canonical keys of the new **`shared/taxkeys`** package, the single source the reports SQL
   reads too: VAT `salesTotal`, `outputVat`, `inputVat`, `netVat`; CIT `profitBeforeTax`,
   `adjustments`, `taxableIncome`, `taxRate`, `taxLiability`; WHT `whtBase`, `whtRate`, `whtAmount`
   (names, labels, mandatory flags and numeric validation unchanged). `SeedPredefined` is keyed by
   **name**, so a re-seed would have left old rows untouched — migration
   **`20260906000021_canonical_tax_keys`** renames the ids on every tenant's predefined rows and the
   same keys inside the `tax_data` of instances bound to them (directly or through their workflow
   task); custom templates and free-form tax data are untouched. It is a data migration over
   FORCE-RLS tables, so it lifts FORCE on `data_templates` / `workflow_tasks` / `task_instances` for
   its own transaction (the RDS master user is an owner, not a superuser — under FORCE the tenant
   policy would silently match nothing) and restores it before COMMIT; verified against a throwaway
   DB seeded with old-shaped rows as a non-superuser owner role (templates renamed across tenants,
   bound instances renamed via either path, an unbound instance and a custom template with the same
   id left alone, FORCE back on). Unit test: every figure key the tax-financial report derives for
   VAT / CIT / WHT (`taxkeys.FigureKeys`) is a numeric field of that type's predefined template.
   Acceptance (`datatemplates`, +1): `TestPredefinedTemplatesFeedTheFinancialReport` — VAT / CIT /
   WHT tasks bound to the predefined templates, a fixture-era key refused (400), canonical figures
   recorded → tax-financial 1000/400/600 · 450 · 300 (total 1350) and the tax-data export read them.
   The UI's generated `client/src/api/types.gen.ts` still carries the old `f-vat-sales` example
   (regenerate with `npm run gen:api`); no UI code references the old ids.

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
`documents`, `data_templates`, `fiscal_calendar`, `tenant_timezone`, `canonical_tax_keys`,
`pagination_indexes`
(+ boilerplate `appschema`, `internal_api_keys`, `nexus_accounts_api_keys`).

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

## Performance — ADR-0021 rule 7 (measured)

Budgets: p95 ≤ **500 ms** for dashboard/list reads, ≤ **2 s** for aggregate reports, at 10⁵
instances per tenant. Fixture: the `scale` tenant (`seed-demo scale`, 48 entities × 5 obligation
types × 8 fiscal years → 1,925 workflows, **97,152 instances**) next to the three demo tenants,
in the compose `postgres:16-alpine`; `seed-demo bench` = 3 warm-ups + 30 sequential timed requests
per target, client wall-clock on loopback, reference machine Apple M1 Max. A > 25 % p95 regression
on an unchanged endpoint blocks review. Rerun: `go run ./cmd/seed-demo bench --api
http://localhost:3000` (add `--no-fail` to capture a table that misses).

| date | api | target | p95 baseline (ms) | p95 after inc 2 (ms) | budget | result |
|---|---|---|---:|---:|---:|---|
| 2026-09-08/09 | 4f2aded → cb4df2e | task feed page 1 (dueDate asc, 50/page) | 33.5 | 42.9 | 500 | PASS |
| | | task feed **page 200** (dueDate asc, offset 9950) | 448.8 (max 508.9) | **67.5** | 500 | PASS |
| | | task feed page 1 (createdAt asc) | 105.3 | 109.2 | 500 | PASS |
| | | task feed page 200 (createdAt asc) | 112.2 | 129.3 | 500 | PASS |
| | | `/reports/task-summary` | — (404, inc 1) | 63.6 | 500 | PASS |
| | | `/reports/workflow-stats` (1,925 rows) | 99.3 | 75.7 | 500 | PASS |
| | | compliance heatmap (FY 2026, period view) | 52.5 | 71.4 | 2000 | PASS |
| | | compliance status page 1 (50/page, exact summary) | **4,159.3** | **220.9** | 2000 | FAIL → PASS |
| | | entities (search not yet honoured — unfiltered first page) | 3.0 | 2.3 | 500 | PASS |
| | | a VAT workflow's instances (108, 50/page) | 4.6 | 4.2 | 500 | PASS |
| | | tax-financial by entity | 61.9 | 112.4 | 2000 | PASS |
| 2026-09-09 | 3483ca1 (after inc 4+5: feed filters, search, named workflow rows) | task feed page 1 / page 200 (dueDate) | — | 37.9 / 63.6 | 500 | PASS |
| | | task feed page 1 / page 200 (createdAt) | — | 119.7 / 134.8 | 500 | PASS |
| | | `/reports/task-summary` | — | 44.2 | 500 | PASS |
| | | `/reports/workflow-stats` | — | 43.3 | 500 | PASS |
| | | compliance heatmap (FY 2026) | — | 50.8 | 2000 | PASS |
| | | compliance status page 1 | — | 174.0 | 2000 | PASS |
| | | `/entities?search=Mül` (**real search now** — total 1) | — | 2.9 | 500 | PASS |
| | | a VAT workflow's instances | — | 4.3 | 500 | PASS |
| | | tax-financial by entity | — | 72.1 | 2000 | PASS |

What moved and why: migration `20260908000022` (tenant-prefixed `(tenant_id, due_date, order_index,
id)` + the same columns on open rows). The deep task-feed page went from an index scan on the
tenant-less `(due_date)` that discarded other tenants' rows via the RLS filter to a range scan on the
tenant's own index; the compliance-status statement, which classifies every instance of the tenant
against `filing_deadline`/`due_date` inside a CTE, stopped scanning the partition. Measured index
build at 100,000 rows: **134 ms** for the whole transaction (the lock window). `createdAt` sorts are
unchanged (no `(tenant_id, created_at, id)` index — within budget, so not added, per the plan).
Plan text (EXPLAIN ANALYZE, BUFFERS) for the deep page and the summary: `docs/testing/perf-2026-09-09.md`
in the UI repo. Not measured yet: search (increment 5), the task feed's new filters (increment 4), a
cell in AWS (the M1 is far faster than db.t4g.small — local PASS is necessary, not sufficient).

## What is missing / remaining

### 🟠 Auth & authorization
- **Search and named workflow rows (2026-09-09, ADR-0026 increment 5, API side).** `search` (≤ 200
  chars, trimmed, blank = no filter) on `/entities` (name, legal name), `/workflows` (name),
  `/obligation-types` (name, code) and `/members` (name, email): case-insensitive `ILIKE` with `%`,
  `_` and `\` escaped (`shared/repositories/like.go`), applied in BOTH the page and the count
  statement through one bind so totals stay exact; the generic repository declares
  `SQLConfig.SearchColumns`. The workflows list select is LEFT-JOINed to entities and obligation
  types so every row carries `entityName` / `obligationTypeName` (nullable), with `w.`-qualified
  filters, `w.created_at` order and `w.id` tie-breaker; the count stays over `workflows` alone.
  `/workflows` year filters are lenient like every other year filter: `financialYear=` and
  `financialYear=all` mean "no filter". Acceptance per module incl. the scale fixture's hostile
  names (`Müller & Söhne 100% GmbH`, `Under_score Holdings Ltd`, `O'Brien \ Partners`).
- **Task feed on the server (2026-09-09, ADR-0026 increment 4, API side).** `/reports/task-instances`
  and `/reports/task-summary` share one filter set (`dto.TaskFilterQuery`): `workflowId`, `entityId`,
  `assigneeId` (uuid | `me` = the session principal | `unassigned`), `obligationTypeId`, `taxType`
  (semi-join on `obligation_types.template`), `financialYear` (repeatable, `none` = no year; `""`/`all`
  ignored), `periodCode`, `status` (stored values + `open`), `workflowCategory`,
  `due=overdue|today|thisWeek` (the `sql_shared.go` tenant-day predicates, so a tile drill-down is
  exact), `dueFrom`/`dueTo` (inclusive; blank or `all` is a 400 like the uuid params), `search`
  (task name, workflow name, period code, plus semi-joins on entity / obligation-type names — the
  COUNT never needs the display LEFT JOINs). Feed `sort` is ONE key from dueDate | createdAt | status
  (statusRank) | workflow | entity (NULLS LAST) | name × asc/desc, every clause ending in
  `ti.order_index, ti.id` in the primary direction; a second sort key is a 400. Predicates are
  assembled only for set filters. `/reports/workflow-stats` gains `workflowId`, `entityId`,
  `financialYear`, `status`, `workflowCategory`; `/audit-log` `action` is repeatable; `/documents`
  `year` is repeatable with `none` (= documents of workflows without a financial year) and its
  `search` treats `all` as a term. OpenAPI: alternation rules (`uuid|oneof=me unassigned`) keep the
  uuid format and document the extra literals; `datetime=2006-01-02` renders as `format: date`.
  Acceptance: every filter, `due=*` on Pacific/Kiritimati and Pago Pago tenants with a required day
  flip against UTC, six sort keys × two directions walked twice and asc = reverse of desc, workflow
  stats filters, repeatable action and year. The demo oracle (403 checks) and the scale oracle
  (3,105 checks) still report 0 differences: the changes are additive.
- **List contracts (2026-09-08, ADR-0026 increment 2).** Every list `ORDER BY` now ends in a unique
  column, in the primary sort's direction: the generic repository (`shared/repositories`) appends an
  `id` tie-breaker (`due_date ASC, id ASC`; `created_at DESC, id DESC`); the audit log orders by `seq`
  alone (assigned under the same per-tenant lock as `occurred_at`, so identical order, and served by
  `idx_audit_log_tenant_seq`). Migration **`20260908000022_pagination_indexes`** adds
  `task_instances (tenant_id, due_date, order_index, id)` and the same columns `WHERE status <>
  'completed'`, and drops the tenant-less `(due_date)` and `(status)` — on a hash partition shared by
  many tenants an index without `tenant_id` reads other tenants' rows before RLS discards them.
  Measured on a 100,000-row scratch DB: the transaction took 134 ms; the deep-page feed plan went from
  an index scan filtering `tenant_id` to a range scan on the tenant's own index; the five-soonest-open
  query became a four-buffer partial-index scan with no sort. The paginated envelope gains `hasMore`;
  repeatable query filters bind to `[]string` (`col = ANY($n)`) with a `none` sentinel declared per
  nullable column (workflows: `status`, `financialYear`); a blank element is a 400. **Standing rule:**
  a new list endpoint declares its tie-breaker (default `id`) and gets a tenant-prefixed index in the
  same migration that introduces its order; index migrations stay plain and transactional until a
  cell's `task_instances` passes ~10⁶ rows or a build exceeds 30 s, then use `ON ONLY` +
  per-partition `CONCURRENTLY` + `ATTACH` in a non-transactional file. Acceptance: a dense-tie fixture
  (144 instances, 12 per due date) walked under every sort and page size — no duplicates, no gaps,
  byte-identical repeat walks — plus an identical-name entities walk and multi-value filter totals.
- **Authentication + sessions — DONE (Increment A, ADR-0011).** First-party email/password
  (argon2id) + server-side sessions (httpOnly + SameSite=Strict cookie, token stored hashed,
  rotation on login/MFA, idle + absolute TTLs, failed-attempt lockout) + **TOTP MFA**
  (enroll/enable/verify). The tenant now comes from the authenticated **session** (`RequireSession`);
  the interim `X-Tenant-ID` header is **gone**. First user via **`cmd/seed-admin`**. `modules/identity`
  + `platform/crypto`; auth endpoints under `/auth/*`.
  - **Failed-login lockout — the attempt budget (rewritten 2026-09-06, round 4).** The policy is
    stated once in `modules/identity/usecases/login.go`; this section is the operator's copy and the
    **only** place timing figures are recorded (they are re-measured whenever the path changes, so a
    number in a code comment is a number that will go stale).
    - **What it bounds: 5 verifications of the stored hash per account *between successes*, per 15
      minutes, independent of concurrency.** An attempt is **charged before the password is
      verified**, so a thousand simultaneous requests buy the same five verifications a thousand
      serial ones do. The counter is per account, not per IP. *"Between successes" is the exact
      bound:* a success resets the counter, so somebody who knows the password refills the budget
      every time they use it, and a burst of correct passwords mints more than five sessions. The
      budget exists to bound **guessing**, and does; it is not an absolute cap on verifications per
      window.
    - **A live lock is never extended.** While the window is running the charge changes nothing —
      counter, `locked_until` and `updated_at` alike — so the requests being refused cannot lengthen
      the refusal.
    - **An elapsed window clears the debt.** The next attempt after it starts a **fresh count at 1** —
      re-locking costs a full 5 attempts again, not 1. A successful login clears the counter *and* any
      stale `locked_until`.
    - **Every attempt costs budget, successes included.** Nothing is known about the password at the
      moment of the charge — that is precisely why the bound holds — so a successful login also spends
      one, and then clears the counter. **The visible cost:** more than 5 *simultaneous* logins by the
      same account get the generic refusal for the surplus even with the right password. They succeed
      on retry (the first success resets the budget). Serial logins never accumulate.
    - **What it does NOT bound.**
      - **DoS on a known address.** An attacker who knows an address holds it out indefinitely by
        paying a fresh threshold of attempts per window — 3 windows for 15 requests. *That figure is
        derived from the policy (5 × 3), not measured;* a previous edit of this file recorded it as
        "measured", which it never was. What the two rules above buy is that the DoS costs 5 requests
        per 15 minutes instead of 1, and that guessing cannot extend a window it is already inside.
      - **Spraying.** One guess each against ten thousand addresses never touches a budget. Nothing
        here sees it.
      - Both need **per-principal / per-IP rate limiting** — see *Not covered* below, which points
        back here.
    - **The lock is never announced, to anybody.** Every failure — unknown address, disabled member,
      invited member with no password yet, service account, wrong password, a charge that could not be
      made, **and the CORRECT password once the budget is spent** — answers the identical generic
      `invalid email or password`, with the same status and the same headers. Past the budget the
      stored hash is **not verified at all**; the dummy equalizer is.
      - *Why the correct password gets nothing either:* answering "account locked" as soon as the
        submitted password matched made the lockout a **password oracle**. An attacker who had just
        spent 5 requests locking an address could then guess against it and read the right password
        straight off the responses, at full speed — the lock refused no verification, so it throttled
        nothing and scored everything. Confirmed by measurement against the real router.
      - **Known cost, accepted deliberately:** a legitimate user who is locked out is told only
        `invalid email or password` and has to wait out the 15 minutes with no explanation. That is
        the price of not leaking, and it is a real support burden.
      - **Future work that removes the cost** (not implemented, deliberately out of scope here): a
        **lockout notification e-mail** to the account's own address, and/or **self-service unlock**
        via a mailed link. Both tell the owner out of band, where an attacker who does not control
        the mailbox learns nothing.
    - **Identical work on every path, structurally.** Every path performs **exactly one argon2
      verification** (~70ms), against the stored hash only when the account is login-capable **and**
      the attempt was inside its budget, and against a fixed dummy otherwise. Two things make that
      structural rather than incidental:
      - a **stored hash that cannot be decoded** used to return in ~2.4ms, because
        `crypto.VerifyPassword` decodes before it hashes — a corrupt or legacy-format row was
        identifiable by timing alone even though it answered the same 401. The decode failure now
        **falls back to verifying the dummy**, so it pays the same cost; it is logged loudly and still
        answers the generic 401;
      - the charge runs for **every login-capable address, locked or not** — where before it
        separated locked from unlocked as well. Login-capable means registered **and** active **and**
        human **and** holding a password, so a disabled member, an invited member with no password
        yet, a service account and an unknown address all pay none: the residual (a few ms against
        the ~70ms floor) separates **a live, password-holding account** from everything else, not
        "registered" from "unknown".
    - **A lockout leaves a server-side trace.** The response is deliberately indistinguishable, so a
      refused-because-locked attempt is logged at **Warn** with the user id and the request id
      (`identity: login refused — the account's attempt budget is spent…`). Without it neither an
      operator chasing "I cannot sign in" nor an auditor asking whether the control ever fires has
      anything to look at. It goes to the log only, never to the caller.
    - **The success path runs on an uncancellable context.** The attempt that reaches the threshold
      stamps the lock *before* anything is known about the password, so a client that hangs up
      between the charge and the reset would otherwise leave the account locked for the full window
      despite having presented the correct password.
    - **Measured 2026-09-06 (round 4, after the rewrite; 3-sample medians, real router, loaded dev
      machine — treat ±2ms as noise):** locked-wrong **73.5ms**, locked-**RIGHT** **73.6ms**,
      unlocked-registered **70.3ms**, locked-mixed-case **70.7ms**, disabled **68.4ms**, invited
      **68.5ms**, service **68.2ms**, unknown **69.6ms**. Spread 68.2–73.6ms on a ~70ms floor. The
      registered/unknown residual is the one charge `UPDATE`. (Superseded: round 3's 69.2–70.9ms, and
      before all of it, a locked address answering in **2.4ms** against an unknown one's 69ms — a
      ~28x oracle plus a distinguishing message, mintable against any address for 5 requests.)
    - **`MsgAccountLocked` is retired.** It is unreachable from `/auth/login` by construction and the
      constant is deleted; `modules/identity/domain/error_messages.go` carries a note saying why, so
      it does not come back.

    **How it is implemented, and what round 4 changed.** The charge is one statement —
    `UPDATE users … FROM (SELECT … FOR UPDATE) … RETURNING NOT base.live_lock` — that updates the row
    **and returns the decision**, evaluated under the row lock. Concurrent attempts serialise on it
    for about a millisecond; an expired lock is erased before the increment (that is the fresh
    window); a live lock is passed through untouched; the threshold-th attempt is still *allowed* and
    is the one that arms the lock.
    Rounds 1–3 instead read the row at the top of the request, decided `locked` from that snapshot,
    and counted the attempt afterwards (as an after-transaction effect). **Every request that started
    before the threshold increment committed therefore read an unlocked account and verified the
    STORED hash**, so the lockout throttled only *serial* guessing: a single concurrent burst bought
    one real verification per thread. Measured against the real router at PoolMax=5, a burst of 40
    wrong passwords with 5 correct guesses released into it logged **1, 3 and 5 of the 5 guesses
    straight in** across three runs (and an all-correct burst of 40 minted 40 sessions against a
    budget of 5); after the rewrite, **0 sessions**, counter exactly 5, account locked, over 4 runs.
    Charging before verifying also removes two older defects by construction: the attempt cannot be
    rolled back with the 401 it accompanies, and a client that hangs up mid-request cannot suppress
    it (if the cancellation lands early enough to break the charge, the attempt is **refused**, not
    admitted — the charge **fails closed**, since a database hiccup must not turn the budget off; the
    failure is logged and answered as the same generic 401, because surfacing a 500 that only
    registered addresses can trigger is the enumeration oracle from the other side).
    **`/auth/login` no longer declares `Tx`.** The use case owns its transactions and takes them
    **sequentially**: the lookup, the charge (one statement, autocommit — its own short transaction),
    then ~70ms of argon2 **holding no connection at all**, then on success a single transaction for
    the counter reset + session insert. The route needed nothing else from the transaction seam — it
    is public, so there is no tenant/user GUC to bind, and login writes no audit entry. This keeps the
    invariant that broke the API in round 1: **one request holds at most one pooled connection at any
    instant**. Holding the request transaction open across the verification would have held a row lock
    for the length of an argon2 hash; borrowing a second connection deadlocked at PoolMax (a burst of
    5 took 5.1s and recorded 1 of 5).
    **`platform/database.AfterTx` is deleted**, with its tests, its `Transaction`-middleware and
    `WithinTransaction` wiring, and the now-unused `database.Detach` it was built on. The failed-login
    counter was its only caller; charging before the answer removed the need for it, and an
    unexercised seam in the transaction path is worse than no seam.
    Covered by use-case unit tests (`TestLogin_AttemptIsChargedBeforeThePasswordIsVerified` pins the
    order at the seam; `TestLogin_EveryFailure_SameAnswerAndSameWork` walks every refusal shape and
    asserts *which* hashes reached the verification seam, undecodable-hash fallback included;
    `TestLogin_Locked_*` are the password-oracle regressions; `TestLogin_ChargeFails_*` pins fail-
    closed) and live-HTTP acceptance tests (`TestFailedLoginLockout`;
    `TestLoginBudget_ConcurrencyCannotWidenIt` — the round-4 regression, 45 concurrent requests;
    `TestFailedLoginLockout_Concurrent` — 4/5/7 concurrent wrong passwords at PoolMax=5 take
    **91/107/143ms** and record 4/5/5, re-measured 2026-09-06; and
    `TestLoginFailures_Indistinguishable`, which compares status, body bytes *and* headers across
    eight refusal shapes including the right password on a locked account).
    **Also settled 2026-09-06:** a **padded e-mail address** reaches the use case and resolves to the
    same account. The mechanism is not what this file previously implied: `validate:"email"` *does*
    reject a padded address, but never sees one, because the route builder `TrimSpace`s every string
    in the body **before** validating (`routing.sanitize`). `Login`'s own `strings.TrimSpace` is
    therefore the use case's guarantee for callers that do not come through HTTP, not a duplicate of a
    validator rule — it stays. **Not** added to the DTO: there is no trim struct tag, it would take
    custom unmarshalling to add one, and it would be a third copy of a rule the sanitizer already
    applies to every body string.
    **Not covered, separate increments:** per-IP / **per-principal rate limiting** — the missing piece
    for *both* gaps named under "What it does NOT bound" above (holding a known address out, and
    spraying across accounts), and the only thing that makes "an attacker cannot hold a known address
    out" true; `POST /auth/mfa/verify` is **unthrottled** (no counter, no budget — an attacker
    holding a password can brute-force the 6-digit TOTP code); and the **argon2 verification itself
    is unmetered** — every login pays 64 MiB and ~70ms *by design*, refusals for addresses that do
    not exist included, and nothing caps how many run at once. Measured against the real router: 200
    concurrent logins for a nonexistent address peaked at ~5 GiB of heap with every request taking
    4.6–5.8s, and 400 took 10.7s. That is a memory-exhaustion lever available to any unauthenticated
    source, and no budget can bound it because it is spent before any account is identified. It needs
    a semaphore (a small multiple of `GOMAXPROCS`) around the verification with a bounded queue and a
    **uniform** shed answer — uniform because a shed response that varies by address is the
    enumeration oracle again.
  - **Cross-origin (CSRF) rule (widened 2026-09-06).** Every **state-changing** request
    (POST/PUT/PATCH/DELETE) on the **external** router is origin-checked by `CrossOriginGuard`,
    wired once in the **route builder** (`buildRoute`) so no future route can forget it: a request
    with **no `Origin`** is allowed (server-to-server clients, curl, and the Express adapter, which
    strips the header), one whose `Origin` host **matches the request host** is allowed, and anything
    else is refused **403 `cross-origin request rejected`** — before any credential is read. The rule
    covers **every external state-changing route, the public `/auth` mutations included**: `login`,
    `logout` and `accept-invite` are checked exactly like a tenant route. A
    **`Authorization: Bearer ztx_…`** request is exempt on purpose: an API token is not ambient
    authority (a browser never attaches it for a foreign page), so CSRF cannot be mounted with one
    and service-account/agent clients keep working from another origin. **GET/HEAD/OPTIONS** are
    untouched. The **internal** router is excluded (not browser-reachable, per-call Basic credentials
    rather than a cookie). Until now the check lived inside `RequireAuth`, which the builder wires
    only for routes declaring a tenant — so the public `/auth` mutations were unguarded, and a page on
    any origin could POST `/auth/logout` with the victim's cookie and genuinely destroy their session,
    or POST `/auth/login` to fixate one. Defence in depth alongside the `SameSite=Strict` session
    cookie; a configurable origin allowlist can replace the host comparison when the browser app is
    served from a different host than the API. Documented for API consumers in
    `shared/apiclient/client.go`. Covered by a middleware decision-table unit test, route-builder
    wiring tests, and a live-HTTP acceptance test (`TestCrossOriginAuthMutationsRejected`).
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
  of the invite link (the token is returned to the admin for now; the UI builds the link from its own
  origin — the API-side link, when it comes, is `app.publicBaseUrl` + `/accept-invite?token=`, see
  `PUBLIC_BASE_URL` above), invite expiry configurability,
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
  **Remaining:** OAuth 2.1 client credentials (MCP-aligned, Phase 2 with WorkOS); **per-principal rate
  limits** — also the missing piece for the two gaps the failed-login attempt budget explicitly does
  not close (an attacker holding a known address locked out at a threshold of requests per window, and
  **spraying** one guess each across many addresses, which never touches a budget at all — see
  "What it does NOT bound" under the failed-login lockout above), and the throttle
  `POST /auth/mfa/verify` still lacks entirely.
- **Tenant timezone (ADR-0003, 2026-09-05) — DONE.** Migration `20260905000020_tenant_timezone` adds
  `tenants.timezone VARCHAR(64) NOT NULL DEFAULT 'UTC'` (`CHECK (timezone <> '')`; the registry stays
  non-partitioned, ADR-0020 exception). Instants stay UTC at rest; the zone is the account's display /
  scheduling reference (ADR-0003 §2) and, since ADR-0023 §6, what "today" means server-side. **Routes**
  (`modules/identity`, `Tenant:true`, no id in the path — always the session's own tenant, pinned from
  `app.GetTenantID(ctx)` because `tenants` is not RLS'd): `GET /tenant` (`member:read`, every role) →
  `{id, slug, name, timezone, createdAt, updatedAt}`; `PUT /tenant` (`member:manage`, tenant_admin only)
  body `{name 1..200, timezone}` — the zone must load with Go's `time.LoadLocation` (400 `unknown IANA
  timezone: <value>`; `Local`, blank and padded names refused; `domain.ValidateTimezone` is the one rule)
  **and** is probed through Postgres `AT TIME ZONE` on the request tx, so a name only one tz database
  knows is a 400, never a broken report later. `GET /auth/me` now carries `tenant: {id, slug, name,
  timezone}` next to every existing key. Audit `tenant.updated {timezone}` (never the name).
  `cmd/seed-admin --timezone <IANA>` (default `UTC`, same validation) writes it on the tenant INSERT and
  prints it in the summary. **Reports (ADR-0023 §6):** the compliance classification's "today" is
  `(NOW() AT TIME ZONE <tenants.timezone of app.tenant_id>)::date` — one Go const `tenantToday`, no
  caller data, replacing `CURRENT_DATE` — so overdue / missed follow the tenant's calendar day, and
  (review hardening, same day) the on-time / late test compares `completed_at` on the **same**
  tenant day (`tenantCompletedDate`): one definition of "day" per tenant. Also from the review:
  only canonical `Area/Location` names (or `UTC`) are accepted — bare abbreviations such as `CET`
  / `EST` are fixed offsets to Postgres but DST zones to browsers and Go, so the two sides' "today"
  would drift — and both binaries embed the IANA database (`time/tzdata`), because the runtime image
  is bare alpine without `/usr/share/zoneinfo` (`Europe/London` was refused in Docker before).
  Acceptance: `acceptance/modules/tenant` (me
  default UTC → viewer GET 200 / every non-admin PUT 403 → admin PUT Europe/London → me + GET reflect
  → Mars/Olympus / Local / blank / unknown-field 400s → other tenant untouched → audit entry, PII-free;
  seeded zone on first request) and `reports.TestComplianceStatusUsesTenantDay` (deadline = today's UTC
  date vs yesterday's under UTC / Pacific/Kiritimati (UTC+14) / Pacific/Pago_Pago (UTC−11), expected
  from the current UTC hour with Go's tz database — a flip versus UTC is asserted at any hour). Unit
  tests: zone validation, self-tenant pinning, audit envelope, nil audit no-op; the SQL const carries no
  `CURRENT_DATE`. **Remaining:** per-user display override (ADR-0003 §4, deferred), digests / reminders
  taking the zone as input (no notification engine yet).
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
- ~~**Non-standard fiscal patterns** (445 / 454 / 544 / 13-period / weekly / custom) —
  currently fail-closed.~~ **Done (2026-09-05, ADR-0023)** — every pattern is computed by
  `shared/deadline` with hand-verified vectors (see Platform / foundation); the frontend's period
  math is superseded by `GET /entities/{id}/periods`.
- ~~**Payment / additional deadlines** beyond the filing deadline are not yet computed during
  generation.~~ **Payment deadline done (2026-09-05)** — `task_instances.payment_deadline` from
  the entity obligation's `paymentOffset` / `paymentFixedDates` / "same as filing".
  **Remaining:** `additionalDeadlines` (advance payments etc.) are recorded on the rule but not
  yet materialized; public holidays (jurisdiction-keyed, ADR-0017 data) on top of the weekend
  adjustment.

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
  `../zentax-ui/docs/assessments/agentic-ai-mcp-readiness.md` for the design (thin stateless
  translator, tools from the route registry, approval never exposed as a tool).
- **Strip `server/` from `zentax-ui`** — deferred until the Go API + client replace the Express dev server.

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
