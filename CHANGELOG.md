# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

Each section represents a development "week", named after the date
that week started on, in the format "YYYYMMDD".

Add a concise and descriptive sentence (preferably matching the commit message)
under the corresponding sub-section.

## 20260616

### Added

- Add Air hot-reload process runner (`make dev` / `make dev-internal`) with `.air.external.toml` and `.air.internal.toml` configs
- Add `Dockerfile.dev` for development container with hot reload
- Add `platform/api_clients/nexus_internal_api.go` — Nexus internal API client with unit tests
- Add comprehensive unit tests across all layers: config, middlewares, routing, swagger, use cases, handlers, shared types, and utils
- Add `payments` `get_by_id` and `orders` `list` endpoints (handler + use case)

### Changed

- Handlers reorganized into `externals/` and `internals/` subdirectories based on route exposure
- Improved pagination: `ListArgs` carries only pagination fields; filters and sorting extracted separately
- Improved sorting: multiple fields supported via comma-separated `sort` query param
- Updated pagination response meta key from `meta` to `pagination`
- Updated auth internal/external lifecycle to use Nexus accounts API key repo
- Updated swagger generator to pass custom header values in external server mode
- Replaced manual `mocks.go` testify mocks with mockery-generated `domain_mock.go` (`.mockery.yml` + `make mock`)

### Fixed

- Fix `.air.internal.toml` config path

## 20260612

### Added

- Add `example` struct tag support for swagger DTO field documentation
- Add `bootstrap/` package: move app wiring (`bootstrap.go`, `container.go`) out of `app/`
- Add `app/` package: consolidate `context.go` (request context accessors) and `requester.go` from `delivery/httpkit/`

### Changed

- Move config files from `config_files/` to `deployment/config_files/`
- Improve routing validator to support nested struct validation for endpoint schemas

### Removed

- Remove redundant credentials table migrations (`20240101000004_credentials`); replaced by `nexus_accounts_api_keys` migration

## 20260504

### Added

- Add README files for AI Skills
- Add claude-run script

## 20260427

### Added

- Scaffolding new golang api boilerplate
- Add AI agentic code review skill part of pre-commit
- Add AI agentic modules creator skill
- Add automatic changelog update skill as part of git push pipeline
- Add update-docs skill to maintain both README.md and CLAUDE.md files
- Add new skill add-endpoint for scaffolding endpoints in existing modules
- Add new skill update-deps for checking and upgrading Go and dependency versions
- Add new skill sync-boilerplate for syncing core framework changes from upstream into downstream projects
- Add new endpoint get_by_id to payments module

### Changed

- Convert review-code and add-module commands into skills
- Update git hooks scripts and documentation
