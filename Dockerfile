# syntax=docker/dockerfile:1

# ---------- build stage ----------
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Dependencies first: this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
# Static binary so it can run in a minimal image.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/api ./cmd/api

# ---------- runtime stage ----------
FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates curl tzdata \
    && adduser -D -H -u 10001 app

COPY --from=builder /out/api /usr/local/bin/api

USER app
EXPOSE 8080

# Production-safe defaults: migrations are embedded in the binary but are not
# applied automatically — run `api migrate up` as a release step, or set
# MIGRATE_ON_START=true (docker compose does) to migrate on boot.
ENV APP_ENV=production \
    HTTP_HOST=0.0.0.0 \
    HTTP_PORT=8080 \
    LOG_FORMAT=json \
    MIGRATE_ON_START=false

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${HTTP_PORT}/healthz" || exit 1

ENTRYPOINT ["/usr/local/bin/api"]
