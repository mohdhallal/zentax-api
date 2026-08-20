#!/usr/bin/env bash
set -euo pipefail

DOCKER_IMAGE="${DOCKER_IMAGE:-go-api-boilerplate}"
DOCKER_TAG="${DOCKER_TAG:-latest}"
APP_DIR="/app/srv/api"

echo ""
echo "Docker Report — ${DOCKER_IMAGE}:${DOCKER_TAG}"
echo "────────────────────────────────────────"
echo ""
echo "  1) Build and report"
echo "  2) Report only (use existing image)"
echo ""
printf "Select option [1/2]: "
read -r choice

case "${choice}" in
  1)
    echo ""
    echo "Building ${DOCKER_IMAGE}:${DOCKER_TAG}..."
    echo ""
    SECRET_FLAGS=""
    if [ -n "${CI_JOB_TOKEN:-}" ]; then
      SECRET_FLAGS="--secret id=CI_JOB_TOKEN,env=CI_JOB_TOKEN"
    fi
    docker build -f docker/Dockerfile ${SECRET_FLAGS} -t "${DOCKER_IMAGE}:${DOCKER_TAG}" .
    ;;
  2)
    if ! docker images "${DOCKER_IMAGE}:${DOCKER_TAG}" --format '{{.ID}}' | grep -q .; then
      echo "Error: image ${DOCKER_IMAGE}:${DOCKER_TAG} not found. Build first."
      exit 1
    fi
    ;;
  *)
    echo "Invalid option. Use 1 or 2."
    exit 1
    ;;
esac

echo ""
echo "Docker Build Report — ${DOCKER_IMAGE}:${DOCKER_TAG}"
echo "────────────────────────────────────────"
printf "%-15s %s\n" "Image size" "$(docker images "${DOCKER_IMAGE}:${DOCKER_TAG}" --format '{{.Size}}')"
printf "%-15s %s    (stripped: -ldflags=\"-s -w\")\n" "Binary size" "$(docker run --rm "${DOCKER_IMAGE}:${DOCKER_TAG}" ls -lh ${APP_DIR}/server | awk '{print $5}')"
printf "%-15s %s\n" "Base OS" "Alpine $(docker run --rm "${DOCKER_IMAGE}:${DOCKER_TAG}" cat /etc/alpine-release)"
printf "%-15s %s\n" "Config files" "$(docker run --rm "${DOCKER_IMAGE}:${DOCKER_TAG}" ls ${APP_DIR}/deployment/config_files/ | wc -l | tr -d ' ')    ($(docker run --rm "${DOCKER_IMAGE}:${DOCKER_TAG}" ls ${APP_DIR}/deployment/config_files/ | tr '\n' ',' | sed 's/,$//'))"
printf "%-15s %s\n" "Exposed ports" "3000, 3001"
echo "────────────────────────────────────────"
echo ""
