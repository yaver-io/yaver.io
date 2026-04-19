FROM golang:1.26-alpine AS builder

WORKDIR /src
# Copy the whole agent module — it imports sibling packages.
COPY desktop/agent/go.mod desktop/agent/go.sum ./
RUN go mod download
COPY desktop/agent/. ./
# modernc.org/sqlite is pure-Go so CGO_ENABLED=0 produces a static binary.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/yaver .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl tini
COPY --from=builder /out/yaver /usr/local/bin/yaver

# Agent data lives under /root/.yaver (phone-projects, config, tasks).
# docker-compose mounts a named volume here so cloud redeploys don't wipe
# imported phone projects. We run as root because containerize_host tasks
# need the docker socket — single-tenant managed cloud, so container-level
# isolation is not the security boundary (Caddy + owner-auth is).
VOLUME ["/root/.yaver"]
WORKDIR /root

EXPOSE 18080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -sf http://127.0.0.1:18080/health || exit 1

ENTRYPOINT ["/sbin/tini", "--"]
# docker-compose overrides this with an inline bootstrap script that writes
# /root/.yaver/config.json from the CLOUD_OWNER_TOKEN env var. The CMD below
# is only hit when someone runs the image directly with `docker run`.
#   --no-tls:   Caddy terminates TLS in front of us; agent serves plain HTTP.
#   --no-relay: this IS the public endpoint; we don't dial out to a relay.
#   --no-quic:  cloud tenants reach us over HTTPS only.
#   --port 18080 is the default HTTP port already; set explicitly for clarity.
CMD ["yaver", "serve", "--no-tls", "--no-relay", "--no-quic", "--port", "18080"]
