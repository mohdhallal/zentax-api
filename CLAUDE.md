# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go API boilerplate — production-ready REST API using modular vertical slice architecture, custom `httpkit` framework (built on chi), generic repository pattern, PostgreSQL (pgx + sqlx), dual-mode HTTP server (external + internal), Sqitch migrations, Prometheus metrics, auto-generated OpenAPI/Swagger, and structured logging via `applogger`.

**Module:** `git.dtone.com/internal/go-api-boilerplate`
**Go version:** 1.26.2
**Config:** JSON files in `deployment/config_files/` loaded as `{APP_ENV}.json`, with env var overrides.

## Development Commands

```bash
# Run
make run                  # external server on :3000
make run-internal         # internal server on :3001
make build                # binary → bin/server

# Dev (hot reload via Air)
make dev                  # external server with hot reload on :3000
make dev-internal         # internal server with hot reload on :3001

# Test
make test                 # go test ./... -count=1
make test-verbose         # with -v
make test-cover           # coverage report
make test-race            # race detector

# Code quality
make lint                 # golangci-lint v2 (installs if needed)
make lint-fix             # auto-fix
make fmt                  # gofmt
make gci                  # import ordering
make vet                  # go vet
make mock                 # regenerate mocks via mockery (.mockery.yml)

# Git hooks
make hooks                # set core.hooksPath → scripts/git-hooks/
make commit-urgent m="msg" # commit bypassing Sergio AI review (AI_REVIEW=0)
make push                 # AI changelog update → approve → push
make push-skip-changelog  # push without changelog update

# Docker
make docker-build         # builds image
make docker-run           # runs with DATABASE_URL
make docker-report        # image size/binary info

# Docker Compose (full stack: postgres + migrate + app)
make compose-up           # start all services (detached)
make compose-down         # stop and remove containers
make compose-build        # rebuild and start
make compose-logs         # follow logs
make compose-ps           # list running services

# Migrations (requires sqitch)
make sqitch-deploy        # apply pending
make sqitch-revert        # revert last
make sqitch-revert-all    # revert all
make sqitch-add           # interactive: create new migration
make sqitch-status        # check state
```

## Architecture

### Layer Structure

```
.claude/
  settings.local.json    Project-local Claude Code permissions
cmd/server/              Entry point, graceful shutdown, signal handling
bootstrap/               Composition root (DI wiring) + Container
app/                     Request-scoped primitives: context accessors (requestId, requester) + Requester identity. Imported by all layers.
config/                  JSON config loading, types, env overrides
logger/                  Structured logger (wraps applogger.git)
platform/
  database/              PostgreSQL pool, transaction executor, PG error helpers
  metrics/               Recorder interface, Prometheus impl, mock impl
delivery/httpkit/
  types/                 Route, Exposure, ServerMode, HttpResponse
  response.go            Ok/Created/Accepted/NoContent helpers
  routing/               Router, RegisterRoute, BuildRoute, validator, helpers
  httperr/               AppError, Classify, HandleError, error interfaces
  middlewares/            CORS, RequestId, Recovery, Metrics, RequestLogger, Auth, Tx
  swagger/               Auto-generated OpenAPI 3.0.3 from route registry
  metric/                Prometheus /metrics endpoint mount
errors/                  Domain error types: NotFound, Conflict, Validation, Forbidden, Unauthorized
shared/
  repositories/          Generic BaseRepo[T, ID] with CRUD + pagination
  types/                 Filter, ListArgs, ListResult[T]
  utils/                 Invariant, AssertDefined, ExtractFilters
modules/                 Business capability slices (not necessarily DB entities — e.g. auth, health)
  {module}/
    domain/              Entity, ports (interfaces), error messages, mocks
    dto/                 Request DTOs (validate tags), response mappers
    handlers/
      externals/         Handlers with Exposure: External or Both
      internals/         Handlers with Exposure: Internal or Both
    usecases/            Business logic, one method per file
    repositories/pg/     SQL queries + repo impl embedding BaseRepo
    router.go            RegisterRoutes(router, usecases)
migrations/              Sqitch: deploy/, revert/, verify/ SQL files
scripts/
  claude-run.sh          Stream Claude CLI activity to terminal in real time (used by hooks + push.sh)
  git-hooks/             pre-commit (lint + AI review), pre-push (tests)
  push.sh                AI-assisted push (changelog + docs sync)
docker/                  Dockerfile + docker-compose.yml
```

### Request Flow

```
HTTP Request
  -> CORS -> RequestId -> Recovery -> Metrics -> RequestLogger
  -> chi Router
  -> Auth middleware (if Auth: true)
  -> Tx middleware (if Tx: true)
  -> Route-level middlewares (if DefineMiddlewares())
  -> Body/Query/Params validation (if DefineSchema())
  -> Handler.Execute(w, r, input, requester)
  -> Use Case -> Repository -> PostgreSQL
  -> Response (writeResponse)
```

### Handler Signature

Handlers implement `types.Route`:

```go
type Route interface {
    DefineRoute() RouteDefinition
    Execute(w http.ResponseWriter, r *http.Request, input *ValidatedInput, requester *Requester) (*HttpResponse, error)
}
```

`DefineRoute()` declares method, path, exposure, auth, and tx requirements declaratively.

## Architecture Rules

1. **Domain layer is pure.** No imports from delivery, platform, or infrastructure. Contains entities, interfaces (ports), error message functions, and mocks.
2. **Ports in `domain/ports.go`.** All dependencies flow inward through interfaces.
3. **Use cases contain business logic.** Depend on repository interfaces, never concrete implementations.
4. **Handlers implement `types.Route`.** Define route metadata, schema, and execute logic. One handler per file.
5. **DTOs separate from domain.** Request DTOs use `validate` struct tags. Response DTOs map domain to JSON.
6. **Repositories implement domain interfaces.** Concrete implementations in `repositories/pg/`. Embed `BaseRepo` for generic CRUD, add custom queries.
7. **No circular dependencies.** Direction: handlers -> usecases -> domain <- repositories.
8. **Transactions are declarative.** Set `Tx: true` in `DefineRoute()`, handled by middleware.
9. **Auth is declarative.** Set `Auth: true` in `DefineRoute()`, handled by middleware.
10. **Route exposure** controls which server mode mounts the route: `External`, `Internal`, or `Both`.
11. **Error classification** uses interface checks (`IsNotFound()`, `IsConflict()`, etc.), not type switches.
12. **Swagger auto-generated** from route registry via reflection on DTO struct tags.

## Coding Standards

### Go Conventions

- Idiomatic Go. Standard naming, short variable names in small scopes, PascalCase exports.
- `context.Context` as first parameter in all repository, use case, and handler methods.
- Constructor functions (`NewXxx`) for dependency injection. No global mutable state except `logger.Log`.
- Interface-driven dependencies. Use case constructors accept interfaces, not concrete types.
- Pointer receivers for use cases, services, repositories, handlers, and infrastructure adapters.
- `*T` for optional/nullable fields to distinguish "not provided" from zero value.
- Required fields: plain values, validated during construction or request handling.
- `var _ Interface = (*Impl)(nil)` compile-time interface check in repo files.

### File Organization

- One handler per file, named after the action (`create.go`, `get_by_id.go`).
- One use case method per file, same naming.
- SQL in separate `sql.go` files. Raw SQL in `SQLConfig` struct + custom queries in module-local vars.
- Domain errors are functions returning strings, used with `apperrors.NewNotFound(domain.ErrXxx(...))`.
- Mocks: generated by mockery into `domain/domain_mock.go`. Run `make mock` after changing interfaces. Config in root `.mockery.yml`.

### Struct Tags

- `db:"column_name"` matches PostgreSQL columns.
- `json:"camelCase"` for API responses.
- `validate:"required,email,max=254"` for request validation.
- `default:"20"` for query parameter defaults.
- `filter:"column_name"` for auto-extracting list filters via `utils.ExtractFilters`.

### SQL Patterns

- `COALESCE($N, col)` for partial updates.
- `RETURNING` on INSERT/UPDATE to avoid extra SELECT.
- Parameterized queries only. Never interpolate user input.

### Response Envelope

```json
{"status": true, "data": {...}}
{"status": true, "data": [...], "pagination": {"total": 100, "limit": 20, "offset": 0}}
{"status": false, "error": {"code": "NOT_FOUND", "message": "user not found: abc-123"}}
```

### Testing

- Unit tests co-located with source files (`*_test.go`).
- Use case tests mock repository interfaces via `domain/domain_mock.go` (generated — run `make mock` after interface changes).
- Handler tests mock use case interfaces.
- Table-driven tests preferred.
- Every new feature or behavior change must include tests covering happy path and edge cases.
- Run `make test` and `make lint` locally before pushing.

## Git Hooks

Hooks live in `scripts/git-hooks/` and are activated via `git config core.hooksPath` (no copying to `.git/hooks/`).

- **pre-commit**: gofmt, gci (import sorting), golangci-lint, go vet, and Sergio (AI code reviewer). Streams Claude activity in real time via `claude-run.sh`.
- **pre-push**: full unit test suite (`go test ./...`). Push aborted if any test fails.
- **`make push`**: Runs two AI steps before pushing — (1) changelog update via `/go-boilerplate:update-changelog`, (2) docs sync via `/go-boilerplate:update-docs`. The wrapper verifies that `go-boilerplate@ezra-plugins` and each skill are available; missing plugin components are logged and skipped. Each active step shows diff → prompts for approval (apply/skip/abort). Streams Claude activity in real time via `claude-run.sh`. Use `make push-skip-changelog` or plain `git push` to bypass.
- **Sergio**: AI-powered reviewer using `/go-skills:review-code` from the enabled `go-skills@ezra-plugins` Claude plugin. The hook skips without failing if the CLI, plugin, or skill is unavailable. Blocks commit on violations. Bypass with `AI_REVIEW=0 git commit` or `make commit-urgent m="msg"`.
- Activate hooks: `make hooks` (sets `core.hooksPath` — edits to `scripts/git-hooks/` take effect immediately).

## Code Quality Rules

- No `TODO`, `FIXME`, or placeholder code before merging. Link issue/ticket ID if unfinished work must remain.
- No commented-out code or temporary debug code.
- Apply Boy Scout Rule: leave code cleaner than found (small, safe cleanups only).
- Remove dead code when touching nearby logic.
- Keep functions short and focused.
- Avoid redundant names when the package provides context (`connector/noop.go` not `connector/noop_connector.go`).
- Avoid stutter in exports (`user.Service` not `user.UserService`).
- Use `const` for domain concepts, static config, magic strings/numbers, enum-like values.
- Use environment variables or injected config for values that vary by environment.
- Before creating new helpers, check if existing internal packages solve the problem.
- Do not skip errors, even in tests.
- Error wrapping: use `fmt.Errorf("context: %w", err)` only when adding meaningful context.

## MR / Branch Conventions

- Small, focused MRs (~10 files max). Break large features into logical MRs:
  1. Data layer: repository interface + implementation + integration test.
  2. Business logic: use case using repo interface + unit test with mocks.
  3. API layer: HTTP handler wiring + acceptance test.
- Branch naming: `ticket-id/short-description` (descriptive enough without project management tool context).
- MR description required. Include architecture diagrams for structural changes.
- Tests in same MR as implementation.
- CI must be green before requesting review.
- Self-review before tagging reviewers. Tag reviewers with context on affected code.
- Squash commits before merge to keep git history clean (developer discretion).
- Separate MR for refactors, independent from functional changes.
- Use scratch branches for throwaway experiments, cherry-pick meaningful commits back.

## Claude Code Skills

| Skill | Description |
|-------|-------------|
| `/go-boilerplate:add-module <name> [Entity]` | Scaffold a new module with all layers (domain, DTOs, handlers, use cases, repo, migration, wiring) |
| `/go-boilerplate:add-endpoint <module> [action]` | Add a new endpoint to an existing module (handler, use case, DTOs, ports, mocks, router wiring) |
| `/go-skills:review-code` | Run Sergio AI code review using the `go-skills@ezra-plugins` plugin |
| `/go-boilerplate:update-changelog` | Generate changelog entries from recent commits for CHANGELOG.md |
| `/go-boilerplate:update-docs` | Analyze unpushed changes and update CLAUDE.md + README.md to reflect new features, commands, skills, or architecture changes |
| `/go-boilerplate:update-deps` | Check and upgrade Go version and public dependencies to latest stable versions with breaking change analysis |
| `/go-boilerplate:sync-boilerplate` | Sync core framework changes from upstream go-api-boilerplate into this project (fetch, diff, plan, apply on sync branch) |

Usage: type `/go-boilerplate:add-module invoices` or `/go-boilerplate:add-endpoint users get_by_email` in Claude Code. The `go-boilerplate:*` skills are provided by the `go-boilerplate@ezra-plugins` plugin and are not stored in this repository.

Claude skills use namespaced plugin commands. This repository only keeps project-local Claude permissions and sync state under `.claude/`.

### Boilerplate Sync

Projects scaffolded from this boilerplate can stay up-to-date with core framework changes using `/go-boilerplate:sync-boilerplate`. Configuration files:

- **`${CLAUDE_SKILL_DIR}/assets/manifest.json`** — plugin-bundled definition of core (synced) vs example (excluded) paths.
- **`.claude/sync-boilerplate.json`** — tracks the last synced commit and history. Each downstream project maintains its own copy.

The skill adds `boilerplate` as a git remote, fetches changes since last sync, performs impact analysis per file, and applies approved changes on a dedicated `sync/boilerplate-<hash>` branch.

## Adding a New Module

> A module represents a business capability (auth, health, notifications), not necessarily a database entity. Not every module needs a migration, repository, or DB table — include only the layers the module requires.

1. **Migration:** `make sqitch-add`, write deploy/revert/verify SQL. *(Skip if module has no DB table.)*
2. **Domain:** `modules/{mod}/domain/` — entity with `json` + `db` tags, ports (interfaces), error messages.
3. **Mocks:** add module package entry to `.mockery.yml`, run `make mock` → generates `domain/domain_mock.go`.
4. **DTOs:** `modules/{mod}/dto/` — request structs with `json`, `validate`, `default`, `filter` tags; response mapper.
5. **Handlers:** `modules/{mod}/handlers/externals/` or `handlers/internals/` — one file per endpoint, placed in subdirectory matching route exposure, implementing `types.Route` + `types.RouteSchemaDefinition`.
6. **Use cases:** `modules/{mod}/usecases/` — struct with repo interface + `NewUseCases()` constructor, one method per file.
7. **Repository:** `modules/{mod}/repositories/pg/` — `sql.go` with queries, `repo.go` embedding `BaseRepo` with `var _` interface check.
8. **Router:** `modules/{mod}/router.go` — `RegisterRoutes(router, usecases)` grouping under `/{resource}`.
9. **Wire up:** add to `bootstrap/container.go` and `bootstrap/bootstrap.go`.
10. **Test:** use case + handler unit tests with mocks.

## Important Files

| File                                        | Purpose                                         |
| ------------------------------------------- | ----------------------------------------------- |
| `bootstrap/bootstrap.go`                    | DI wiring, middleware stack, route registration |
| `bootstrap/container.go`                    | Repository + use case construction              |
| `app/context.go`                            | Request context accessors (requestId, requester) |
| `app/requester.go`                          | Requester identity type (user/service)          |
| `config/types.go`                           | All config structs                              |
| `deployment/config_files/development.json`  | Local dev config template                       |
| `.golangci.yml`                             | Linter config (golangci-lint v2)                |
| `Makefile`                                  | All development commands                        |
| `shared/repositories/pg_base_repository.go` | Generic CRUD base                               |
| `delivery/httpkit/routing/build_route.go`   | Route registration + validation pipeline        |
| `delivery/httpkit/httperr/handler.go`       | Error classification + response                 |
| `.mockery.yml`                                          | Mockery config — packages to generate mocks for  |
| `go-boilerplate` plugin `sync-boilerplate/assets/manifest.json` | Core vs excluded paths for boilerplate sync |
| `.claude/sync-boilerplate.json`                               | Per-project sync tracking (last commit, history) |
