FROM golang:1.22-alpine AS builder

WORKDIR /src
# Copy the whole agent module — it imports sibling packages.
COPY desktop/agent/go.mod desktop/agent/go.sum ./
RUN go mod download
COPY desktop/agent/. ./
# modernc.org/sqlite is pure-Go so CGO_ENABLED=0 produces a static binary.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/yaver .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates curl tini && \
    adduser -D -h /home/yaver -u 10001 yaver
COPY --from=builder /out/yaver /usr/local/bin/yaver

# Agent data lives under /home/yaver/.yaver (phone-projects, config, tasks).
# Mount this as a persistent volume in docker-compose so cloud redeploys
# don't wipe imported phone projects.
VOLUME ["/home/yaver/.yaver"]
USER yaver
WORKDIR /home/yaver

EXPOSE 18080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -sf http://127.0.0.1:18080/health || exit 1

ENTRYPOINT ["/sbin/tini", "--"]
# --no-tls: Caddy terminates TLS in front of us. Running behind Caddy on the
# same host means the agent only needs to serve plain HTTP on 0.0.0.0:18080.
# --no-relay: this is the public endpoint; we don't dial out to a relay.
# --no-quic: cloud tenants reach us over HTTPS only.
CMD ["yaver", "serve", "--no-tls", "--no-relay", "--no-quic"]
