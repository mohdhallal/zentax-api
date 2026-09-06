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
| Tool | `cmd/seed-demo/` — `seed`, `verify`, `reset` |
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
reported as INFO, since the recomputation is the authority), `--only`, and
`--today YYYY-MM-DD`, a what-if that moves the *oracle's* today while the API
answers on its own clock.

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

`disabled@acme-demo.test` cannot sign in — that is the point of it. The seeder
creates the account, uses it, and disables it last.

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
