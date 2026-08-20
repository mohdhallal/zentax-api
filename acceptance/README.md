# Acceptance tests

The acceptance suite verifies the API across its real HTTP middleware, use cases,
repositories, PostgreSQL schema, and both external and internal server modes.

The canonical command is:

```bash
make acceptance
```

Use `make acceptance-verbose` for verbose Go test output. Set
`KEEP_ACCEPTANCE_ENV=1` to retain the Compose environment for debugging.

## Requirements

- macOS with Docker Desktop, or Linux with Docker Engine.
- Bash 3.2 or newer.
- Docker Compose v2 with `docker compose up --wait` support.
- The Go version declared by the root and acceptance modules.

The runner avoids GNU-specific command options and works with both macOS BSD
utilities and standard Linux utilities.

## Runtime topology

Docker Compose starts these services in dependency order:

1. PostgreSQL 17, initialized from `acceptance/postgresql/*.sql`, and its
   readiness check.
2. The Go tester container.

The acceptance suite owns its schema. It intentionally contains only the tables,
columns, constraints, and other objects exercised by acceptance scenarios. It is
not generated from production migrations and does not depend on Sqitch or any
other application migration tool. This keeps the suite usable when a derived
service changes migration technology or consumes an externally owned database.

The tester runs the complete scenario suite directly. Each suite boots the real
application router for its server mode with `httptest`, while retaining real
middleware, use cases, repositories, and PostgreSQL behavior.

## Test layout

```text
acceptance/
├── api_types.go
├── config/tester.go
├── db_fixtures.go
├── docker-compose.test.yml
├── http_client.go
├── http_response.go
├── postgresql/
│   └── 001_schema.sql
├── suite.go
└── modules/
    ├── health/
    ├── orders/
    ├── payments/
    └── users/
```

`Suite` owns the database fixture connection, both bootstrapped applications,
their database pools, the HTTP servers, and the shared client. Each scenario
starts fresh applications and truncates business tables during teardown.

Fixtures are only for arranging prerequisite state. Actions should use the same
HTTP interface as production, and assertions should validate observable API or
database outcomes. Use the typed response structures in `api_types.go` rather
than `map[string]any`.

## Authentication helpers

```go
s.Client.External().
    WithExternalAuth(accountID).
    POST(s.T(), "/orders", body)

s.Client.Internal().
    WithInternalAuth(key, secret).
    POST(s.T(), "/payments", body)
```

Protected endpoints should have both authenticated success coverage and an
unauthenticated rejection scenario.

## Dependency access

The runner downloads root and acceptance module dependencies on the host and
mounts the populated Go caches into the tester. Private dependency
authentication is therefore handled once by the host or CI job; no SSH agent or
credentials are forwarded into the acceptance container.

## Failure handling

Each run uses a unique Compose project name to prevent concurrent developer or
CI jobs from sharing resources. The runner always removes containers, networks, and volumes unless
`KEEP_ACCEPTANCE_ENV=1` is set. On failure it writes combined Compose logs to:

```text
acceptance/test-output/compose.log
```

The GitLab acceptance job publishes that directory as a failure artifact.

## Adding a scenario

1. Add the scenario under `acceptance/modules/<domain>`.
2. Reuse `Suite`, `TestClient`, typed response structures, and existing fixtures.
3. Trigger behavior over HTTP.
4. Assert all externally observable business outcomes, including related entity
   state for transactional workflows.
5. Extend `acceptance/postgresql` only when the scenario requires another schema
   object. Do not copy unrelated application schema into acceptance.
6. Run `make acceptance` twice to confirm repeatability and cleanup.
