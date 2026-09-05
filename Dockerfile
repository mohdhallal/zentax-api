# zentax-api — multi-stage build for the self-host / local Docker edition.
# Produces two static binaries: the API server and the seed-admin CLI.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/zentax-api ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed-admin ./cmd/seed-admin

FROM alpine:3
RUN adduser -D -u 10001 zentax
WORKDIR /app
COPY --from=build /out/zentax-api /out/seed-admin /usr/local/bin/
# Config is loaded from deployment/config_files/{APP_ENV}.json relative to the
# working directory (development | staging | production — the whole directory
# is copied). Secrets never live in the files: DATABASE_URL (or DB_HOST /
# DB_PORT / DB_NAME / DB_USER / DB_PASSWORD / DB_SSLMODE), AUTH_ENCRYPTION_KEY,
# CORS_ALLOWED_ORIGINS come from the environment (ADR-0014), as does the
# deployment's public origin, PUBLIC_BASE_URL.
COPY deployment/config_files ./deployment/config_files
# Document blobs for the filesystem storage adapter (ADR-0022). Compose mounts a
# named volume here; the directory must exist and belong to the runtime user so
# the volume inherits that ownership on first use.
RUN mkdir -p /var/lib/zentax/documents && chown -R zentax:zentax /var/lib/zentax
VOLUME ["/var/lib/zentax/documents"]
USER zentax
EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
  CMD wget -qO /dev/null http://127.0.0.1:3000/health || exit 1
ENTRYPOINT ["zentax-api"]
