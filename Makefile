.PHONY: run run-external run-internal dev dev-external dev-internal build lint lint-fix lint-install lint-uninstall fmt gci simplify vet tidy mock test test-verbose test-cover test-race test-short acceptance acceptance-verbose hooks commit-urgent push push-skip-changelog docker-build docker-run docker-report compose-up compose-down compose-build compose-logs compose-ps sqitch-deploy sqitch-revert sqitch-verify sqitch-status sqitch-log

# ── Go toolchain ─────────────────────────────────────────────

GOPATH := $(shell go env GOPATH)
GOBIN  := $(GOPATH)/bin

GO_SRC_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')

GCI_VERSION      := v0.13.1
GCI_EXE          := $(GOBIN)/gci

GOLANGCI_VERSION := v2.11.3
GOLANGCI_EXE     := $(GOBIN)/golangci-lint

MODULE_NAME      := git.dtone.com/internal/go-api-boilerplate

# ── Application ──────────────────────────────────────────────

run: run-external

run-external:
	go run ./cmd/server

run-internal:
	go run ./cmd/server internal

dev: dev-external

dev-external:
	air -c .air.external.toml

dev-internal:
	air -c .air.internal.toml

build:
	go build -o bin/server ./cmd/server

# ── Code Quality ─────────────────────────────────────────────

vet:
	go vet ./...

fmt:
	gofmt -l -w $(GO_SRC_FILES)

gci:
	$(GCI_EXE) write --skip-generated -s standard -s default -s 'prefix($(MODULE_NAME))' .

simplify:
	gofmt -s -l -w $(GO_SRC_FILES)

tidy:
	go mod tidy

# dynamically update mockery.yml file with the new modules and then runc mockery
mock:
	@{ \
		echo "all: false"; \
		echo "dir: '{{.InterfaceDir}}'"; \
		echo "pkgname: '{{.SrcPackageName}}'"; \
		echo "filename: '{{.SrcPackageName}}_mock.go'"; \
		echo "force-file-write: true"; \
		echo "formatter: goimports"; \
		echo "require-template-schema-exists: true"; \
		echo "template: testify"; \
		echo "template-schema: '{{.Template}}.schema.json'"; \
		echo "packages:"; \
		for dir in modules/*/domain; do \
			mod=$$(basename $$(dirname $$dir)); \
			echo "  $(MODULE_NAME)/modules/$$mod/domain:"; \
			echo "    config:"; \
			echo "      all: true"; \
			echo "      filename: 'domain_mock.go'"; \
			echo "      structname: '{{.InterfaceName}}Mock'"; \
		done; \
	} > .mockery.yml
	mockery

lint-install:
	@CURRENT_GO_MM="$$(go env GOVERSION | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/')"; \
	INSTALLED_GO_MM="$$(if [ -x "$(GOLANGCI_EXE)" ]; then $(GOLANGCI_EXE) version 2>/dev/null | sed -nE 's/.*built with go([0-9]+\.[0-9]+).*/\1/p'; fi)"; \
	if [ ! -x "$(GOLANGCI_EXE)" ] || [ "$$INSTALLED_GO_MM" != "$$CURRENT_GO_MM" ]; then \
		echo "Installing golangci-lint $(GOLANGCI_VERSION)"; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION); \
	fi
	$(GOLANGCI_EXE) version
	test -e $(GCI_EXE) || $(info Installing gci import sorter…) \
		go install github.com/daixiang0/gci@$(GCI_VERSION)
	$(GCI_EXE) --version

lint-uninstall:
	rm -f $(GOLANGCI_EXE) $(GCI_EXE)

lint: lint-install fmt gci
	$(GOLANGCI_EXE) --version
	$(GOLANGCI_EXE) run -v

lint-fix: lint-install fmt gci
	$(GOLANGCI_EXE) --version
	$(GOLANGCI_EXE) run -v --fix

# ── Git Hooks ────────────────────────────────────────────────

hooks:
	@git config core.hooksPath scripts/git-hooks
	@chmod +x scripts/git-hooks/*
	@echo "Git hooks active via core.hooksPath → scripts/git-hooks/"

commit-urgent:
	AI_REVIEW=0 git commit -m "$(m)"

push:
	@bash scripts/push.sh

push-skip-changelog:
	git push

# ── Testing ──────────────────────────────────────────────────

test:
	go test ./... -count=1

test-verbose:
	go test ./... -v -count=1

test-cover:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out
	@echo "\nHTML report: go tool cover -html=coverage.out"

test-race:
	go test ./... -count=1 -race

test-short:
	go test ./... -count=1 -short

acceptance:
	bash scripts/acceptance.sh

acceptance-verbose:
	bash scripts/acceptance.sh -v

# ── Docker ───────────────────────────────────────────────────

DOCKER_IMAGE := go-api-boilerplate
DOCKER_TAG   := latest

DOCKER_SECRET_FLAGS = $(if $(CI_JOB_TOKEN),--secret id=CI_JOB_TOKEN,env=CI_JOB_TOKEN)

docker-build:
	docker build -f docker/Dockerfile \
		$(DOCKER_SECRET_FLAGS) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-run:
	docker run --rm -p 3000:3000 -p 3001:3001 \
		-e DATABASE_URL="$(DATABASE_URL)" \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

# ── Docker Compose ──────────────────────────────────────────

COMPOSE_FILE := docker/docker-compose.yml

compose-up:
	docker compose -f $(COMPOSE_FILE) up -d

compose-down:
	docker compose -f $(COMPOSE_FILE) down

compose-build:
	docker compose -f $(COMPOSE_FILE) up -d --build

compose-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

compose-ps:
	docker compose -f $(COMPOSE_FILE) ps

docker-report:
	@DOCKER_IMAGE=$(DOCKER_IMAGE) DOCKER_TAG=$(DOCKER_TAG) ./scripts/docker-report.sh

# ── Database Migrations (requires: brew install sqitch) ──────

SQITCH_DIR = migrations
SQITCH_TARGET ?= db:pg://admin:secretgol@localhost:5432/golang-api
SQITCH = cd $(SQITCH_DIR) && sqitch

sqitch-deploy:
	$(SQITCH) deploy '$(SQITCH_TARGET)'

sqitch-revert:
	$(SQITCH) revert '$(SQITCH_TARGET)' --to @HEAD^

sqitch-revert-all:
	$(SQITCH) revert '$(SQITCH_TARGET)'

sqitch-verify:
	$(SQITCH) verify '$(SQITCH_TARGET)'

sqitch-status:
	$(SQITCH) status '$(SQITCH_TARGET)'

sqitch-log:
	$(SQITCH) log '$(SQITCH_TARGET)'

sqitch-add:
	@read -p "Change name: " name; \
	read -p "Dependencies (space-separated, or empty): " deps; \
	read -p "Note: " note; \
	ts=$$(date -u +%Y%m%d%H%M%S); \
	$(SQITCH) add $${ts}_$$name $${deps:+--requires $$deps} -n "$$note"
