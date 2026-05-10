#!/usr/bin/env bash
# setup-relay-wildcard.sh — opt-in wildcard subdomain setup for any
# yaver-relay box (greenfield install OR retrofitting one that's
# already running with single-host nginx).
#
# What this gives you: every connected agent gets auto-assigned
# https://<deviceId>.<expose-domain> and publishes that URL into the
# device row's publicEndpoints. Mobile then has a clean direct candidate
# even when the user hasn't configured Cloudflare Tunnel / Tailscale
# / public IP themselves.
#
# What it does, idempotently:
#   1. Ensures `*.<expose-domain>` A record exists in Cloudflare,
#      DNS-only (gray cloud — proxy off so the wildcard doesn't enter
#      a Worker route configured for the apex zone).
#   2. Installs certbot + the Cloudflare DNS-01 plugin and obtains a
#      wildcard TLS cert for `*.<expose-domain>` (HTTP-01 doesn't
#      cover wildcards, so we MUST use DNS-01).
#   3. Writes an nginx server block matching `*.<expose-domain>`
#      that proxies to the relay's HTTP port. Reloads nginx.
#   4. Updates the relay's environment so it starts publishing the
#      assigned URL pattern. Restarts the relay (Docker or systemd).
#
# Forward compatibility: this script touches only the wildcard path.
# Existing single-host setups (e.g. relay.example.com terminating at
# the same nginx) keep working — we add a SECOND server block, not
# replace the first.
#
# Usage:
#   ./setup-relay-wildcard.sh \
#     --expose-domain dev.example.com \
#     --cf-token <cloudflare-api-token> \
#     [--cf-zone example.com] \
#     [--relay-http-port 8443] \
#     [--public-ip <auto-detected>] \
#     [--email admin@example.com] \
#     [--restart-mode docker|systemd|none]
#
# Cloudflare API token scope: Zone:DNS:Edit on the target zone is
# enough. Generate at dash.cloudflare.com → My Profile → API Tokens.

set -euo pipefail

# ── Defaults ────────────────────────────────────────────────────────

EXPOSE_DOMAIN=""
CF_TOKEN="${CF_TOKEN:-}"
CF_ZONE=""
RELAY_HTTP_PORT="8443"
PUBLIC_IP=""
EMAIL=""
RESTART_MODE="auto"  # auto picks docker | systemd | none

# ── Args ────────────────────────────────────────────────────────────

usage() {
  cat <<EOF
Usage: $0 --expose-domain <domain> --cf-token <token> [options]

Required:
  --expose-domain   Wildcard subdomain root, e.g. dev.example.com
  --cf-token        Cloudflare API token (Zone:DNS:Edit on target zone).
                    Falls back to env CF_TOKEN.

Optional:
  --cf-zone         Cloudflare zone name. Defaults to the last two
                    labels of --expose-domain (dev.example.com → example.com).
  --relay-http-port Port the relay binary listens on locally (default: 8443).
  --public-ip       This server's public IPv4 the wildcard A record
                    should point at. Auto-detected via api.ipify.org
                    when omitted.
  --email           Email for Let's Encrypt registration. Defaults to
                    admin@<expose-domain>.
  --restart-mode    docker | systemd | none. Default: auto.

Examples:
  $0 --expose-domain dev.yaver.io --cf-token \$CF_TOKEN
  $0 --expose-domain dev.example.com --cf-token \$CF_TOKEN --cf-zone example.com --restart-mode systemd
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --expose-domain)   EXPOSE_DOMAIN="$2"; shift 2 ;;
    --cf-token)        CF_TOKEN="$2";      shift 2 ;;
    --cf-zone)         CF_ZONE="$2";       shift 2 ;;
    --relay-http-port) RELAY_HTTP_PORT="$2"; shift 2 ;;
    --public-ip)       PUBLIC_IP="$2";     shift 2 ;;
    --email)           EMAIL="$2";         shift 2 ;;
    --restart-mode)    RESTART_MODE="$2";  shift 2 ;;
    --help|-h)         usage ;;
    *)                 echo "Unknown option: $1"; usage ;;
  esac
done

if [ -z "$EXPOSE_DOMAIN" ] || [ -z "$CF_TOKEN" ]; then
  echo "Error: --expose-domain and --cf-token are required."
  echo
  usage
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "Error: must run as root (sudo $0 ...)"
  exit 1
fi

# Derive defaults
if [ -z "$CF_ZONE" ]; then
  # Take last two labels: dev.foo.example.com → example.com.
  # net.ParseURL is overkill for a shell script; awk handles the
  # vast-majority case (3+ labels) and we error out for short ones.
  CF_ZONE=$(echo "$EXPOSE_DOMAIN" | awk -F. '{ if (NF < 2) print $0; else print $(NF-1)"."$NF }')
fi

if [ -z "$EMAIL" ]; then
  EMAIL="admin@$EXPOSE_DOMAIN"
fi

if [ -z "$PUBLIC_IP" ]; then
  PUBLIC_IP=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
            || curl -fsS --max-time 5 https://icanhazip.com 2>/dev/null \
            || true)
fi

if [ -z "$PUBLIC_IP" ]; then
  echo "Error: could not auto-detect public IP. Pass --public-ip <ip>."
  exit 2
fi

# ── Helpers ─────────────────────────────────────────────────────────

log() { echo -e "\033[1;34m[wildcard-setup]\033[0m $*"; }
ok()  { echo -e "\033[1;32m  ✓\033[0m $*"; }
warn(){ echo -e "\033[1;33m  ⚠\033[0m $*"; }
err() { echo -e "\033[1;31m  ✗\033[0m $*"; exit 1; }

cf_api() {
  local method=$1; shift
  local path=$1;   shift
  curl -fsS -X "$method" "https://api.cloudflare.com/client/v4${path}" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" \
    "$@"
}

# ── Show plan ───────────────────────────────────────────────────────

log "Yaver relay wildcard setup"
log "  expose-domain : $EXPOSE_DOMAIN"
log "  cf-zone       : $CF_ZONE"
log "  public-ip     : $PUBLIC_IP"
log "  relay-port    : $RELAY_HTTP_PORT"
log "  email         : $EMAIL"
log "  restart-mode  : $RESTART_MODE"
echo

# ── Step 1: Cloudflare DNS ──────────────────────────────────────────

log "Step 1/4: Ensuring Cloudflare DNS *.${EXPOSE_DOMAIN} → ${PUBLIC_IP} (DNS-only)"

# Find the zone ID
ZONE_RESP=$(cf_api GET "/zones?name=${CF_ZONE}")
ZONE_ID=$(echo "$ZONE_RESP" | grep -oE '"id":"[^"]+' | head -1 | cut -d'"' -f4)
if [ -z "$ZONE_ID" ]; then
  err "Cloudflare zone '$CF_ZONE' not found. Token scoped to this zone? API response: $ZONE_RESP"
fi
ok "Found zone $CF_ZONE (id ${ZONE_ID:0:8}...)"

# Find existing wildcard record
WILDCARD_NAME="*.${EXPOSE_DOMAIN}"
EXISTING=$(cf_api GET "/zones/${ZONE_ID}/dns_records?name=${WILDCARD_NAME}&type=A")
RECORD_ID=$(echo "$EXISTING" | grep -oE '"id":"[^"]+' | head -1 | cut -d'"' -f4 || true)
EXISTING_IP=$(echo "$EXISTING" | grep -oE '"content":"[^"]+' | head -1 | cut -d'"' -f4 || true)
EXISTING_PROXIED=$(echo "$EXISTING" | grep -oE '"proxied":(true|false)' | head -1 | cut -d: -f2 || true)

PAYLOAD=$(printf '{"type":"A","name":"%s","content":"%s","ttl":300,"proxied":false}' \
  "$WILDCARD_NAME" "$PUBLIC_IP")

if [ -n "$RECORD_ID" ]; then
  if [ "$EXISTING_IP" = "$PUBLIC_IP" ] && [ "$EXISTING_PROXIED" = "false" ]; then
    ok "Wildcard A record already correct (id ${RECORD_ID:0:8}...)"
  else
    log "  Updating existing record (was IP=$EXISTING_IP proxied=$EXISTING_PROXIED)"
    cf_api PUT "/zones/${ZONE_ID}/dns_records/${RECORD_ID}" --data "$PAYLOAD" >/dev/null
    ok "Updated wildcard A record"
  fi
else
  log "  Creating wildcard A record"
  cf_api POST "/zones/${ZONE_ID}/dns_records" --data "$PAYLOAD" >/dev/null
  ok "Created wildcard A record"
fi

# ── Step 2: Wildcard TLS cert via certbot DNS-01 ───────────────────

log "Step 2/4: Obtaining wildcard cert for *.${EXPOSE_DOMAIN}"

# Install certbot + cloudflare plugin
if ! command -v certbot &>/dev/null; then
  log "  Installing certbot..."
  if command -v apt-get &>/dev/null; then
    apt-get update -qq
    apt-get install -y -qq certbot python3-certbot-dns-cloudflare
  elif command -v dnf &>/dev/null; then
    dnf install -y certbot python3-certbot-dns-cloudflare
  elif command -v yum &>/dev/null; then
    yum install -y certbot python3-certbot-dns-cloudflare
  else
    err "Unsupported package manager. Install certbot + python3-certbot-dns-cloudflare manually."
  fi
fi

if ! python3 -c "import certbot_dns_cloudflare" 2>/dev/null; then
  warn "Cloudflare DNS plugin missing — trying pip install"
  pip3 install --break-system-packages certbot-dns-cloudflare 2>/dev/null \
    || pip3 install certbot-dns-cloudflare \
    || err "Install python3-certbot-dns-cloudflare manually for your distro."
fi

# Cloudflare credentials file — must be 0600 or certbot warns/errors.
CF_CREDS="/root/.secrets/cloudflare-${CF_ZONE}.ini"
mkdir -p /root/.secrets
chmod 700 /root/.secrets
cat > "$CF_CREDS" <<EOF
# Auto-generated by setup-relay-wildcard.sh — do not commit.
dns_cloudflare_api_token = ${CF_TOKEN}
EOF
chmod 600 "$CF_CREDS"
ok "Wrote Cloudflare credentials to $CF_CREDS"

# Issue the cert. --cert-name pins the lineage so re-runs renew in
# place instead of stamping out cert-0001/-0002 directories.
CERT_NAME="wildcard-${EXPOSE_DOMAIN//./-}"
if certbot certificates 2>/dev/null | grep -q "Certificate Name: ${CERT_NAME}"; then
  log "  Cert ${CERT_NAME} already exists — renewing if due"
  certbot renew --cert-name "${CERT_NAME}" --non-interactive 2>/dev/null || true
  ok "Cert ${CERT_NAME} present"
else
  log "  Requesting new wildcard cert (DNS-01 propagation, ~30s)..."
  if certbot certonly \
      --dns-cloudflare \
      --dns-cloudflare-credentials "$CF_CREDS" \
      --dns-cloudflare-propagation-seconds 30 \
      -d "*.${EXPOSE_DOMAIN}" \
      --non-interactive --agree-tos --email "$EMAIL" \
      --cert-name "${CERT_NAME}"; then
    ok "Wildcard cert issued"
  else
    err "certbot failed. Check /var/log/letsencrypt/letsencrypt.log"
  fi
fi

CERT_DIR="/etc/letsencrypt/live/${CERT_NAME}"
[ -f "$CERT_DIR/fullchain.pem" ] || err "Cert files missing at $CERT_DIR"

# ── Step 3: nginx wildcard server block ─────────────────────────────

log "Step 3/4: Configuring nginx wildcard server block"

NGINX_CONF_NAME="yaver-relay-wildcard-${EXPOSE_DOMAIN//./-}"
if [ -d /etc/nginx/sites-available ]; then
  NGINX_CONF="/etc/nginx/sites-available/${NGINX_CONF_NAME}"
  NGINX_LINK="/etc/nginx/sites-enabled/${NGINX_CONF_NAME}"
elif [ -d /etc/nginx/conf.d ]; then
  NGINX_CONF="/etc/nginx/conf.d/${NGINX_CONF_NAME}.conf"
  NGINX_LINK=""
else
  err "Could not find /etc/nginx/sites-available or /etc/nginx/conf.d"
fi

cat > "$NGINX_CONF" <<NGINX
# Auto-generated by setup-relay-wildcard.sh
# Wildcard subdomain proxy: <deviceId>.${EXPOSE_DOMAIN} → relay
# Each connected agent's auto-assigned URL terminates here.
server {
    listen 443 ssl http2;
    server_name *.${EXPOSE_DOMAIN};

    ssl_certificate     ${CERT_DIR}/fullchain.pem;
    ssl_certificate_key ${CERT_DIR}/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;

    # Long-lived dev-server proxies (Metro, Vite, Next) need
    # generous timeouts; the relay just pipes bytes.
    proxy_read_timeout  600s;
    proxy_send_timeout  600s;
    proxy_buffering     off;

    location / {
        proxy_pass http://127.0.0.1:${RELAY_HTTP_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        # Pass Upgrade for SSE / websockets used by /dev/events.
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}

# Redirect plain HTTP to HTTPS for the wildcard.
server {
    listen 80;
    server_name *.${EXPOSE_DOMAIN};
    return 301 https://\$host\$request_uri;
}
NGINX

[ -n "$NGINX_LINK" ] && ln -sf "$NGINX_CONF" "$NGINX_LINK"

if nginx -t 2>/dev/null; then
  systemctl reload nginx
  ok "Nginx reloaded with wildcard config"
else
  nginx -t  # show the error
  err "nginx config invalid — left $NGINX_CONF in place for inspection, did NOT reload"
fi

# ── Step 4: Restart the relay so EXPOSE_DOMAIN takes effect ─────────

log "Step 4/4: Restarting relay so it starts publishing assigned URLs"

did_restart=false
case "$RESTART_MODE" in
  docker|auto)
    if [ "$RESTART_MODE" = "docker" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^yaver-relay$'; then
      if [ -f /opt/yaver-relay/docker-compose.yml ]; then
        # Add or update EXPOSE_DOMAIN in the compose env block.
        if grep -q 'EXPOSE_DOMAIN=' /opt/yaver-relay/docker-compose.yml; then
          sed -i "s|EXPOSE_DOMAIN=.*|EXPOSE_DOMAIN=${EXPOSE_DOMAIN}|" /opt/yaver-relay/docker-compose.yml
        else
          # Insert under environment: block (best-effort awk).
          awk -v ed="$EXPOSE_DOMAIN" '
            /environment:/ { print; print "      - EXPOSE_DOMAIN=" ed; next }
            { print }
          ' /opt/yaver-relay/docker-compose.yml > /tmp/dc.yml.new && \
            mv /tmp/dc.yml.new /opt/yaver-relay/docker-compose.yml
        fi
        (cd /opt/yaver-relay && docker compose up -d 2>/dev/null || docker-compose up -d)
        ok "Relay container restarted with EXPOSE_DOMAIN=${EXPOSE_DOMAIN}"
        did_restart=true
      fi
    fi
    ;;
esac

case "$RESTART_MODE" in
  systemd|auto)
    if [ "$did_restart" = false ] && systemctl list-unit-files 2>/dev/null | grep -q '^yaver-relay\.service'; then
      mkdir -p /etc/systemd/system/yaver-relay.service.d
      cat > /etc/systemd/system/yaver-relay.service.d/expose-domain.conf <<EOF
# Auto-generated by setup-relay-wildcard.sh
[Service]
Environment=EXPOSE_DOMAIN=${EXPOSE_DOMAIN}
EOF
      systemctl daemon-reload
      systemctl restart yaver-relay
      ok "yaver-relay systemd unit restarted with drop-in EXPOSE_DOMAIN=${EXPOSE_DOMAIN}"
      did_restart=true
    fi
    ;;
esac

if [ "$did_restart" = false ]; then
  if [ "$RESTART_MODE" = "none" ]; then
    log "  --restart-mode=none — skipping relay restart"
  else
    warn "Couldn't auto-detect docker-compose at /opt/yaver-relay or systemd unit yaver-relay.service."
    warn "Restart your relay manually with EXPOSE_DOMAIN=${EXPOSE_DOMAIN} so the change takes effect."
  fi
fi

# ── Done ────────────────────────────────────────────────────────────

echo
log "Wildcard setup complete."
log ""
log "  → DNS:    *.${EXPOSE_DOMAIN} A ${PUBLIC_IP} (DNS-only / gray cloud)"
log "  → TLS:    /etc/letsencrypt/live/${CERT_NAME}/ (auto-renew via certbot.timer)"
log "  → Nginx:  ${NGINX_CONF}"
log ""
log "Verify by connecting an agent and watching its log for"
log "  [RELAY] Assigned public URL https://<deviceId>.${EXPOSE_DOMAIN}"
log "then curl https://<deviceId>.${EXPOSE_DOMAIN}/health → expect 200."
