# The ZenTax demo dataset

`dataset.json` describes a complete, believable ZenTax world — three tenants in
three timezones, their people, their entities and obligations, every workflow
and task instance, and **the exact numbers every report must return for them**.
`cmd/seed-demo` builds that world in a running stack, checks it, and removes it
again.

It exists so that "the reports are correct" is something you can *run*, not
something you have to believe. It is also what you point a demo, a screenshot,
or a new developer's first afternoon at.

| | |
|---|---|
| Spec | `seed/demo/dataset.json` (~340 KB) |
| Loader / validator | `seed/demo/spec/` |
| Tool | `cmd/seed-demo/` — `seed`, `verify`, `reset`, plus `scale` and `bench` for the scale fixture |
| Scale fixture generator | `seed/demo/scale/` (see [The scale fixture](#the-scale-fixture)) |
| Human narrative | `../../../zentax-ui/docs/testing/seed-dataset.md` |

The dataset is the contract. `$schemaNotes` inside `dataset.json` documents every
field and is the authority on shape; read it before changing anything.

---

## What is in it

| Tenant | Slug | Timezone | Entities | Workflows | Task instances |
|---|---|---|---|---|---|
| Acme Group | `acme-demo` | Europe/Berlin | 5 | 13 (12 started) | 243 |
| Globex Ltd | `globex` | America/New_York | 2 | 5 | 53 |
| Initech KK | `initech` | Asia/Tokyo | 1 | 1 | 5 |

Three zones is not decoration. A completion is a civil date in the *tenant's*
zone, and the compliance SQL reads `(completed_at AT TIME ZONE tenant.timezone)::date`
— so an evening filing in New York and a morning one in Tokyo land on different
UTC days, and a seeder that stamps the civil date with a `Z` gets caught.

The world deliberately contains the awkward cases: a scoped preparer who can
only write within one entity subtree, a disabled user, a draft workflow that was
never started, an archived one, project workflows (which carry `periodCode`
`PROJECT`, no payment deadline, and are excluded from the three compliance
reports), documents with versions, and instances in every status including
`pending_approval` and `blocked`.

`asOf` is `2026-09-06`, and `validityWindow` says the classifications hold for a
tenant-today anywhere in `2026-09-03 .. 2026-09-10`. Outside that window the
`expectedAsOf` tables drift, because `missed` vs `not_due` depends on today.

`$schemaNotes.coverageGaps` lists what the dataset deliberately does *not*
exercise (non-standard fiscal calendars, bi-annual periodicity, and so on) —
those live in the engine's unit tests instead, because adding them here would
move every expected number.

---

## Running it

Everything below assumes the compose stack in the sibling UI repo and Go 1.27:

```sh
export GOROOT="$HOME/.gvm/gos/go1.27"; export PATH="$GOROOT/bin:$PATH"
```

### 0. Bring the stack up to the current code

**The api image is built from this repo, so it is stale until you rebuild it.**
Seeding against a stale image fails in confusing ways — an image predating the
project-workflow fix rejects `POST /workflows/{id}/start` with *"only recurring
workflows generate task instances"*.

```sh
cd ../zentax-ui
docker compose up -d --build --no-deps api
docker compose up migrate          # --no-deps skipped it; migrations must follow
```

Confirm both, from anywhere:

```sh
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:3000/health/ready   # 200
docker exec zentax-postgres-1 psql -U postgres -d zentax \
  -c "select name from _migrations order by name desc limit 1;"
# ... 20260906000021_canonical_tax_keys.sql
```

Migration `20260906000021` matters specifically: it makes the predefined data
templates use the canonical tax-data field ids (`salesTotal`, `outputVat`, …)
that the reports read. Without it the seeded figures are all zero.

Docker registry pulls occasionally time out. Retry, or build `api` and `web`
separately — don't assume a timeout means something is broken.

### 1. Two DSNs, and why

Run every command below **from the repository root** (`seed-demo` resolves
`seed/demo/dataset.json` and `deployment/config_files/` relative to the working
directory).

```sh
APP_DSN='postgres://zentax_app:zentax-local-app@localhost:5433/zentax?sslmode=disable'
ADMIN_DSN='postgres://postgres:zentax-local-pg@localhost:5433/zentax?sslmode=disable'
go build -o /tmp/seed-demo ./cmd/seed-demo/
```

The two roles are not interchangeable, in **both** directions:

- **`seed` must use the application role.** The tenant bootstrap seeds the three
  predefined data templates through the same use case `POST
  /data-templates/predefined` runs, and that use case decides what is missing
  with `repo.List(ctx, ListDataTemplatesArgs{})` — a query with **no tenant
  predicate**, scoped by row-level security alone. That is right for the API,
  whose role is `NOBYPASSRLS` (ADR-0004). On a superuser connection the same
  list returns *every* tenant's templates, so a name another tenant already owns
  is judged present, nothing is inserted, and the bootstrap cheerfully reports
  "3 predefined data templates" for a tenant that has none. `seed-demo` now
  refuses such a connection up front rather than letting it half-build a tenant.

- **`reset` needs more than the application role.** Deleting a tenant clears
  `audit_log`, which is append-only for `zentax_app` (the migrate job `REVOKE`s
  `UPDATE`/`DELETE` and the RLS policies allow only `SELECT`/`INSERT`). Give it
  the privileged DSN with `--admin-dsn`; without it you get
  `permission denied for table audit_log`, and the delete rolls back cleanly.

### 2. Reset

```sh
/tmp/seed-demo reset --yes --admin-dsn "$ADMIN_DSN" --database-url "$APP_DSN"
```

It prints what it will delete before doing anything, and two independent locks
stand in front of it:

1. `--yes`. Without it the command lists the tenants and stops.
2. A **local-database guard**: the DSN's host must be loopback, or `APP_ENV` must
   be `development` (how the compose stack runs, where the host is the
   `postgres` service name). A staging or production endpoint is refused
   outright — no flag overrides it, the check runs *before* any connection is
   opened, and a DSN whose host cannot be read counts as not-local.

Only slugs that appear in the dataset are deleted, so a reset cannot take a
neighbouring tenant with it. Note the consequence: **the compose stack's own
`acme` / `admin@acme.test` tenant (from `docker compose run --rm seed`) is
deliberately left alone** — that is why the dataset's Acme slug is `acme-demo`.
Delete it by hand if you want a database holding nothing but the dataset.

### 3. Seed

```sh
/tmp/seed-demo seed --api http://localhost:3000 --database-url "$APP_DSN"
```

or, the usual loop, reset and seed in one command:

```sh
/tmp/seed-demo seed --reset --yes --api http://localhost:3000 \
  --database-url "$APP_DSN" --admin-dsn "$ADMIN_DSN"
```

Useful flags: `--dry-run` (validate and print the plan, writing nothing),
`--only acme,initech` (tenant keys *or* slugs), `--out` (where the key → id map
goes, default `./seed-demo-output.json`).

`--api` takes the **Go API's** base URL. There is no `/api` prefix on it; that
belongs to the UI's Express proxy.

A preflight refuses to start if any dataset slug **or any dataset e-mail** is
already registered (`users.email` is globally unique across tenants), listing
every collision — seeding on top of a partial world would build something
nothing describes.

Expect, on success:

```
acme (acme-demo)   7 users, 5 entities, 5 obligation types, 11 entity obligations
                   13 workflows (12 started), 58 task templates, 243 task instances,
                   6 documents (7 versions)
globex (globex)    4 users, 2 entities, 2 obligation types, 4 entity obligations
                   5 workflows (5 started), 23 task templates, 53 task instances
initech (initech)  2 users, 1 entity, 1 obligation type, 1 entity obligation
                   1 workflow (1 started), 5 task templates, 5 task instances
```

### 4. Verify

```sh
/tmp/seed-demo verify --api http://localhost:3000 --in ./seed-demo-output.json
```

403 checks across eleven families — task instances (including the *exact*
`completed_at` instant per instance), workflow previews, entity periods, the
compliance heatmap, compliance status, tax-financial, export-raw, workflow
stats, the dashboard, project participation, and key resolution. It recomputes
every expected number **independently** from the dataset plus each tenant's
civil today and diffs it against the live endpoints; it never imports
`modules/reports`.

Exit is non-zero on any difference. A green run reads:

```
403 checks, 402 passed, 0 failed, 1 skipped — 0 differences
```

(The skip is real: initech has no project workflow, so there is no project
participation to check.)

Flags: `--json` (machine-readable), `--strict-static` (also fail when the
dataset's own `expectedAsOf` tables disagree with the recomputation — normally
reported as INFO, since the recomputation is the authority), `--only`,
`--today YYYY-MM-DD`, a what-if that moves the *oracle's* today while the API
answers on its own clock, and `--scale`, which verifies the scale fixture
instead of the dataset (see [Verify](#verify-the-scale-fixture) below).

`verify` resolves the dataset's keys from the **live tenant** by natural key
(entity name, obligation-type code, workflow name), so it works on a database
seeded by an earlier run with no `--in` file at all. When the file is present it
is cross-checked as well.

---

## The one direct-database write

Everything in the seed is an ordinary HTTP call made as one of the seeded users,
in the dataset's own order, with exactly two exceptions no endpoint exposes:

1. **The tenant, its admin and the predefined data templates**, before the API
   calls. There is no endpoint that creates a tenant; `platform/seed` does it,
   exactly as `cmd/seed-admin` does.

2. **The completion instants**, once, at the very end.
   `completed_at` / `submitted_at` / `approved_at` are Postgres `NOW()`
   everywhere in the API, so a demo whose story runs from April 2025 to
   September 2026 cannot be told through the API at all. One transaction per
   tenant, opened with `SELECT set_config('app.tenant_id', …, true)` so RLS
   applies, `UPDATE`s the parent table with `COALESCE` (a column the dataset
   says nothing about is never cleared) and requires every row to report
   `RowsAffected == 1` or the tenant rolls back.

   The rule: `completed_at` = `completedOn` at the local completion time
   (default 10:00) read in the **tenant's** zone and converted to UTC;
   `approved_at` = `completed_at` for approvals; `submitted_at` = `submittedOn`
   at the default time. Where the dataset pins `completedAtUtc`, a disagreement
   is a hard error naming both instants.

### The audit-trail caveat

**`audit_log` is never back-dated.** It is append-only and hash-chained
(ADR-0007 / ADR-0008): rewriting it would either break the chain or teach the
tool to forge it, and both are worse than the alternative. So the audit trail
truthfully shows *the instants of the seed run*, not the dataset's story dates.

A task completed "in April 2025" therefore has an audit entry timestamped when
you seeded. That is correct behaviour, and it is the one place where the demo
world visibly is a demo. Don't screenshot the audit log next to a 2025 filing
date and expect them to agree.

**Re-seed anything seeded before 2026-09-12.** The hash-chain cut-over
(migration `20260912000026_audit_chain_version`) fixed a writer that hashed
`occurred_at` at nanosecond precision while the column stores microseconds:
every entry written before it is marked `hash_version = 1` and can never be
recomputed from the stored row — `platform/audit.Verify` link-checks those rows,
skips recomputation, and reports the first sequence number it could verify. A
demo whose chain must verify end to end (and any screenshot of a verifier)
therefore needs a fresh trail: `reset --yes --admin-dsn …`, then seed again.
There is no migration that repairs the old rows, and there will not be:
rewriting an append-only ledger is the one thing it exists to prevent.

---

## `seed-demo-output.json`

The key → id map, written by `seed` and read by `verify --in`:

```
generatedAt, specPath, apiBaseUrl, asOf, warning, tenants[]
  tenants[]: key, slug, name, timezone, tenantId,
             users (email/role/scope/password), entities, obligationTypes,
             entityObligations, dataTemplates (by tax type),
             workflows (id, category, status, started, templates,
                        instances keyed "period|task"),
             documents, counts
```

Every task-instance id is in there, which makes it the source of deep links into
the UI. Maps marshal sorted, so two runs differ only in ids and the timestamp.

**It contains cleartext demo passwords** (the `warning` field says so). It is
scratch output — don't commit it.

---

## Sign in

Demo credentials. This dataset must never describe a real tenant, and these
passwords must never appear anywhere but a local stack.

| Tenant | Email | Role | Password |
|---|---|---|---|
| `acme-demo` | admin@acme-demo.test | tenant_admin | `Acme-Admin-2026!` |
| `acme-demo` | manager@acme-demo.test | manager | `Acme-Manager-2026!` |
| `acme-demo` | preparer@acme-demo.test | preparer | `Acme-Preparer-2026!` |
| `acme-demo` | preparer-fr@acme-demo.test | preparer, scoped to Acme France | `Acme-PreparerFR-2026!` |
| `acme-demo` | reviewer@acme-demo.test | reviewer | `Acme-Reviewer-2026!` |
| `acme-demo` | viewer@acme-demo.test | viewer | `Acme-Viewer-2026!` |
| `acme-demo` | disabled@acme-demo.test | preparer, **disabled** | `Acme-Disabled-2026!` |
| `globex` | admin@globex.test | tenant_admin | `Globex-Admin-2026!` |
| `globex` | manager@globex.test | manager | `Globex-Manager-2026!` |
| `globex` | preparer@globex.test | preparer | `Globex-Preparer-2026!` |
| `globex` | reviewer@globex.test | reviewer | `Globex-Reviewer-2026!` |
| `initech` | admin@initech.test | tenant_admin | `Initech-Admin-2026!` |
| `initech` | preparer@initech.test | preparer | `Initech-Preparer-2026!` |
| `scale` | admin@scale.test | tenant_admin | `Scale-Admin-2026!` |
| `scale` | `<role>N@scale.test` — manager1–2, reviewer1–2, preparer1–6, viewer1 | as named; preparer5 scoped to *Scale EMEA Holding B.V.*, preparer6 **disabled** | `Scale-Member-2026!` (one shared member password) |

`disabled@acme-demo.test` cannot sign in — that is the point of it. The seeder
creates the account, uses it, and disables it last. The `scale` rows exist only
after `seed-demo scale` (below).

The UI is at <http://localhost:5001> and proxies `/api/*` to the Go API. Signing
in there works with the same credentials:

```sh
curl -s -c /tmp/j -X POST http://localhost:5001/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@acme-demo.test","password":"Acme-Admin-2026!"}'
curl -s -b /tmp/j 'http://localhost:5001/api/reports/compliance-heatmap?year=all&viewMode=period'
```

Send **no** `Origin` header when calling the Go API directly — CSRF rejects a
cross-origin one. `curl` omits it by default.

---

## The scale fixture

The demo dataset proves the reports are *right*. It cannot prove they are
*fast*, or that the lists page: 243 instances fit under every cap. ADR-0021
rule 7 promises p95 ≤ 500 ms list reads at 10⁵ task instances, and rule 2 that
the UI shows "N of M" — both need a tenant with years of history. The scale
fixture is that tenant: **`scale` / Scale Group AG, Europe/Berlin, 97,152 task
instances**, generated in memory by `seed/demo/scale` and bulk-written by
`seed-demo scale`.

```sh
/tmp/seed-demo scale --yes --database-url "$APP_DSN"                                   # ~15 s locally
/tmp/seed-demo reset --scale --yes --admin-dsn "$ADMIN_DSN" --database-url "$APP_DSN"  # remove it again
```

**`reset --scale` is slow and silent while it works: between 45 s and 4 min
on the default fixture.** Deleting the tenant cascades through its 97,152 task
instances (plus 11,165 task templates and 1,925 workflows) in one transaction,
and nothing is printed until the delete returns. It has not hung — wait for it.

`scale` takes the application DSN (the same RLS reasoning as `seed`), only
writes to a local database (the `reset` guard), refuses a slug or an e-mail
that already exists — it tells you to `reset --scale` — and needs `--yes`.
`--dry-run` generates and prints the statistics without touching anything.
Flags: `--seed` (default 1), `--entities` (48), `--years` (8), `--as-of`
(today in the tenant's zone), `--slug` / `--name` / `--timezone`, `--out`
(default `./seed-demo-scale.json`).

That last file is the **generation record**: the exact `Config` the tenant was
derived from, plus ids and counts. Keep it next to the fixture — the tenant is
a pure function of that config, and a recomputation (`verify --scale`, which
reads the record through `--scale-config`) must start from the same values,
`asOf` above all: the statuses are drawn for the generation day, so
regenerating on a later day with the default `--as-of` describes a different
world.

### Size

| | Default | Rule |
|---|---|---|
| Entities | 48 | 1 holding → 3 regional sub-holdings → 44 operating companies across 12 countries; standard calendar; 42 close on 12-31, a cluster of 6 (UK / Japan) on 03-31 |
| Users | 12 | admin, 2 managers, 2 reviewers, 6 preparers (one scoped to the EMEA subtree, one disabled), 1 viewer |
| Obligation types | 5 | VAT (monthly, **9** templates), WHT (monthly, 5), ENV levy on the `Custom` template (monthly, 5), CIT (quarterly, 5), TP (annual, 5) |
| Entity obligations | 240 | one per (entity, type), jurisdiction-dependent filing / payment rules; half the countries move weekend deadlines, half do not |
| Workflows | 1,925 | entities × types × years = **1,920 started** (≈1 % `completed`, ≈1 % `archived`, the rest `active`) + 5 never-started drafts for the next fiscal year |
| Task templates | 11,165 | |
| Task instances | **97,152** | entities × years × 253 per entity-year (VAT 12 × 9 + WHT 12 × 5 + ENV 12 × 5 + CIT 4 × 5 + TP 1 × 5). A VAT workflow is 108 instances — past the 100-row list cap on purpose |

The fiscal years end with the entity's year running at `--as-of`, so for the
12-31 entities the default is FY2019–FY2026 and for the 03-31 cluster
FY2020–FY2027. Three entity names exist to exercise search escaping and are
spelled exactly: `Müller & Söhne 100% GmbH`, `Under_score Holdings Ltd`,
`O'Brien \ Partners`.

### Generated, not declared

Nothing in the fixture is a number somebody typed. Everything — the tree, the
rules, the state of every instance — is a pure function of the `Config`
(seed, entities, years, asOf) through a seeded PRNG: the same seed produces a
byte-identical `spec.Tenant`, and the tests pin that. The reason is size: a
hand-written `expectedAsOf` block for 97,152 instances would be nobody's
contract. **The only reference for what the API must return is a
recomputation** — the same `spec.Tenant` handed to the verify oracle
(`verify --scale`), which recomputes every report for the run day exactly as
it does for the demo dataset.

The state of each instance is drawn by its due-date band relative to `asOf`
(the table in `seed/demo/scale/states.go`): far past mostly completed with one
in five late, the last sixty days a mix of completed / in progress / pending
approval / blocked, today and the next month mostly open, the far future
untouched. Completions are dated in the tenant's zone and never after `asOf`.
The offsets differ by jurisdiction, so something is due on every calendar day
**from the generation day until 2027-02-06** for the default configuration —
which is what keeps *overdue*, *due today*, *this week*, *awaiting approval*
and *on_time / late / missed / not_due* all non-empty on any run day until
then, not only on the generation day. 2027-02-07 is the first day with nothing
due: by then the 42 entities closing on 12-31 have no period left in their
current fiscal year (the last FY2026 deadline is the UK VAT filing 37 days
after 31 December), and the six 03-31 entities alone do not cover every day.
**Regenerate the fixture after 2027-02-06** (`reset --scale`, then `scale`
again, which picks a new `--as-of` and a new current fiscal year).

Dates come from the same engine the API's generator uses (`shared/deadline`
through the entity calendar) with the oracle's rules; the oracle's planner is
unexported in `cmd/seed-demo`, so the generator mirrors it and
`TestScalePlanMatchesTheOracle` diffs the two plans instance by instance. The
writer persists the oracle's own plan, so what `verify --scale` recomputes is,
by construction, what was written.

### Verify the scale fixture

```sh
/tmp/seed-demo verify --scale --api http://localhost:3000                                     # reads ./seed-demo-scale.json
/tmp/seed-demo verify --scale --scale-config ./seed-demo-scale.json --api http://localhost:3000
```

`--scale` reads the generation record (`--scale-config`, default
`./seed-demo-scale.json` — the `--out` of `scale`), regenerates the tenant from
**the record's** config, and runs the same check families `verify` runs for
the demo tenants against the `scale` tenant alone, signed in as
`admin@scale.test`. It does not read `--spec` or `--in`, and it refuses
`--only` (the fixture is one tenant: *"--scale and --only do not combine"*).
`--today` and `--json` work as for the dataset.

What differs from the dataset run, and is said rather than hidden:

- **`static-expectations` is SKIPPED** — a generated fixture declares no
  `expectedAsOf` tables; the recomputation is its only reference. The line
  appears under `SKIPPED`, together with `project-participation` (the fixture
  has no project workflow).
- **`scale-record`** replaces the seeder-id-map cross-check: the record's
  tenant id, admin id and counts must match the live tenant and the
  regenerated spec, so a stale or foreign record is caught before the reports
  are compared.
- The row-level report checks (compliance-status, tax-financial, export-raw)
  **walk every page** at the 5,000-row cap — twenty pages of compliance rows,
  twenty of the tasks export — and the paging proof pages at the cap too
  instead of at 100, so it makes twenty requests rather than a thousand.
- Filters: without a dataset there are no declared filter keys, so the reports
  run unfiltered (`viewMode=period` / `tax-type` for the heatmap, `all` for the
  rest, every `groupBy` for tax-financial, all three export datasets).

The run makes ~3,400 requests (195 pages of 500 task instances, 1,925 workflow
previews, 1,157 entity period lists, 20 + 20 pages of compliance rows, 20 of
the tasks export) — **one to two minutes** against the compose stack on the
reference machine (65 s idle, 105 s with `bench` running alongside) — and
**prints its progress on stderr**
(`scale: workflow-preview 500/1925`, …) so a slow run is distinguishable from
a stuck one. Do not run it across the tenant's midnight (22:00 UTC in summer
for Europe/Berlin): the oracle reads today once at the start, the API on every
request, and a run that straddles the day boundary differs on every due
window. Expect, on success:

```
verify --scale: http://localhost:3000, generation record ./seed-demo-scale.json
  tenant scale ("Scale Group AG", Europe/Berlin), seed 1, 48 entities, 8 years, asOf 2026-09-08 — generated 2026-09-08T19:20:15.137Z

tenant scale    slug=scale      zone=Europe/Berlin      today=2026-09-08  instances: expected 97152, live 97152

SKIPPED (2):
  scale project-participation []: the tenant has no project workflow
  scale static-expectations []: the scale fixture is generated, not declared: it has no static expectedAsOf tables to cross-check

3105 checks, 3103 passed, 0 failed, 2 skipped — 0 differences
```

A difference here is one of the three kinds listed under
[Changing the dataset](#changing-the-dataset) — an oracle (or generator) bug,
a writer bug (`cmd/seed-demo/scale_write.go` did not persist what the plan
says), or a product bug — and the line names the endpoint, the key and both
values.

### How it is written

Unlike `seed`, `scale` does not drive the API — 97,152 `PUT`s would take an
hour. It creates the tenant, its admin and the predefined data templates
through `platform/seed` (as `seed` and `cmd/seed-admin` do), inserts the
members with **one** argon2id hash computed once (every member shares the demo
password — hashing is deliberately slow), and then, in **one transaction bound
to the tenant** (`app.tenant_id` set, the escape-hatch pattern), inserts
entities → grants → obligation types → entity obligations → workflows → task
templates → task instances as `INSERT … SELECT FROM unnest(…)` over parallel
arrays in batches of 5,000 rows. Ids are generated in Go, so no child waits on
`RETURNING`; the parent tables are addressed, never a partition.
`completed_at` / `submitted_at` / `approved_at` are set directly to the
instants the demo seeder's escape hatch would have written, and `created_at`
is back-dated to each workflow's fiscal-year start so `sort=createdAt` orders
eight years of history the way a real tenant's would.

### What it deliberately omits

- **Documents** — none are uploaded; the documents list stays empty.
- **API-driven writes** — no validation, authorization, SoD or approval rule
  ran for these rows. The demo dataset is what proves those.
- **Audit entries** — `audit_log` is append-only and hash-chained
  (ADR-0007/0008), so the fixture's history simply has no trail. The scale
  tenant's audit page is empty; that is correct, not a bug.
- **Non-standard calendars, project workflows, bi-annual periodicity, MFA** —
  the engine's unit tests and the demo dataset cover those.

---

## Bench

`seed-demo bench` is the ADR-0021 rule 7 measurement. It signs in as the
tenant's admin, resolves the ids it needs (the current fiscal year, one VAT
workflow), and hits each target sequentially: `--warmup` requests discarded,
`--n` requests timed on the client's wall clock (request sent → body fully
read, no JSON decoding), then p50 / p95 / max against the target's p95 budget.

```sh
/tmp/seed-demo bench --api http://localhost:3000            # tenant scale, 3 warm-ups + 30 timed requests
/tmp/seed-demo bench --tenant acme-demo --n 10 --json       # a demo tenant, machine-readable
```

Flags: `--api`, `--tenant` (slug, default `scale`), `--n` (30), `--warmup`
(3), `--page-size` (50), `--deep-page` (200 → offset 9,950), `--json` (print
the JSON report instead of the table), `--out F` (also write the JSON report
to a file), `--no-fail` (exit 0 even on a miss), `--email` / `--password`
(sign in as someone else; by default the scale admin or the dataset tenant's
admin).

| Target | Budget (p95) |
|---|---|
| Task feed page 1 and page 200, `sort=dueDate:asc` and `sort=createdAt:asc` (`/reports/task-instances`) | 500 ms |
| `/reports/task-summary` | 500 ms |
| `/reports/workflow-stats` | 500 ms |
| `/entities?search=Mül&limit=20` | 500 ms |
| One VAT workflow's instances (`/task-instances?workflowId=…&limit=50`) | 500 ms |
| `/reports/compliance-heatmap?year=<current FY>&viewMode=period` | 2,000 ms |
| `/reports/compliance-status?limit=50&offset=0` | 2,000 ms |
| `/reports/tax-financial?groupBy=entity` | 2,000 ms |

**Reading the table.** One row per target: `N` timed samples, `P50 MS` /
`P95 MS` / `MAX MS`, the `BUDGET` and the `RESULT`. `PASS` is p95 ≤ budget,
`FAIL` is a miss (p50 and max are context, not gates), `SKIP` means the route
does not exist on this API build (`/reports/task-summary` lands in increment 1
— a 404 there is expected until then), `ERROR` is any other failure. Notes
under the table explain a caveat: in particular, the query validator silently
drops parameters it does not model, so before increment 5 the entity search
measures the *unfiltered* first page and the note says so (detected by
comparing the search total with the unfiltered total). The exit status is
non-zero on any `FAIL` or `ERROR` unless `--no-fail`.

The first run on a fresh fixture is the **baseline** and is expected to miss;
it documents the defect the pagination increments fix. Record it (STATUS.md,
`docs/testing/perf-<date>.md`) — do not tune the fixture to pass. Numbers are
only comparable on the same machine with the same stack; a > 25 % p95
regression on an unchanged endpoint is the review trigger.

---

## Changing the dataset

The dataset and the expected numbers are one artifact: change a date, a status
or a figure and the `expectedAsOf` tables must move with it.

1. **Edit `dataset.json`.** Keep the schema; `$schemaNotes` is the contract.
2. **Recompute, don't hand-write, the expectations.** `verify`'s oracle
   (`cmd/seed-demo/oracle*.go`) recomputes every number from the dataset. Run
   the offline test — it needs no stack and prints the offending figures rather
   than just failing:
   ```sh
   go test ./cmd/seed-demo/ -run TestVerifyStaticTables -v
   ```
   It diffs the whole `expectedAsOf` block against the recomputation. Copy the
   recomputed values into `dataset.json` until it is silent.
3. **Re-seed and verify for real:**
   ```sh
   /tmp/seed-demo seed --reset --yes --api http://localhost:3000 \
     --database-url "$APP_DSN" --admin-dsn "$ADMIN_DSN"
   /tmp/seed-demo verify --api http://localhost:3000 --strict-static
   ```
4. **Mind `asOf` and `validityWindow`.** No due, filing or payment date may fall
   strictly inside the window, or a run on a different day classifies
   differently. When today drifts past `validityWindow.to`, move the window —
   and re-check every `missed` / `not_due`.
5. **Update the narrative** in `zentax-ui/docs/testing/seed-dataset.md`.

If `verify` reports a difference, it is one of three things, and they are worth
telling apart before touching anything:

- **an oracle bug** — the recomputation is wrong; fix `oracle*.go`;
- **a dataset error** — the declared numbers are wrong; fix `dataset.json`;
- **a product bug** — the API is wrong. Capture the endpoint, the filter, and
  expected vs actual. `verify` prints all of that on one line, per difference.

The oracle deliberately derives dates through `shared/deadline` (the engine)
rather than reimplementing them, so an engine change moves both sides at once.
That blind spot is covered separately: `verify` also diffs every planned
instance against `GET /workflows/{id}/preview` and every persisted one against
`/reports/task-instances`, per instance.
