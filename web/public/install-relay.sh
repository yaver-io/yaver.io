#!/usr/bin/env bash
# Yaver Relay Server — One-line installer
#
# Install a self-hosted relay server on any VPS with a single command:
#
#   curl -fsSL https://yaver.io/install-relay.sh | bash -s -- \
#     --domain relay.example.com \
#     --password my-secret
#
# Or clone and run:
#   ./scripts/install-relay.sh --domain relay.example.com --password my-secret
#
# Requirements:
#   - Linux VPS with root/sudo access (Ubuntu/Debian recommended)
#   - Public IP with ports 80, 443, 4433 open
#   - Domain pointing to this server's IP (A record)
#
# What it does:
#   1. Installs Docker (if not present)
#   2. Pulls and runs the Yaver relay container
#   3. Sets up nginx reverse proxy
#   4. Gets Let's Encrypt SSL certificate (auto-renewing)
#   5. Configures firewall
#   6. Creates systemd service for auto-start
#
# The relay is a pass-through proxy — it never stores, reads, or logs your data.
# All traffic is encrypted via QUIC (TLS 1.3).

set -euo pipefail

# ── Parse arguments ────────────────────────────────────────────────

DOMAIN=""
PASSWORD=""
QUIC_PORT=4433
HTTP_PORT=8080
EMAIL="admin@$(hostname -f 2>/dev/null || echo 'localhost')"
SKIP_SSL=false

usage() {
  echo "Usage: $0 --domain <domain> --password <password> [options]"
  echo ""
  echo "Required:"
  echo "  --domain      Domain name pointing to this server (e.g. relay.example.com)"
  echo "  --password    Relay password (agents use this to connect)"
  echo ""
  echo "Optional:"
  echo "  --email       Email for Let's Encrypt (default: admin@hostname)"
  echo "  --quic-port   QUIC tunnel port (default: 4433)"
  echo "  --http-port   Internal HTTP port (default: 8080)"
  echo "  --skip-ssl    Skip SSL setup (use if behind another proxy)"
  echo ""
  echo "Example:"
  echo "  $0 --domain relay.mycompany.com --password super-secret-123"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --domain) DOMAIN="$2"; shift 2 ;;
    --password) PASSWORD="$2"; shift 2 ;;
    --email) EMAIL="$2"; shift 2 ;;
    --quic-port) QUIC_PORT="$2"; shift 2 ;;
    --http-port) HTTP_PORT="$2"; shift 2 ;;
    --skip-ssl) SKIP_SSL=true; shift ;;
    --help|-h) usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
done

if [ -z "$DOMAIN" ] || [ -z "$PASSWORD" ]; then
  echo "Error: --domain and --password are required."
  echo ""
  usage
fi

# ── Helpers ────────────────────────────────────────────────────────

log() { echo -e "\033[1;34m[yaver-relay]\033[0m $*"; }
ok()  { echo -e "\033[1;32m  ✓\033[0m $*"; }
err() { echo -e "\033[1;31m  ✗\033[0m $*"; exit 1; }

# Check root
if [ "$(id -u)" -ne 0 ]; then
  err "Please run as root (sudo $0 ...)"
fi

log "Installing Yaver Relay Server"
log "  Domain:   $DOMAIN"
log "  QUIC:     :$QUIC_PORT"
log "  Password: ****${PASSWORD: -4}"
echo ""

# ── Step 1: Install Docker ─────────────────────────────────────────

log "Step 1/5: Checking Docker..."

if command -v docker &>/dev/null; then
  ok "Docker already installed ($(docker --version | head -1))"
else
  log "  Installing Docker..."
  if command -v apt-get &>/dev/null; then
    apt-get update -qq
    apt-get install -y -qq docker.io docker-compose-v2 2>/dev/null || apt-get install -y -qq docker.io
  elif command -v dnf &>/dev/null; then
    dnf install -y docker docker-compose
    systemctl enable docker
  elif command -v yum &>/dev/null; then
    yum install -y docker docker-compose
    systemctl enable docker
  else
    err "Unsupported package manager. Install Docker manually: https://docs.docker.com/engine/install/"
  fi
  systemctl start docker
  ok "Docker installed"
fi

# ── Step 2: Deploy relay container ─────────────────────────────────

log "Step 2/5: Deploying relay..."

mkdir -p /opt/yaver-relay

cat > /opt/yaver-relay/docker-compose.yml <<YML
services:
  relay:
    image: ghcr.io/kivanccakmak/yaver-relay:latest
    container_name: yaver-relay
    restart: always
    ports:
      - "${QUIC_PORT}:4433/udp"
      - "${HTTP_PORT}:8080"
    environment:
      - RELAY_PASSWORD=${PASSWORD}
      - RELAY_QUIC_PORT=4433
      - RELAY_HTTP_PORT=8080
      - RELAY_DATA_DIR=/data
    volumes:
      - relay-data:/data

  watchtower:
    image: containrrr/watchtower
    container_name: yaver-watchtower
    restart: always
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    command: --interval 3600 --cleanup

volumes:
  relay-data:
YML

cd /opt/yaver-relay

# Pull and start
if command -v docker compose &>/dev/null; then
  docker compose pull -q
  docker compose up -d
else
  docker-compose pull -q 2>/dev/null || docker pull ghcr.io/kivanccakmak/yaver-relay:latest
  docker-compose up -d
fi

# Wait for relay to be ready
sleep 3
if curl -sf http://127.0.0.1:${HTTP_PORT}/health &>/dev/null; then
  ok "Relay container running"
else
  err "Relay failed to start. Check: docker logs yaver-relay"
fi

# ── Step 3: Setup nginx reverse proxy ──────────────────────────────

log "Step 3/5: Setting up nginx..."

if ! command -v nginx &>/dev/null; then
  if command -v apt-get &>/dev/null; then
    apt-get install -y -qq nginx
  elif command -v dnf &>/dev/null; then
    dnf install -y nginx
  elif command -v yum &>/dev/null; then
    yum install -y nginx
  fi
fi

cat > /etc/nginx/sites-available/yaver-relay <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};

    location / {
        proxy_pass http://127.0.0.1:${HTTP_PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 300s;
        proxy_buffering off;
    }
}
NGINX

# Handle both Debian and RHEL nginx layouts
if [ -d /etc/nginx/sites-enabled ]; then
  ln -sf /etc/nginx/sites-available/yaver-relay /etc/nginx/sites-enabled/
  rm -f /etc/nginx/sites-enabled/default
elif [ -d /etc/nginx/conf.d ]; then
  cp /etc/nginx/sites-available/yaver-relay /etc/nginx/conf.d/yaver-relay.conf
fi

nginx -t &>/dev/null && systemctl reload nginx
ok "Nginx configured"

# ── Step 4: SSL certificate ────────────────────────────────────────

if [ "$SKIP_SSL" = true ]; then
  log "Step 4/5: Skipping SSL (--skip-ssl)"
  ok "SSL skipped"
else
  log "Step 4/5: Getting SSL certificate..."

  if ! command -v certbot &>/dev/null; then
    if command -v apt-get &>/dev/null; then
      apt-get install -y -qq certbot python3-certbot-nginx
    elif command -v dnf &>/dev/null; then
      dnf install -y certbot python3-certbot-nginx
    elif command -v pip3 &>/dev/null; then
      pip3 install certbot certbot-nginx
    fi
  fi

  if certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --email "$EMAIL" 2>/dev/null; then
    ok "SSL certificate installed (auto-renewing)"
  else
    log "  SSL failed — you may need to verify DNS points to this server"
    log "  Run manually: certbot --nginx -d $DOMAIN"
  fi
fi

# ── Step 5: Firewall ───────────────────────────────────────────────

log "Step 5/5: Configuring firewall..."

if command -v ufw &>/dev/null; then
  ufw allow 80/tcp &>/dev/null
  ufw allow 443/tcp &>/dev/null
  ufw allow ${QUIC_PORT}/udp &>/dev/null
  ok "UFW rules added (80, 443, ${QUIC_PORT}/udp)"
elif command -v firewall-cmd &>/dev/null; then
  firewall-cmd --permanent --add-port=80/tcp &>/dev/null
  firewall-cmd --permanent --add-port=443/tcp &>/dev/null
  firewall-cmd --permanent --add-port=${QUIC_PORT}/udp &>/dev/null
  firewall-cmd --reload &>/dev/null
  ok "Firewalld rules added"
else
  ok "No firewall detected (make sure ports 80, 443, ${QUIC_PORT}/udp are open)"
fi

# ── Done ───────────────────────────────────────────────────────────

echo ""
log "========================================="
log "  Yaver Relay Server installed!"
log "========================================="
echo ""
echo "  HTTPS:     https://${DOMAIN}"
echo "  QUIC:      $(curl -sf ifconfig.me 2>/dev/null || echo '<your-ip>'):${QUIC_PORT}"
echo "  Password:  ${PASSWORD}"
echo "  Health:    https://${DOMAIN}/health"
echo ""
echo "  Configure in Yaver CLI:"
echo "    yaver relay add https://${DOMAIN} --password ${PASSWORD}"
echo ""
echo "  Or in mobile app: Settings → Relay → Add"
echo ""
echo "  Logs:      docker logs -f yaver-relay"
echo "  Stop:      cd /opt/yaver-relay && docker compose down"
echo "  Update:    Automatic via Watchtower (checks hourly)"
echo ""
echo "  The relay is a pass-through proxy — it never stores your data."
echo "  All connections are encrypted via QUIC (TLS 1.3)."
echo ""
