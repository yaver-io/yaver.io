#!/usr/bin/env bash
# One-shot bootstrap for a fresh Hetzner (or any Debian/Ubuntu) box.
# Run from a machine that already has SSH access:
#   ssh root@<HETZNER_IP> 'bash -s' < cloud/deploy.sh
set -euo pipefail

DOMAIN="${CLOUD_DOMAIN:-cloud.yaver.io}"
REPO_URL="${REPO_URL:-https://github.com/kivanccakmak/yaver.io.git}"
BRANCH="${BRANCH:-main}"
ENV_FILE_REMOTE="/opt/yaver-cloud/cloud/.env"

if ! command -v docker >/dev/null 2>&1; then
  echo "[deploy] installing docker"
  curl -fsSL https://get.docker.com | sh
fi

if [ ! -d /opt/yaver-cloud ]; then
  echo "[deploy] cloning $REPO_URL ($BRANCH)"
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" /opt/yaver-cloud
else
  echo "[deploy] updating /opt/yaver-cloud"
  (cd /opt/yaver-cloud && git fetch --depth 1 origin "$BRANCH" && git checkout "$BRANCH" && git reset --hard "origin/$BRANCH")
fi

cd /opt/yaver-cloud

if [ ! -f "$ENV_FILE_REMOTE" ]; then
  echo "[deploy] creating cloud/.env from example — edit this and re-run"
  cp cloud/.env.example "$ENV_FILE_REMOTE"
  sed -i "s|CLOUD_DOMAIN=.*|CLOUD_DOMAIN=$DOMAIN|" "$ENV_FILE_REMOTE"
  if [ -z "${CLOUD_OWNER_TOKEN:-}" ]; then
    gen_token=$(openssl rand -hex 32)
    sed -i "s|CLOUD_OWNER_TOKEN=.*|CLOUD_OWNER_TOKEN=$gen_token|" "$ENV_FILE_REMOTE"
    echo "[deploy] generated CLOUD_OWNER_TOKEN — grab it from $ENV_FILE_REMOTE"
  fi
fi

docker compose --env-file "$ENV_FILE_REMOTE" -f cloud/docker-compose.yml up -d --build

echo
echo "[deploy] done. once DNS for $DOMAIN points at this box, try:"
echo "  curl https://$DOMAIN/health"
echo
echo "to push a phone project from your laptop:"
echo "  yaver phone push <slug> --to https://$DOMAIN"
