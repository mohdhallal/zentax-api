# 🚀 Go API Boilerplate

Production-ready Go API with **Vertical Slice Architecture**, PostgreSQL, auto-generated Swagger, and dual-mode servers (external + internal).

![Go](https://img.shields.io/badge/Go-1.26.2-00ADD8?logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-pgx%2Fv5-4169E1?logo=postgresql&logoColor=white)
![Architecture](<https://img.shields.io/badge/Architecture-Modular%20Monolith%20(Vertical%20Slice)-blueviolet>)

---

## ✨ Features

- 🏗️ **Modular vertical slices** — each module is self-contained (domain → handlers → usecases → repo)
- 🔐 **Dual-mode auth** — external (credentials) + internal (API keys) via HTTP Basic
- 📝 **Auto-generated OpenAPI 3.0** — derived from route definitions + struct tags, zero manual spec files
- 🔄 **Declarative transactions** — set `Tx: true` in route config, middleware handles the rest
- ✅ **Request validation** — struct tags with human-readable errors + defaults
- 🎯 **Error classification** — interface-based (`IsNotFound`, `IsConflict`, etc.) → auto HTTP status
- 📊 **Generic BaseRepo** — CRUD + pagination + filtering out of the box
- 📈 **Prometheus metrics** — HTTP duration histograms, inflight gauge, `/metrics` endpoint
- 🐳 **Docker-ready** — multi-stage Alpine build + `Dockerfile.dev` for hot-reload development
- 🔗 **Cross-module wiring** — circular dependency resolution via deferred setters
- 🪝 **Git hooks** — pre-commit (gofmt, gci, golangci-lint, go vet) + pre-push (full test suite)
- 🤓 **Sergio AI reviewer** — AI-powered pre-commit code review against project rules via Claude Code
- 📋 **AI changelog** — `make push` auto-generates changelog entries from commits, shows diff, prompts before applying
- 📖 **AI docs sync** — `make push` detects doc-impacting changes and updates CLAUDE.md + README.md automatically
- 📦 **AI dependency updates** — `/go-boilerplate:update-deps` checks Go version + all public deps for latest stable releases, reports breaking changes, and upgrades with full validation (fmt → lint → test → build → docker → vulncheck)
- 🔄 **Boilerplate sync** — `/go-boilerplate:sync-boilerplate` pulls core framework changes from upstream boilerplate repo, analyzes impact per file, and applies approved changes on a dedicated sync branch

---

## 📁 Project Structure

```
├── .claude/
│   └── settings.local.json    → Project-local Claude permissions
├── cmd/
│   ├── server/                → Entrypoint, graceful shutdown
│   └── credentials/           → CLI tool for credential management
├── bootstrap/                 → Composition root: DI container + app wiring
│   ├── bootstrap.go           → Wiring, middleware stack, route registration
│   └── container.go           → Repository + use case construction
├── app/                       → Request-scoped primitives (imported by all layers)
│   ├── context.go             → Context accessors (RequestId, Requester)
│   └── requester.go           → Requester identity type (user/service)
├── config/                    → Config loading (JSON + env overrides)
│   ├── config.go              → Config loader (dtconfig)
│   └── types.go               → Config structs
├── deployment/
│   └── config_files/          → Per-environment JSON configs (development / staging / production)
├── infra/                     → AWS CDK: the environment foundations (VPC, RDS, KMS, S3, ECS cluster),
│                                 the GitHub OIDC deploy roles and the Api stack — see infra/README.md;
│                                 the web service + front door live in ../zentax-ui/infra (ADR-0024)
├── delivery/httpkit/
│   ├── response.go            → Response helpers (Ok, Created, NoContent)
│   ├── httperr/               → Error classification + HTTP error responses
│   ├── metric/                → Metrics endpoint mount
│   ├── middlewares/
│   │   ├── auth.go            → RequireAuth (Basic Auth)
│   │   ├── cors.go            → CORS
│   │   ├── metrics.go         → Request metrics collection
│   │   ├── recovery.go        → Panic recovery
│   │   ├── request_id.go      → X-Request-Id propagation
│   │   ├── request_logger.go  → Structured request logging
│   │   └── tx.go              → Transaction middleware (buffered response)
│   ├── routing/               → Router, route builder, validation, helpers
│   ├── swagger/               → Auto-generated OpenAPI 3.0 spec + UI
│   └── types/                 → Route, HttpResponse, Exposure, ServerMode
├── errors/                    → Typed domain errors (NotFound, Conflict, Validation...)
├── logger/                    → Structured logger (wraps applogger)
├── platform/
│   ├── database/              → PostgreSQL pool, transactions, error helpers
│   └── metrics/               → Metrics interface + Prometheus / mock implementations
├── modules/                       → Business capability slices (not necessarily DB entities — e.g. auth, health)
│   ├── auth/                  → External + internal authentication
│   ├── health/                → Health check endpoint
│   ├── users/                 → Full CRUD example (expanded below)
│   │   ├── router.go          → Group + register all handlers
│   │   ├── domain/
│   │   │   ├── user.go        → Entity + input types
│   │   │   ├── ports.go       → Repository + UseCases interfaces
│   │   │   ├── error_messages.go
│   │   │   └── domain_mock.go → Generated testify mocks (mockery)
│   │   ├── dto/
│   │   │   ├── request.go     → Validated request DTOs (struct tags)
│   │   │   └── response.go    → Domain → JSON mapper
│   │   ├── handlers/          → One file per endpoint, grouped by exposure
│   │   │   ├── externals/     → Exposure: External
│   │   │   │   ├── create.go
│   │   │   │   ├── update.go
│   │   │   ├── internals/     → Exposure: Internal
│   │   │   │   └── delete.go
│   │   │   ├── get_by_id.go   → Exposure: Both (can live at root too)
│   │   │   └── list.go
│   │   ├── usecases/          → Business logic, one method per file
│   │   │   ├── usecases.go    → Struct + constructor
│   │   │   ├── create.go
│   │   │   ├── get_by_id.go
│   │   │   ├── list.go
│   │   │   ├── update.go
│   │   │   └── delete.go
│   │   └── repositories/pg/
│   │       ├── repo.go        → Embeds BaseRepo + custom queries
│   │       └── sql.go         → SQL strings + config
│   ├── orders/                → Same structure as users
│   └── payments/              → Same structure as users
├── shared/                    → BaseRepo, types, utilities
├── migrations/                → Sqitch (deploy/revert/verify)
├── scripts/
│   ├── claude-run.sh          → Stream Claude CLI activity to terminal in real time
│   ├── git-hooks/             → Git hooks (pre-commit, pre-push)
│   └── push.sh               → AI-assisted push (changelog + docs sync)
└── docker/                    → Dockerfile
```

---

## 📦 Dependencies

| Package | Version | Purpose |
| ------- | ------- | ------- |
| `git.dtone.com/internal/golang/applogger.git` | v1.5.0 | Structured logging (wraps Zap) |
| `git.dtone.com/internal/golang/config.git` | v1.8.0 | Config loading (JSON + env overrides) |
| `github.com/go-chi/chi/v5` | v5.2.5 | HTTP router |
| `github.com/go-chi/cors` | v1.2.2 | CORS middleware |
| `github.com/go-playground/validator/v10` | v10.30.2 | Struct validation (request DTOs) |
| `github.com/google/uuid` | v1.6.0 | UUID generation |
| `github.com/jackc/pgx/v5` | v5.9.2 | PostgreSQL driver |
| `github.com/jmoiron/sqlx` | v1.4.0 | SQL extensions (named queries, scanning) |
| `github.com/prometheus/client_golang` | v1.23.2 | Prometheus metrics |
| `github.com/stretchr/testify` | v1.11.1 | Test assertions + mocks |
| `golang.org/x/crypto` | v0.50.0 | Bcrypt / SHA-512 for auth |

---

## 🛠️ Getting Started

### Prerequisites

| Tool       | Version                        |
| ---------- | ------------------------------ |
| Go         | 1.26.2+                        |
| PostgreSQL | 16+                            |
| Sqitch     | latest (`brew install sqitch`) |

### Setup

```bash
# 1. Clone & install deps
git clone <repo-url> && cd go-api-boilerplate
go mod download

# 2. Configure
# Edit deployment/config_files/development.json (DATABASE_URL, ports, etc.)
# Env vars override JSON values — e.g. DATABASE_URL=... make run

# 3. Run migrations
make sqitch-deploy

# 4. Install git hooks (pre-commit lint + pre-push tests)
make hooks

# 5. Run
make run
```

### Configuration

Values are loaded from `deployment/config_files/{APP_ENV}.json` and can be **overridden** by environment variables. Env vars take precedence over JSON config.

| Variable                   | Description                                            | Default       |
| -------------------------- | ------------------------------------------------------ | ------------- |
| `APP_ENV`                  | Environment (`development` / `staging` / `production`) | `development` |
| `PORT`                     | External server port                                   | `3000`        |
| `INTERNAL_PORT`            | Internal server port                                   | `3001`        |
| `DATABASE_URL`             | PostgreSQL connection string                           | —             |
| `PUBLIC_BASE_URL`          | Public origin of the product (the web tier's hostname, e.g. `https://eu.app.zentax.software`), base for absolute links the API hands out; absolute http(s) URL, https in staging/production, trailing slash stripped | `app.publicBaseUrl` (dev: `http://localhost:5000`) |
| `PG_POOL_MAX`              | Max pool connections                                   | `10`          |
| `PG_IDLE_TIMEOUT_MS`       | Idle connection timeout                                | `30000`       |
| `PG_CONNECTION_TIMEOUT_MS` | Connection timeout                                     | `2000`        |

---

## 🏃 Running

```bash
make run              # External server on :3000
make run-internal     # Internal server on :3001
make build            # Build binary → bin/server

# Hot reload (requires Air)
make dev              # External server with hot reload on :3000
make dev-internal     # Internal server with hot reload on :3001
```

---

## 🧪 Testing

```bash
make test             # Run all tests
make test-verbose     # Verbose output
make test-cover       # Coverage report
make test-race        # Race condition detection
make test-short       # Skip long-running tests
make acceptance       # Docker-based API acceptance suite
make acceptance-verbose # Acceptance suite with verbose Go output
```

---

## 🔍 Linting

Uses [golangci-lint v2](https://golangci-lint.run/) with 30+ linters configured in `.golangci.yml`. Import ordering enforced via `gci` (standard → third-party → project).

```bash
make lint-install     # Install golangci-lint v2.11.3 + gci v0.13.1
make lint             # Run full lint suite (fmt + gci + golangci-lint)
make lint-fix         # Auto-fix where possible
make fmt              # Format code (gofmt)
make gci              # Sort imports
make vet              # go vet
make simplify         # gofmt -s (simplify)
make tidy             # go mod tidy
```

---

## 🪝 Git Hooks

Git hooks live in `scripts/git-hooks/` and are activated via `core.hooksPath` (no copying to `.git/hooks/` — edits take effect immediately).

| Hook           | Trigger      | What it does                                                                                  |
| -------------- | ------------ | --------------------------------------------------------------------------------------------- |
| **pre-commit** | `git commit` | Runs `gofmt`, `gci` (import sorting), `golangci-lint`, `go vet`, and **Sergio (AI reviewer)** on staged `.go` files. Auto-formats and re-stages before lint. Streams Claude activity in real time. |
| **pre-push**   | `git push`   | Runs the full unit test suite (`go test ./...`). Push is aborted if any test fails.           |
| **make push**  | `make push`  | Runs two AI steps before pushing: (1) changelog update, (2) docs sync (CLAUDE.md + README.md). Each shows diff → prompts for approval → commits. Streams Claude activity in real time. |

```bash
make hooks            # Sets core.hooksPath → scripts/git-hooks/
make push             # AI changelog + docs sync + push (recommended)
make push-skip-changelog  # Push without AI steps
```

### Sergio — AI Code Reviewer (pre-commit)

The pre-commit hook includes **Sergio**, an AI-powered code reviewer using [Claude Code](https://claude.ai/code). Sergio invokes `/go-skills:review-code` from the `go-skills@ezra-plugins` plugin, which checks changes against the architecture rules, coding standards, and conventions defined in `CLAUDE.md`. The commit is blocked if violations are found.

- **Requires:** `claude` CLI installed and authenticated, with `go-skills@ezra-plugins` installed and enabled
- **Skips gracefully** if the Claude CLI, plugin, or `review-code` skill is unavailable
- **Output:** `PASS` (commit proceeds) or `FAIL` with a numbered list of violations
- **Bypass for urgent commits:** `AI_REVIEW=0 git commit -m "urgent fix"`

### Changelog — AI-Assisted Updates (make push)

`make push` wraps `git push` with an AI-generated changelog step using `/go-boilerplate:update-changelog` from `go-boilerplate@ezra-plugins`. Before pushing, it:

1. Analyzes unpushed commits and classifies changes (Added, Changed, Fixed, etc.)
2. Edits `CHANGELOG.md` directly following [Keep a Changelog](https://keepachangelog.com/en/1.0.0/) with weekly sections
3. Shows the proposed diff for review
4. Prompts: **[y]** commit + push, **[n]** revert + push without it, **[a]** abort

### Documentation Sync — AI-Assisted Updates (make push)

After the changelog step, `make push` runs `/go-boilerplate:update-docs` from the same plugin. The wrapper verifies that the plugin is enabled and each skill exists; unavailable plugin components are logged and skipped without blocking the push. It:

1. Analyzes the full diff of unpushed changes
2. Detects which sections of `CLAUDE.md` and `README.md` need updates (new skills, commands, hooks, modules, config, etc.)
3. Edits both files directly, preserving existing structure and style
4. Shows the proposed diff for review
5. Prompts: **[y]** commit + continue, **[n]** revert + continue, **[a]** abort

Both AI steps (changelog + docs) share the same flow:
- **Requires:** `claude` CLI installed and authenticated
- **Skips gracefully** if `claude` CLI is not available
- **Bypass:** `make push-skip-changelog` or plain `git push`

> Hooks only check staged Go files (pre-commit) or run on push (pre-push) — they won't slow down non-Go changes.

---

## 🗄️ Migrations (Sqitch)

```bash
make sqitch-deploy    # Apply pending migrations
make sqitch-revert    # Revert last migration
make sqitch-revert-all # Revert all migrations
make sqitch-verify    # Verify applied migrations
make sqitch-status    # Show current state
make sqitch-log       # Show migration history
make sqitch-add       # Interactive: create new migration
```

---

## 📖 API Documentation

Swagger UI auto-generated from route definitions — no manual spec files:

| Server             | Swagger UI | Spec JSON            |
| ------------------ | ---------- | -------------------- |
| External (`:3000`) | `/swagger` | `/swagger/spec.json` |
| Internal (`:3001`) | `/swagger` | `/swagger/spec.json` |

---

## 🤖 Claude Code Skills

This project includes [Claude Code](https://claude.ai/code) skills for AI-assisted development:

| Skill | Description |
|-------|-------------|
| `/go-boilerplate:add-module <name> [Entity]` | Scaffold a new module — generates domain, DTOs, handlers, use cases, repository, migration, mocks, router, and wires into DI container |
| `/go-boilerplate:add-endpoint <module> [action]` | Add a new endpoint to an existing module — generates handler, use case, DTOs, and updates ports, mocks, and router |
| `/go-skills:review-code` | Run Sergio AI reviewer from the `go-skills@ezra-plugins` plugin |
| `/go-boilerplate:update-changelog` | Generate and apply changelog entries from recent commits |
| `/go-boilerplate:update-docs` | Analyze changes and update CLAUDE.md + README.md to reflect new features, commands, or architecture changes |
| `/go-boilerplate:update-deps` | Check and upgrade Go version and public dependencies to latest stable versions with breaking change analysis |
| `/go-boilerplate:sync-boilerplate [status]` | Sync core framework changes from upstream go-api-boilerplate — fetch, diff, plan, apply on sync branch. Pass `status` to check sync state without applying |

```bash
# Examples
/go-boilerplate:add-module invoices              # module: invoices, entity: Invoice
/go-boilerplate:add-endpoint users get_by_email  # add get_by_email endpoint to users module
```

The boilerplate skills are provided by `go-boilerplate@ezra-plugins` and are no longer stored in this repository. Plugin skills use namespaced commands such as `/go-boilerplate:add-module` and `/go-skills:review-code`.

---

## 🏗️ Adding a New Module

> A module represents a business capability (auth, health, notifications), not necessarily a database entity. Not every module needs a migration, repository, or DB table — include only the layers the module requires.

1. **Migration** — `make sqitch-add` *(skip if module has no DB table)*
2. **Domain** — `modules/{mod}/domain/` (entity, ports, error messages)
3. **Mocks** — add module package entry to `.mockery.yml`, run `make mock` → generates `domain/domain_mock.go`
4. **DTOs** — `modules/{mod}/dto/` (request + response mappers)
5. **Handlers** — `modules/{mod}/handlers/externals/` or `handlers/internals/` (one file per endpoint, implements `types.Route`)
6. **Use Cases** — `modules/{mod}/usecases/` (business logic, one method per file)
7. **Repository** — `modules/{mod}/repositories/pg/` (embed `BaseRepo`, add custom SQL)
8. **Router** — `modules/{mod}/router.go` (group + register handlers)
9. **Wire** — add to `bootstrap/container.go` + `bootstrap/bootstrap.go`

---

## 🎛️ Declarative Handlers

Each handler is a struct implementing interfaces. No manual wiring of middleware chains — behavior is declared via config:

### `Route` (required)

Defines method, path, exposure, and declarative flags:

```go
func (h *CreateUserHandler) DefineRoute() types.RouteDefinition {
    return types.RouteDefinition{
        Method:   http.MethodPost,
        Path:     "/",
        Exposure: types.Exposures.External,  // External | Internal | Both
        Auth:     true,                      // Auto-applies auth middleware
        Tx:       true,                      // Auto-wraps in DB transaction
    }
}
```

### `RouteSchemaDefinition` (optional)

Declares validation schemas via struct tags — body, query params, and path params are auto-validated before `Execute` runs:

```go
func (h *CreateUserHandler) DefineSchema() types.SchemaDefinition {
    return types.SchemaDefinition{
        Body:   dto.CreateUserBody{},    // validate + json tags
        Query:  dto.ListUsersQuery{},    // validate + default tags
        Params: dto.UserIdParams{},      // validate:"required,uuid"
    }
}
```

### `RouteMiddlewareDefinition` (optional)

Per-route middlewares applied after auth/tx:

```go
func (h *MyHandler) DefineMiddlewares() []types.Middleware {
    return []types.Middleware{rateLimiter, cacheControl}
}
```

### `Execute`

Receives pre-validated, typed input — no parsing/validation logic in handlers:

```go
func (h *CreateUserHandler) Execute(w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester) (*types.HttpResponse, error) {
    body, _ := input.Body.(*dto.CreateUserBody)
    user, err := h.usecases.Create(r.Context(), domain.CreateUserInput{
        Email: body.Email,
        Name:  body.Name,
    })
    if err != nil {
        return nil, err  // error classification handles HTTP status
    }
    return httpkit.Created(dto.UserToJSON(user)), nil
}
```

### Request Flow

```
HTTP Request → CORS → RequestId → Recovery → Metrics → RequestLogger
  → Auth (if Auth: true)
  → Transaction (if Tx: true)
  → Route Middlewares (if DefineMiddlewares())
  → Schema Validation (if DefineSchema())
  → Execute()
  → Response
```

---

## 🐳 Docker

### Build & Run

```bash
make docker-build     # Builds image (go-api-boilerplate:latest)
make docker-run       # Run container on :3000 + :3001

# Or manually:
docker build -f docker/Dockerfile -t go-api-boilerplate .
docker run -p 3000:3000 -p 3001:3001 \
  -e DATABASE_URL="postgres://user:pass@host.docker.internal:5432/mydb?sslmode=disable" \
  go-api-boilerplate
```

> **Note:** For private module dependencies (`git.dtone.com`), pass `--build-arg CI_JOB_TOKEN=<token>` to authenticate during build.

### Docker Compose (Full Stack)

Runs the complete stack: PostgreSQL 17, Sqitch migrations (auto-applied), external server (`:3000`), and internal server (`:3001`).

```bash
make compose-up       # Start all services (detached)
make compose-down     # Stop and remove containers
make compose-build    # Rebuild images and start
make compose-logs     # Follow logs from all services
make compose-ps       # List running services
```

Compose file: `docker/docker-compose.yml`. Services:

| Service | Description |
|---------|-------------|
| `postgres` | PostgreSQL 17 Alpine with health checks |
| `migrate` | Sqitch container — auto-deploys migrations on startup |
| `app-external` | External API server on `:3000` |
| `app-internal` | Internal API server on `:3001` |

### Build Report

Run `make docker-report` after building the image to verify artifact sizes:

```bash
make docker-report
```

Example output:

```
Docker Build Report — go-api-boilerplate:latest
────────────────────────────────────────
Image size     59.7 MB
Binary size    15.9 MB    (stripped: -ldflags="-s -w")
Base OS        Alpine 3.21
Config files   1          (development.json)
Exposed ports  3000, 3001
────────────────────────────────────────
```

---

## 🔑 Credential CLI

Generate auth credentials for external (user-facing) or internal (service-to-service) authentication:

```bash
# External credential (bcrypt-hashed secret)
go run ./cmd/credentials external

# Internal API key (SHA-512-hashed secret)
go run ./cmd/credentials internal
```

Output includes the key, plaintext secret, hashed secret, and a ready-to-use SQL `INSERT` statement.

---

## 🔗 Cross-Module Dependency Wiring

When modules depend on each other (e.g., Orders ↔ Payments), circular imports are resolved via **deferred setters** in the DI container:

```go
orderUC := ordersusecases.NewUseCases(orderRepo)
paymentUC := paymentsusecases.NewUseCases(paymentRepo)

orderUC.SetPayments(paymentUC)   // inject after construction
paymentUC.SetOrders(orderUC)
```

Each use case struct accepts the cross-dependency as an optional interface, set after all constructors run. This avoids import cycles while keeping dependencies explicit and testable.

---

## 📐 Architecture Principles

- Domain layer has **zero infrastructure imports**
- Dependencies flow **inward** through interfaces
- `Handlers → Use Cases → Domain ← Repositories`
- Transactions and auth are **declarative** (route config flags)
- Error classification via **interface checks**, not type switches
- One handler per file, one use case method per file
- SQL lives in dedicated `sql.go` files, uses `COALESCE` for partial updates

---

## 📝 License

DT One
