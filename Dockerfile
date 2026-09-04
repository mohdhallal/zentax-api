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
# working directory; DATABASE_URL always comes from the environment.
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
