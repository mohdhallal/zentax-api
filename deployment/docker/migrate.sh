#!/bin/sh
# Idempotent migration runner for the Docker Compose stack. Runs inside a
# postgres-image container as the SUPERUSER (DDL + pgcrypto need it), applies
# migrations/deploy/*.sql in lexical order (= dependency order, same as the
# sqitch.plan), tracks applied files in _migrations, then provisions the
# NON-SUPERUSER, NON-BYPASSRLS app role — the role the API must connect as, or
# Postgres RLS tenant isolation is silently defeated (see STATUS.md runbook).
#
# Grants are prod-shaped: no TRUNCATE anywhere, and audit_log is append-only
# for the app role (SELECT + INSERT; UPDATE/DELETE revoked — defense in depth
# on top of its RLS policies).
set -eu

: "${PGHOST:?PGHOST required}"
: "${PGUSER:?PGUSER required}"
: "${PGPASSWORD:?PGPASSWORD required}"
: "${PGDATABASE:?PGDATABASE required}"
: "${APP_DB_USER:?APP_DB_USER required}"
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD required}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-/migrations/deploy}"

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
for f in $(ls "$MIGRATIONS_DIR"/*.sql | sort); do
  name=$(basename "$f")
  already=$(psql -tA -c "SELECT 1 FROM _migrations WHERE name = '$name'")
  if [ "$already" = "1" ]; then
    continue
  fi
  echo "applying $name"
  psql -v ON_ERROR_STOP=1 -q -f "$f"
  psql -v ON_ERROR_STOP=1 -q -c "INSERT INTO _migrations (name) VALUES ('$name')"
  applied_count=$((applied_count + 1))
done
echo "migrations: $applied_count newly applied"

# App role + grants — re-run every time so tables from new migrations are
# covered. The password is (re)set from the environment on every run.
psql -v ON_ERROR_STOP=1 -q <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${APP_DB_USER}') THEN
    CREATE ROLE "${APP_DB_USER}" LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
  END IF;
END
\$\$;
ALTER ROLE "${APP_DB_USER}" WITH LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD '${APP_DB_PASSWORD}';
GRANT USAGE ON SCHEMA public TO "${APP_DB_USER}";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "${APP_DB_USER}";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO "${APP_DB_USER}";
-- audit_log is append-only for the app; the migration ledger is not app data.
REVOKE UPDATE, DELETE ON audit_log FROM "${APP_DB_USER}";
REVOKE ALL ON _migrations FROM "${APP_DB_USER}";
SQL

echo "app role '${APP_DB_USER}' provisioned (non-superuser, non-BYPASSRLS)"
echo "done"
