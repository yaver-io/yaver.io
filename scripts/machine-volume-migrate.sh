#!/usr/bin/env bash
# machine-volume-migrate.sh — move a managed box's mutable state onto its
# attached Hetzner Volume, so scale-to-zero stops needing a full-disk snapshot.
#
# WHY: park had to snapshot the whole boot disk and wake had to restore it —
# ~10 minutes of re-imaging data that never changes. A Volume survives the
# server delete and re-attaches at create, so:
#   park = delete the server (instant, nothing to snapshot)
#   wake = slim base image + attach volume (~1-2 min)
#
# WHAT MOVES (everything mutable / heavy):
#   /srv/yaver/state → container-mode agent root: ~/.yaver, repos, runner auth
#   /root            → legacy VM-mode agent root (fallback only)
#   /var/lib/docker  → images + layers
#   ~/.ollama        → model weights (rides along inside the chosen agent root)
#
# SAFETY: copy-then-swap. We rsync into the volume, VERIFY, and only then
# bind-mount over the originals. The original data is left in place under
# *.premigrate until you're satisfied — nothing is deleted by this script.
# Idempotent: re-running when already migrated is a no-op.
set -euo pipefail

VOL_ID="${1:-}"
if [ -z "$VOL_ID" ]; then
  echo "usage: $0 <hetzner-volume-id>" >&2
  exit 2
fi

DEV="/dev/disk/by-id/scsi-0HC_Volume_${VOL_ID}"
MNT="/data"

log() { echo "[volume-migrate] $*"; }

# ---------------------------------------------------------------- preflight --
[ -b "$DEV" ] || { log "ERROR: volume device $DEV not found (is it attached?)"; exit 1; }

if [ -d /srv/yaver/state ]; then
  AGENT_SRC="/srv/yaver/state"
  AGENT_DST="$MNT/state"
  AGENT_LABEL="/srv/yaver/state"
else
  AGENT_SRC="/root"
  AGENT_DST="$MNT/root"
  AGENT_LABEL="/root"
fi

if mountpoint -q "$AGENT_SRC" && grep -q " $AGENT_SRC " /proc/mounts; then
  log "already migrated ($AGENT_SRC is a bind mount) — nothing to do"
  exit 0
fi

# Hetzner pre-formats the volume ext4 at create (format=ext4), so in the normal
# path there is nothing to format — and the box's Policy Guard blocks mkfs on a
# block device anyway. Only format a genuinely blank volume, and require an
# explicit opt-in so this can never silently wipe data.
if ! blkid "$DEV" >/dev/null 2>&1; then
  if [ "${YAVER_VOLUME_ALLOW_FORMAT:-}" = "1" ]; then
    log "volume is blank — creating ext4 (YAVER_VOLUME_ALLOW_FORMAT=1)"
    mkfs.ext4 -F "$DEV"
  else
    log "ERROR: volume has no filesystem and formatting is not permitted here."
    log "       Re-create the volume with format=ext4 (the default), or re-run"
    log "       with YAVER_VOLUME_ALLOW_FORMAT=1 to format it. Aborting — safe."
    exit 1
  fi
fi

mkdir -p "$MNT"
mountpoint -q "$MNT" || mount -o discard,defaults "$DEV" "$MNT"
log "volume mounted at $MNT ($(df -h "$MNT" | awk 'NR==2{print $2" total, "$4" free"}'))"

# --------------------------------------------------------------- stop things --
# The agent state root and Docker both must be down before we copy, or we
# snapshot a torn state. In container-mode the host path is /srv/yaver/state,
# mounted into the yaver-cloud container as /root.
log "stopping yaver + docker"
XDG_RUNTIME_DIR=/run/user/0 systemctl --user stop yaver 2>/dev/null || true
systemctl stop yaver yaver-agent 2>/dev/null || true
docker stop yaver 2>/dev/null || true
systemctl stop docker docker.socket 2>/dev/null || true
sleep 2

# -------------------------------------------------------------------- copy --
mkdir -p "$AGENT_DST" "$MNT/docker"

log "copying $AGENT_SRC → $AGENT_DST (agent state — auth, repos, models, runtimes)"
rsync -aHAX --numeric-ids --delete --info=progress2 "$AGENT_SRC/" "$AGENT_DST/"

if [ -d /var/lib/docker ]; then
  log "copying /var/lib/docker → $MNT/docker"
  rsync -aHAX --numeric-ids --delete /var/lib/docker/ "$MNT/docker/"
fi

# ------------------------------------------------------------------ verify --
SRC_N=$(find "$AGENT_SRC" -xdev | wc -l)
DST_N=$(find "$AGENT_DST" -xdev | wc -l)
log "verify: $AGENT_SRC=$SRC_N entries, volume=$DST_N entries"
if [ "$DST_N" -lt $(( SRC_N * 95 / 100 )) ]; then
  log "ERROR: copy looks short — ABORTING before any swap. Original data untouched."
  systemctl start docker 2>/dev/null || true
  systemctl start yaver yaver-agent 2>/dev/null || true
  XDG_RUNTIME_DIR=/run/user/0 systemctl --user start yaver 2>/dev/null || true
  exit 1
fi

# -------------------------------------------------------------------- swap --
# Keep the originals (renamed) rather than deleting — recovery is one mv away.
log "swapping in the volume via bind mounts (originals kept at *.premigrate)"
PREMIGRATE="${AGENT_SRC}.premigrate"
[ -d "$PREMIGRATE" ] || cp -a "$AGENT_SRC" "$PREMIGRATE" 2>/dev/null || true

# fstab: volume first, then the bind mounts. `nofail` so a detached volume can
# never leave the box unbootable.
sed -i '/HC_Volume_/d;/# yaver-volume/d' /etc/fstab
{
  echo "# yaver-volume — persistent data (survives scale-to-zero delete)"
  echo "$DEV $MNT ext4 discard,nofail,defaults 0 0"
  echo "$AGENT_DST $AGENT_SRC none bind,nofail 0 0"
  echo "$MNT/docker /var/lib/docker none bind,nofail 0 0"
} >> /etc/fstab

mount --bind "$AGENT_DST" "$AGENT_SRC"
if [ -d /var/lib/docker ]; then mount --bind "$MNT/docker" /var/lib/docker; fi

# ------------------------------------------------------------------ restart --
log "starting docker + yaver"
systemctl start docker 2>/dev/null || true
systemctl start yaver yaver-agent 2>/dev/null || true
XDG_RUNTIME_DIR=/run/user/0 systemctl --user start yaver 2>/dev/null || true

log "DONE — $AGENT_LABEL and /var/lib/docker now live on volume $VOL_ID"
log "Boot disk is now slim: snapshot it as the base image for fast wakes."
df -h "$MNT" "$AGENT_SRC" | sed 's/^/[volume-migrate] /'
