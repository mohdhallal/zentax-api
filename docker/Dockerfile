FROM golang:1.26.2-alpine AS builder

ARG VERSION
ENV VERSION=${VERSION:-dev}

RUN apk --no-cache add --upgrade git make openssh-client libssl3 ca-certificates

ENV BUILDER_APP_DIR=/app/srv/api

RUN mkdir -p /root/.ssh

COPY docker/ssh/config /root/.ssh/
COPY docker/git/.gitconfig /root/

WORKDIR ${BUILDER_APP_DIR}
COPY go.mod go.sum ./

RUN --mount=type=ssh --mount=type=secret,id=CI_JOB_TOKEN \
  chmod 600 /root/.ssh/config && \
  if [ -f /run/secrets/CI_JOB_TOKEN ]; then \
    TOKEN=$(cat /run/secrets/CI_JOB_TOKEN) && \
    printf "machine git.dtone.com\n  login gitlab-ci-token\n  password %s\n" \
      "$TOKEN" > /root/.netrc && \
    chmod 600 /root/.netrc && \
    rm -f /root/.gitconfig; \
  fi && \
  go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server

# -------------------------------------------------------------------------

FROM alpine:3.20.3

ENV APP_DIR=/app/srv/api
ENV APP_NAME=server
ENV SERVER_MODE=""

RUN apk update \
  && apk add --no-cache ca-certificates \
  && rm -rf /var/cache/apk/* \
  && adduser -D -h /home/app app

WORKDIR ${APP_DIR}

COPY --from=builder ${APP_DIR}/server .
COPY --from=builder ${APP_DIR}/deployment/config_files/ ./deployment/config_files/
RUN chown -R app:app $APP_DIR
USER app

EXPOSE 3000 3001

CMD ["sh", "-c", "${APP_DIR}/${APP_NAME} ${SERVER_MODE}"]
