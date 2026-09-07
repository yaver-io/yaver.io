#!/usr/bin/env bash
# Provision a managed relay server on Hetzner Cloud.
# Called by the backend webhook after a successful payment.
#
# Usage: ./scripts/provision-relay.sh <user-id> <domain> <password> [region]
#
# Requires:
#   - HCLOUD_TOKEN env var (Hetzner Cloud API token)
#   - SSH key registered in Hetzner Cloud
#
# Creates a CAX11 ARM server, deploys relay via Docker, sets up nginx + Let's Encrypt.

set -euo pipefail

USER_ID="${1:?Usage: provision-relay.sh <user-id> <domain> <password> [region]}"
DOMAIN="${2:?Domain required}"
PASSWORD="${3:?Password required}"
REGION="${4:-eu-central}" # eu-central (Falkenstein) or us-east (Ashburn)
HCLOUD_TOKEN="${HCLOUD_TOKEN:?Set HCLOUD_TOKEN env var}"

SERVER_NAME="relay-${USER_ID:0:8}"
SERVER_TYPE="cax11" # 2 vCPU ARM, 4GB RAM, ~3.79 EUR/mo
IMAGE="docker-ce"
SSH_KEY="${HCLOUD_SSH_KEY:-yaver-deploy}"

echo "Provisioning managed relay for $USER_ID..."
echo "  Domain: $DOMAIN"
echo "  Region: $REGION"
echo "  Server type: $SERVER_TYPE"

# Map region to Hetzner datacenter
case "$REGION" in
  eu*) DATACENTER="fsn1-dc14" ;;
  us*) DATACENTER="ash-dc1" ;;
  *) DATACENTER="fsn1-dc14" ;;
esac

# Create server
echo "Creating Hetzner server..."
SERVER_RESPONSE=$(curl -s -X POST "https://api.hetzner.cloud/v1/servers" \
  -H "Authorization: Bearer $HCLOUD_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"$SERVER_NAME\",
    \"server_type\": \"$SERVER_TYPE\",
    \"image\": \"$IMAGE\",
    \"datacenter\": \"$DATACENTER\",
    \"ssh_keys\": [\"$SSH_KEY\"],
    \"labels\": {
      \"service\": \"yaver-relay\",
      \"user\": \"${USER_ID:0:8}\",
      \"managed\": \"true\"
    },
    \"user_data\": \"$(cat <<'CLOUDCONFIG'
#cloud-config
packages:
  - docker.io
  - docker-compose
  - nginx
  - certbot
  - python3-certbot-nginx
runcmd:
  - systemctl enable docker
  - systemctl start docker
CLOUDCONFIG
)\"
  }")

SERVER_ID=$(echo "$SERVER_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['server']['id'])" 2>/dev/null)
SERVER_IP=$(echo "$SERVER_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['server']['public_net']['ipv4']['ip'])" 2>/dev/null)

if [ -z "$SERVER_ID" ] || [ "$SERVER_ID" = "null" ]; then
  echo "ERROR: Failed to create server"
  echo "$SERVER_RESPONSE"
  exit 1
fi

echo "Server created: ID=$SERVER_ID IP=$SERVER_IP"

# Wait for server to be ready
echo "Waiting for server to be ready..."
for i in $(seq 1 60); do
  STATUS=$(curl -s "https://api.hetzner.cloud/v1/servers/$SERVER_ID" \
    -H "Authorization: Bearer $HCLOUD_TOKEN" | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['server']['status'])" 2>/dev/null)
  if [ "$STATUS" = "running" ]; then
    echo "Server is running!"
    break
  fi
  sleep 5
done

# Wait for SSH to be ready
echo "Waiting for SSH..."
for i in $(seq 1 30); do
  if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 root@"$SERVER_IP" "echo ready" 2>/dev/null; then
    break
  fi
  sleep 5
done

# Deploy relay server via Docker
echo "Deploying relay server..."
ssh -o StrictHostKeyChecking=no root@"$SERVER_IP" bash <<DEPLOY
set -e

# Wait for cloud-init to finish
cloud-init status --wait 2>/dev/null || sleep 30

# Pull and run relay
mkdir -p /opt/yaver-relay
cat > /opt/yaver-relay/docker-compose.yml <<YML
version: '3'
services:
  relay:
    image: ghcr.io/kivanccakmak/yaver-relay:latest
    restart: always
    ports:
      - "4433:4433/udp"
      - "8080:8080"
      - "3478:3478/udp"
    environment:
      - RELAY_PASSWORD=$PASSWORD
      - RELAY_QUIC_PORT=4433
      - RELAY_HTTP_PORT=8080
      - TURN_PORT=3478
      - TURN_PUBLIC_IP=$SERVER_IP
    volumes:
      - relay-data:/var/lib/yaver-relay
volumes:
  relay-data:
YML

cd /opt/yaver-relay
docker compose pull
docker compose up -d

# Setup nginx reverse proxy + SSL
cat > /etc/nginx/sites-available/relay <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name $DOMAIN;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \\\$host;
        proxy_set_header X-Real-IP \\\$remote_addr;
        proxy_set_header X-Forwarded-For \\\$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \\\$scheme;
        proxy_read_timeout 300s;
        proxy_buffering off;
    }
}
NGINX

ln -sf /etc/nginx/sites-available/relay /etc/nginx/sites-enabled/
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl reload nginx

# Get SSL cert
certbot --nginx -d $DOMAIN --non-interactive --agree-tos --email support@yaver.io || true

echo "Relay deployed successfully!"
DEPLOY

echo ""
echo "=== Managed Relay Provisioned ==="
echo "Server ID: $SERVER_ID"
echo "Server IP: $SERVER_IP"
echo "Domain: $DOMAIN"
echo "QUIC: $SERVER_IP:4433"
echo "HTTPS: https://$DOMAIN"
echo "TURN: $SERVER_IP:3478 (UDP — colocated WebRTC TURN; agents derive turn:$DOMAIN:3478 from RELAY_URL)"
echo "Password: $PASSWORD"

# Output JSON for automation
cat <<JSON
{"serverId":"$SERVER_ID","serverIp":"$SERVER_IP","domain":"$DOMAIN","quicPort":4433,"httpPort":443}
JSON
