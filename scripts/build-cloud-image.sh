#!/usr/bin/env bash
# Build a Yaver cloud image (Hetzner snapshot, GCP custom image, or AWS AMI).
#
# Pattern is identical across providers:
#   1. Provision a fresh Ubuntu 24.04 VM via the provider CLI
#   2. Wait for SSH
#   3. Push the cloud-image overlay + yaver binary via rsync/scp
#   4. Run ci/remote/bootstrap.sh (the same script yaver-test-ephemeral uses)
#   5. Capture a provider-specific image (snapshot / image / AMI)
#   6. Delete the build VM
#   7. Write dist/cloud-image/<provider>-<version>-<arch>.json with the image id
#
# Env vars (per provider):
#   Hetzner:  HCLOUD_TOKEN
#   GCP:      GOOGLE_APPLICATION_CREDENTIALS or `gcloud auth login` done
#             YAVER_GCP_PROJECT     (default: derived from gcloud config)
#             YAVER_GCP_ZONE        (default: europe-west4-a)
#   AWS:      AWS access keys (env / ~/.aws/credentials) and region
#             AWS_REGION            (default: eu-central-1)
#
# Usage:
#   scripts/build-cloud-image.sh --provider hetzner --arch arm64
#   scripts/build-cloud-image.sh --provider gcp     --arch amd64
#   scripts/build-cloud-image.sh --provider aws     --arch amd64

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

PROVIDER=""
ARCH=""
VERSION="${VERSION:-dev}"
YAVER_BINARY=""
KEEP_BUILD_VM=0
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist/cloud-image}"

# Provider defaults — overridable via env.
HCLOUD_LOCATION="${HCLOUD_LOCATION:-hel1}"
HCLOUD_SSH_KEY="${HCLOUD_SSH_KEY:-yaver-ci}"
YAVER_GCP_PROJECT="${YAVER_GCP_PROJECT:-}"
YAVER_GCP_ZONE="${YAVER_GCP_ZONE:-europe-west4-a}"
AWS_REGION="${AWS_REGION:-eu-central-1}"

usage() {
  cat <<'EOF'
Usage:
  scripts/build-cloud-image.sh --provider <hetzner|gcp|aws> --arch <amd64|arm64> [options]

Options:
  --provider <name>       Cloud provider: hetzner, gcp, aws
  --arch <amd64|arm64>    Target arch (Hetzner cax* => arm64, cx*/cpx* => amd64)
  --version <semver>      Version label embedded in release JSON (default: dev)
  --yaver-binary <path>   Pre-built yaver binary; otherwise built on the host
                          via go build with GOOS=linux GOARCH=<arch>
  --keep-build-vm         Leave the build VM running after capture (debug)
  -h, --help              Show help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider)       PROVIDER="${2:-}"; shift 2 ;;
    --arch)           ARCH="${2:-}"; shift 2 ;;
    --version)        VERSION="${2:-}"; shift 2 ;;
    --yaver-binary)   YAVER_BINARY="${2:-}"; shift 2 ;;
    --keep-build-vm)  KEEP_BUILD_VM=1; shift ;;
    -h|--help)        usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$PROVIDER" ]] || { echo "--provider required" >&2; usage >&2; exit 2; }
[[ -n "$ARCH"     ]] || { echo "--arch required"     >&2; usage >&2; exit 2; }

case "$ARCH" in
  amd64|arm64) ;;
  *) echo "--arch must be amd64 or arm64" >&2; exit 2 ;;
esac

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

log() { printf '\n[cloud-image] %s\n' "$*"; }

mkdir -p "$OUTPUT_DIR"

# ─────────────────────────────────────────────────────────────
# Build the yaver binary (or accept a pre-built one)
# ─────────────────────────────────────────────────────────────
require_cmd ssh
require_cmd rsync
require_cmd jq

BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/yaver-cloud-image.XXXXXX")"
trap 'rm -rf "$BUILD_DIR"' EXIT

STAGED_BINARY="$BUILD_DIR/yaver-linux-$ARCH"

if [[ -n "$YAVER_BINARY" ]]; then
  cp "$YAVER_BINARY" "$STAGED_BINARY"
else
  require_cmd go
  log "building yaver linux/$ARCH binary"
  (
    cd "$ROOT_DIR/desktop/agent"
    GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w" -o "$STAGED_BINARY" .
  )
fi
chmod +x "$STAGED_BINARY"

# Stage the rootfs overlay + release metadata
STAGED_OVERLAY="$BUILD_DIR/overlay"
mkdir -p "$STAGED_OVERLAY"
rsync -a "$ROOT_DIR/cloud-image/rootfs/" "$STAGED_OVERLAY/"
mkdir -p "$STAGED_OVERLAY/etc/yaver" "$STAGED_OVERLAY/usr/local/bin"
cp "$STAGED_BINARY" "$STAGED_OVERLAY/usr/local/bin/yaver"

BUILT_AT="$(date -u +%FT%TZ)"
cat > "$STAGED_OVERLAY/etc/yaver/cloud-image-release.json" <<EOF
{
  "version": "$VERSION",
  "arch": "$ARCH",
  "provider": "$PROVIDER",
  "builtAtUtc": "$BUILT_AT"
}
EOF

# No-leak guard: this overlay ships to EVERY booted box — and for BYO it
# is snapshotted into the USER'S OWN provider account — so it must carry
# ZERO secrets (per-box tokens are injected at boot via cloud-init, never
# baked). Fail the build if a secret-shaped string appears in any text
# file. grep -I skips the compiled yaver binary (no false positives on
# its random bytes); the release json is the one allowed hit.
log "scanning overlay for accidental secrets (no-leak guard)"
SECRET_HITS="$(grep -rInE \
  'BEGIN (RSA|OPENSSH|EC|DSA|PGP) PRIVATE KEY|AKIA[0-9A-Z]{16}|(hcloud|api[_-]?key|secret|password|token)[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9_/+-]{16,}' \
  "$STAGED_OVERLAY" 2>/dev/null | grep -vE 'cloud-image-release\.json' || true)"
if [ -n "$SECRET_HITS" ]; then
  log "ABORT: possible secret(s) baked into the image overlay — remove before building (they would ship to every box, and into the BYO user's own account):"
  printf '%s\n' "$SECRET_HITS" >&2
  exit 1
fi
log "overlay clean — no secret-shaped strings"

# ─────────────────────────────────────────────────────────────
# Provider-specific provisioning
# ─────────────────────────────────────────────────────────────
SSH_USER="root"
SSH_HOST=""
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_rsa}"
CAPTURE_ARTIFACT=""

provision_hetzner() {
  require_cmd hcloud
  : "${HCLOUD_TOKEN:?HCLOUD_TOKEN must be set for --provider hetzner}"

  # arm64 => cax21, amd64 => cpx21. Both cheapest in their family.
  local server_type
  case "$ARCH" in
    arm64) server_type="cax21" ;;
    amd64) server_type="cpx21" ;;
  esac

  local server_name="yaver-image-build-$VERSION-$ARCH-$(date +%s)"
  local user_data_file="$BUILD_DIR/hetzner-user-data.yaml"
  cp "$ROOT_DIR/cloud-image/cloud-init/user-data" "$user_data_file"

  log "creating Hetzner server $server_name ($server_type, $HCLOUD_LOCATION)"
  hcloud server create \
    --name "$server_name" \
    --type "$server_type" \
    --location "$HCLOUD_LOCATION" \
    --image "ubuntu-24.04" \
    --ssh-key "$HCLOUD_SSH_KEY" \
    --user-data-from-file "$user_data_file" \
    --label purpose=image-build \
    --label managed-by=yaver-cloud-image \
    -o json > "$BUILD_DIR/hetzner-create.json"

  SSH_HOST="$(jq -r '.server.public_net.ipv4.ip' "$BUILD_DIR/hetzner-create.json")"
  echo "$server_name" > "$BUILD_DIR/hetzner-server-name"

  log "waiting for SSH on $SSH_HOST"
  wait_for_ssh "$SSH_HOST"

  bootstrap_remote "$SSH_HOST"

  log "shutting down for clean snapshot"
  ssh -o StrictHostKeyChecking=no -i "$SSH_KEY" "$SSH_USER@$SSH_HOST" \
    "sync; systemctl poweroff" || true

  # Wait for the server to actually stop. Hetzner refuses snapshots on
  # running servers more cleanly when off (avoids in-flight FS state).
  log "waiting for server to power off"
  local i
  for i in $(seq 1 60); do
    local status
    status="$(hcloud server describe "$server_name" -o json | jq -r '.status')"
    if [[ "$status" == "off" ]]; then break; fi
    sleep 5
  done

  local desc="Yaver Cloud Image $VERSION ($ARCH) — built $BUILT_AT"
  log "creating snapshot"
  hcloud server create-image \
    --type snapshot \
    --description "$desc" \
    --label yaver-image-version="$VERSION" \
    --label yaver-image-arch="$ARCH" \
    "$server_name" \
    -o json > "$BUILD_DIR/hetzner-snapshot.json"

  CAPTURE_ARTIFACT="$(jq -r '.image.id' "$BUILD_DIR/hetzner-snapshot.json")"

  if [[ "$KEEP_BUILD_VM" -ne 1 ]]; then
    log "deleting build VM"
    hcloud server delete "$server_name" || true
  fi

  write_release_json "$CAPTURE_ARTIFACT" \
    "$(jq -c '.image' "$BUILD_DIR/hetzner-snapshot.json")"
}

provision_gcp() {
  require_cmd gcloud
  if [[ -z "$YAVER_GCP_PROJECT" ]]; then
    YAVER_GCP_PROJECT="$(gcloud config get-value project 2>/dev/null || true)"
  fi
  [[ -n "$YAVER_GCP_PROJECT" ]] || {
    echo "YAVER_GCP_PROJECT not set and gcloud config has no project" >&2
    exit 1
  }

  # GCP arch mapping. e2-small is amd64; t2a-standard-1 is the Ampere arm64.
  local machine_type
  local source_image
  case "$ARCH" in
    amd64)
      machine_type="e2-small"
      source_image="projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-amd64"
      ;;
    arm64)
      machine_type="t2a-standard-1"
      source_image="projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-arm64"
      ;;
  esac

  local instance_name="yaver-image-build-${VERSION//[^a-z0-9]/-}-$ARCH-$(date +%s)"
  instance_name="$(echo "$instance_name" | tr '[:upper:]' '[:lower:]' | cut -c1-63)"

  log "creating GCP instance $instance_name ($machine_type, $YAVER_GCP_ZONE)"
  gcloud compute instances create "$instance_name" \
    --project="$YAVER_GCP_PROJECT" \
    --zone="$YAVER_GCP_ZONE" \
    --machine-type="$machine_type" \
    --image="$source_image" \
    --metadata-from-file="user-data=$ROOT_DIR/cloud-image/cloud-init/user-data" \
    --labels="purpose=image-build,managed-by=yaver-cloud-image" \
    --format=json > "$BUILD_DIR/gcp-create.json"

  SSH_HOST="$(jq -r '.[0].networkInterfaces[0].accessConfigs[0].natIP' "$BUILD_DIR/gcp-create.json")"
  echo "$instance_name" > "$BUILD_DIR/gcp-instance-name"

  log "waiting for SSH on $SSH_HOST"
  wait_for_ssh "$SSH_HOST"

  bootstrap_remote "$SSH_HOST"

  log "stopping instance for image capture"
  gcloud compute instances stop "$instance_name" \
    --project="$YAVER_GCP_PROJECT" \
    --zone="$YAVER_GCP_ZONE" \
    --quiet

  local image_name="yaver-cloud-${VERSION//[^a-z0-9]/-}-$ARCH"
  image_name="$(echo "$image_name" | tr '[:upper:]' '[:lower:]' | cut -c1-63)"

  log "creating GCP custom image $image_name"
  gcloud compute images create "$image_name" \
    --project="$YAVER_GCP_PROJECT" \
    --source-disk="$instance_name" \
    --source-disk-zone="$YAVER_GCP_ZONE" \
    --family="yaver-cloud-$ARCH" \
    --description="Yaver Cloud Image $VERSION ($ARCH) — built $BUILT_AT" \
    --labels="yaver-image-version=${VERSION//[^a-z0-9-]/-},yaver-image-arch=$ARCH" \
    --format=json > "$BUILD_DIR/gcp-image.json"

  CAPTURE_ARTIFACT="projects/$YAVER_GCP_PROJECT/global/images/$image_name"

  if [[ "$KEEP_BUILD_VM" -ne 1 ]]; then
    log "deleting build instance"
    gcloud compute instances delete "$instance_name" \
      --project="$YAVER_GCP_PROJECT" \
      --zone="$YAVER_GCP_ZONE" \
      --quiet || true
  fi

  write_release_json "$CAPTURE_ARTIFACT" \
    "$(jq -c '.' "$BUILD_DIR/gcp-image.json")"
}

provision_aws() {
  require_cmd aws

  local instance_type
  local owner_alias_filter="amazon"
  case "$ARCH" in
    amd64) instance_type="t3.small" ;;
    arm64) instance_type="t4g.small" ;;
  esac

  # Resolve latest Ubuntu 24.04 AMI for the region+arch via SSM. Cleaner
  # than parsing image search results and the canonical Ubuntu pattern.
  local ssm_param="/aws/service/canonical/ubuntu/server/24.04/stable/current/$ARCH/hvm/ebs-gp2/ami-id"
  local source_ami
  source_ami="$(aws ssm get-parameter \
    --region "$AWS_REGION" \
    --name "$ssm_param" \
    --query 'Parameter.Value' --output text)"

  local key_name="${AWS_SSH_KEY_NAME:-yaver-cloud-image}"
  local instance_tag="yaver-image-build-$VERSION-$ARCH-$(date +%s)"

  log "creating AWS instance ($instance_type, ami=$source_ami, region=$AWS_REGION)"
  aws ec2 run-instances \
    --region "$AWS_REGION" \
    --image-id "$source_ami" \
    --instance-type "$instance_type" \
    --key-name "$key_name" \
    --user-data "file://$ROOT_DIR/cloud-image/cloud-init/user-data" \
    --tag-specifications \
      "ResourceType=instance,Tags=[{Key=Name,Value=$instance_tag},{Key=purpose,Value=image-build},{Key=managed-by,Value=yaver-cloud-image}]" \
    --query 'Instances[0].InstanceId' \
    --output text > "$BUILD_DIR/aws-instance-id"

  local instance_id
  instance_id="$(cat "$BUILD_DIR/aws-instance-id")"

  log "waiting for instance $instance_id to enter running state"
  aws ec2 wait instance-running --region "$AWS_REGION" --instance-ids "$instance_id"

  SSH_HOST="$(aws ec2 describe-instances \
    --region "$AWS_REGION" \
    --instance-ids "$instance_id" \
    --query 'Reservations[0].Instances[0].PublicIpAddress' \
    --output text)"
  SSH_USER="ubuntu"

  log "waiting for SSH on $SSH_HOST"
  wait_for_ssh "$SSH_HOST"

  bootstrap_remote "$SSH_HOST"

  log "stopping instance for AMI creation"
  aws ec2 stop-instances --region "$AWS_REGION" --instance-ids "$instance_id" >/dev/null
  aws ec2 wait instance-stopped --region "$AWS_REGION" --instance-ids "$instance_id"

  local ami_name="yaver-cloud-$VERSION-$ARCH-$(date +%s)"
  log "creating AMI $ami_name"
  local ami_id
  ami_id="$(aws ec2 create-image \
    --region "$AWS_REGION" \
    --instance-id "$instance_id" \
    --name "$ami_name" \
    --description "Yaver Cloud Image $VERSION ($ARCH) — built $BUILT_AT" \
    --tag-specifications \
      "ResourceType=image,Tags=[{Key=yaver-image-version,Value=$VERSION},{Key=yaver-image-arch,Value=$ARCH}]" \
    --query 'ImageId' --output text)"

  log "waiting for AMI $ami_id to become available"
  aws ec2 wait image-available --region "$AWS_REGION" --image-ids "$ami_id"

  CAPTURE_ARTIFACT="$ami_id"

  if [[ "$KEEP_BUILD_VM" -ne 1 ]]; then
    log "terminating build instance"
    aws ec2 terminate-instances --region "$AWS_REGION" --instance-ids "$instance_id" >/dev/null || true
  fi

  local ami_json
  ami_json="$(aws ec2 describe-images \
    --region "$AWS_REGION" --image-ids "$ami_id" \
    --query 'Images[0]' --output json)"
  write_release_json "$ami_id" "$ami_json"
}

# ─────────────────────────────────────────────────────────────
# Shared helpers
# ─────────────────────────────────────────────────────────────

wait_for_ssh() {
  local host="$1" i
  for i in $(seq 1 60); do
    if ssh -o StrictHostKeyChecking=no \
           -o UserKnownHostsFile=/dev/null \
           -o ConnectTimeout=5 \
           -o BatchMode=yes \
           -i "$SSH_KEY" \
           "$SSH_USER@$host" "true" 2>/dev/null; then
      log "SSH ready after ${i}x 5s"
      # Give cloud-init a beat to start its first runcmd
      sleep 5
      return 0
    fi
    sleep 5
  done
  echo "SSH never came up on $host" >&2
  exit 1
}

bootstrap_remote() {
  local host="$1"

  log "uploading cloud-image overlay + ci/remote/ to $host"
  rsync -az -e "ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
    "$STAGED_OVERLAY/" "$SSH_USER@$host:/tmp/yaver-overlay/"
  rsync -az -e "ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
    "$ROOT_DIR/ci/remote/" "$SSH_USER@$host:/tmp/yaver-ci-remote/"

  log "applying overlay + running ci/remote/bootstrap.sh"
  ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      -i "$SSH_KEY" "$SSH_USER@$host" "bash -se" <<'REMOTE_SCRIPT'
set -euo pipefail
sudo_run() { if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi; }

# Wait for cloud-init's runcmd block to finish so we don't race apt locks.
sudo_run cloud-init status --wait || true

sudo_run rsync -a /tmp/yaver-overlay/ /
sudo_run chmod +x /usr/local/bin/yaver
sudo_run mkdir -p /opt/yaver/ci
sudo_run rsync -a /tmp/yaver-ci-remote/ /opt/yaver/ci/remote/

# The bootstrap.sh script needs root + a working network. Run it under
# `sudo bash` rather than just `bash` so its install steps don't error
# on a non-root SSH user (AWS uses ubuntu, Hetzner uses root).
sudo_run bash /opt/yaver/ci/remote/bootstrap.sh

# Final hygiene: clear cloud-init state so the captured image will run
# cloud-init fresh on every launched instance, not reuse our build-VM
# instance-id. Without this, downstream VMs skip cloud-init entirely.
sudo_run cloud-init clean --logs --seed || sudo_run cloud-init clean --logs || true
sudo_run rm -rf /var/lib/cloud/instances /var/lib/cloud/instance
sudo_run rm -rf /tmp/yaver-overlay /tmp/yaver-ci-remote
REMOTE_SCRIPT
}

write_release_json() {
  local image_id="$1" provider_meta="$2"
  local out="$OUTPUT_DIR/$PROVIDER-$VERSION-$ARCH.json"
  jq -n \
    --arg provider "$PROVIDER" \
    --arg version  "$VERSION" \
    --arg arch     "$ARCH" \
    --arg imageId  "$image_id" \
    --arg builtAt  "$BUILT_AT" \
    --argjson providerMeta "$provider_meta" \
    '{provider:$provider, version:$version, arch:$arch, imageId:$imageId,
      builtAtUtc:$builtAt, providerMeta:$providerMeta}' \
    > "$out"
  log "wrote $out"
  log "image id: $image_id"
}

case "$PROVIDER" in
  hetzner) provision_hetzner ;;
  gcp)     provision_gcp ;;
  aws)     provision_aws ;;
  *)
    echo "unknown provider: $PROVIDER (expected hetzner|gcp|aws)" >&2
    exit 2
    ;;
esac

log "done — image id: $CAPTURE_ARTIFACT"
