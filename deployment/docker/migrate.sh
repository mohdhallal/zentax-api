#!/bin/sh
# Idempotent migration runner — the ONE script every edition runs: the Docker
# Compose `migrate` job (bind-mounted), the `Dockerfile.migrate` image (ECS
# run-task before each deploy, ADR-0014 RPO/RTO runbook), and a laptop with psql.
#
# Runs as the SUPERUSER (DDL + pgcrypto need it), applies
# migrations/deploy/*.sql in lexical order (= dependency order, same as the
# sqitch.plan), tracks applied files in _migrations, then provisions the
# NON-SUPERUSER, NON-BYPASSRLS app role — the role the API must connect as, or
# Postgres RLS tenant isolation is silently defeated (see STATUS.md runbook).
# The app role's password is (re)set from APP_DB_PASSWORD on EVERY run, so a
# role that already exists with another password (a rotated secret, a restored
# snapshot from another environment) is brought back in line, never left stale.
#
# Grants are prod-shaped: no TRUNCATE anywhere, and audit_log is append-only
# for the app role (SELECT + INSERT; UPDATE/DELETE revoked — defense in depth
# on top of its RLS policies).
#
# Env: PGHOST PGUSER PGPASSWORD PGDATABASE (libpq; PGPORT/PGSSLMODE optional —
# set PGSSLMODE=require against RDS), APP_DB_USER APP_DB_PASSWORD,
# MIGRATIONS_DIR (default /migrations/deploy).
set -eu

: "${PGHOST:?PGHOST required}"
: "${PGUSER:?PGUSER required}"
: "${PGPASSWORD:?PGPASSWORD required}"
: "${PGDATABASE:?PGDATABASE required}"
: "${APP_DB_USER:?APP_DB_USER required}"
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD required}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-/migrations/deploy}"

echo "migrate: target ${PGHOST}:${PGPORT:-5432}/${PGDATABASE} as ${PGUSER}; migrations from ${MIGRATIONS_DIR}"

i=0
until pg_isready -q; do
  i=$((i + 1))
  [ "$i" -gt 60 ] && echo "postgres never became ready" >&2 && exit 1
  echo "waiting for postgres ($i)..."
  sleep 1
done

psql -v ON_ERROR_STOP=1 -q -c \
  "CREATE TABLE IF NOT EXISTS _migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())"

applied_count=0
applied_names=""
skipped_count=0
for f in $(ls "$MIGRATIONS_DIR"/*.sql | sort); do
  name=$(basename "$f")
  # psql only interpolates :'var' on stdin/-f input, never in -c strings.
  already=$(echo "SELECT 1 FROM _migrations WHERE name = :'name'" | psql -tA -v ON_ERROR_STOP=1 -v name="$name")
  if [ "$already" = "1" ]; then
    skipped_count=$((skipped_count + 1))
    continue
  fi
  echo "applying $name"
  psql -v ON_ERROR_STOP=1 -q -f "$f"
  echo "INSERT INTO _migrations (name) VALUES (:'name')" | psql -v ON_ERROR_STOP=1 -q -v name="$name"
  applied_count=$((applied_count + 1))
  applied_names="${applied_names} ${name}"
done
ledger_count=$(psql -tA -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM _migrations")
echo "migrations: ${applied_count} newly applied, ${skipped_count} already applied, ${ledger_count} in ledger"
if [ "$applied_count" -gt 0 ]; then
  echo "migrations applied this run:"
  for n in $applied_names; do echo "  - $n"; done
fi

# Range-partition create-ahead (ADR-0020): top up monthly partitions for the
# time-partitioned streams on EVERY run, so a long-lived deployment never runs
# out of partitions (each table also has a _default backstop). Idempotent.
psql -v ON_ERROR_STOP=1 -q -o /dev/null <<'SQL'
SELECT ensure_month_partitions('audit_log',  DATE '2026-08-01', 3);
SELECT ensure_month_partitions('sessions',   DATE '2026-08-01', 3);
SELECT ensure_month_partitions('api_tokens', DATE '2026-08-01', 3);
SELECT ensure_month_partitions('invite_tokens', DATE '2026-09-01', 3);
SQL
echo "range partitions ensured (now + 3 months)"

# App role + grants — re-run every time so tables from new migrations are
# covered and the password matches the current secret. The role name and the
# password travel as psql variables (:"user" identifier / :'pw' literal), so a
# generated password containing quotes, $ or backslashes cannot break (or
# inject into) the SQL. CREATE/ALTER ROLE are built with format() + \gexec
# because DO blocks cannot interpolate psql variables.
psql -v ON_ERROR_STOP=1 -q -v user="$APP_DB_USER" -v pw="$APP_DB_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE', :'user')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'user') \gexec
SELECT format('ALTER ROLE %I WITH LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE PASSWORD %L', :'user', :'pw') \gexec
GRANT USAGE ON SCHEMA public TO :"user";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO :"user";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO :"user";
-- audit_log is append-only for the app; the migration ledger is not app data.
REVOKE UPDATE, DELETE ON audit_log FROM :"user";
REVOKE ALL ON _migrations FROM :"user";
SQL

role_flags=$(echo "SELECT rolsuper || '/' || rolbypassrls || '/' || rolcanlogin FROM pg_roles WHERE rolname = :'user'" \
  | psql -tA -v ON_ERROR_STOP=1 -v user="$APP_DB_USER")
echo "app role '${APP_DB_USER}' provisioned (superuser/bypassrls/login = ${role_flags}; password set from APP_DB_PASSWORD)"
echo "done"
