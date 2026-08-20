#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE_FILE="$ROOT/acceptance/docker-compose.test.yml"
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-go-api-acceptance-$$}"

command -v docker >/dev/null 2>&1 || {
  echo "docker is required to run acceptance tests" >&2
  exit 1
}
docker compose version >/dev/null 2>&1 || {
  echo "the Docker Compose plugin is required to run acceptance tests" >&2
  exit 1
}
docker compose up --help 2>&1 | grep -q -- "--wait" || {
  echo "Docker Compose with 'up --wait' support is required" >&2
  exit 1
}

compose() {
  docker compose -f "$COMPOSE_FILE" "$@"
}

export TEST_FLAGS="${TEST_FLAGS:--count=1 -p 1 -parallel 1 -timeout 120s}"
if [ "${1:-}" = "-v" ]; then
  export TEST_FLAGS="$TEST_FLAGS -v"
fi

# Resolve Go cache paths so docker-compose can mount them.
export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
export GOCACHE="${GOCACHE:-$(go env GOCACHE)}"

# Resolve private dependencies on the host before starting containers.
(cd "$ROOT" && go mod download)
(cd "$ROOT/acceptance" && go mod download)

# shellcheck disable=SC2329 # invoked by the EXIT trap
cleanup() {
  if [ "${KEEP_ACCEPTANCE_ENV:-0}" != "1" ]; then
    compose down --volumes --remove-orphans
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_log="$(mktemp)"
set +e
{
  compose up -d --wait postgres &&
    compose run --rm --no-deps tester
} 2>&1 | tee "$run_log"
status=${PIPESTATUS[0]}
set -e

if [ "$status" -ne 0 ]; then
  output_dir="$ROOT/acceptance/test-output"
  mkdir -p "$output_dir"
  cp "$run_log" "$output_dir/compose.log"
  compose logs --no-color >> "$output_dir/compose.log" 2>&1 || true
  echo "Acceptance logs written to $output_dir/compose.log"
fi
rm -f "$run_log"

exit "$status"
