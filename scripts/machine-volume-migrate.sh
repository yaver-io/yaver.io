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
#   /root            → workspace, repos, ~/.yaver (config, runtimes, vault)
#   /var/lib/docker  → images + layers
#   ~/.ollama        → model weights (rides along inside /root)
#
# SAFETY: copy-then-swap. We rsync into the volume, VERIFY, and only then
# bind-mount over the originals. The original data is left in place under
# /root.premigrate until you're satisfied — nothing is deleted by this script.
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

if mountpoint -q /root && grep -q " /root " /proc/mounts; then
  log "already migrated (/root is a bind mount) — nothing to do"
  exit 0
fi

# Format only if the volume has no filesystem. Hetzner pre-formats ext4 when the
# volume is created with format=ext4; this guard means we never wipe real data.
if ! blkid "$DEV" >/dev/null 2>&1; then
  log "volume is blank — creating ext4"
  mkfs.ext4 -F "$DEV"
fi

mkdir -p "$MNT"
mountpoint -q "$MNT" || mount -o discard,defaults "$DEV" "$MNT"
log "volume mounted at $MNT ($(df -h "$MNT" | awk 'NR==2{print $2" total, "$4" free"}'))"

# --------------------------------------------------------------- stop things --
# The agent runs out of /root; Docker holds /var/lib/docker open. Both must be
# down before we copy, or we snapshot a torn state.
log "stopping yaver + docker"
XDG_RUNTIME_DIR=/run/user/0 systemctl --user stop yaver 2>/dev/null || true
systemctl stop docker docker.socket 2>/dev/null || true
sleep 2

# -------------------------------------------------------------------- copy --
mkdir -p "$MNT/root" "$MNT/docker"

log "copying /root → $MNT/root (this is the big one — repos, models, runtimes)"
rsync -aHAX --numeric-ids --delete --info=progress2 /root/ "$MNT/root/"

if [ -d /var/lib/docker ]; then
  log "copying /var/lib/docker → $MNT/docker"
  rsync -aHAX --numeric-ids --delete /var/lib/docker/ "$MNT/docker/"
fi

# ------------------------------------------------------------------ verify --
SRC_N=$(find /root -xdev | wc -l)
DST_N=$(find "$MNT/root" -xdev | wc -l)
log "verify: /root=$SRC_N entries, volume=$DST_N entries"
if [ "$DST_N" -lt $(( SRC_N * 95 / 100 )) ]; then
  log "ERROR: copy looks short — ABORTING before any swap. Original data untouched."
  systemctl start docker 2>/dev/null || true
  XDG_RUNTIME_DIR=/run/user/0 systemctl --user start yaver 2>/dev/null || true
  exit 1
fi

# -------------------------------------------------------------------- swap --
# Keep the originals (renamed) rather than deleting — recovery is one mv away.
log "swapping in the volume via bind mounts (originals kept at *.premigrate)"
[ -d /root.premigrate ] || cp -a /root /root.premigrate 2>/dev/null || true

# fstab: volume first, then the bind mounts. `nofail` so a detached volume can
# never leave the box unbootable.
sed -i '/HC_Volume_/d;/# yaver-volume/d' /etc/fstab
{
  echo "# yaver-volume — persistent data (survives scale-to-zero delete)"
  echo "$DEV $MNT ext4 discard,nofail,defaults 0 0"
  echo "$MNT/root /root none bind,nofail 0 0"
  echo "$MNT/docker /var/lib/docker none bind,nofail 0 0"
} >> /etc/fstab

mount --bind "$MNT/root" /root
if [ -d /var/lib/docker ]; then mount --bind "$MNT/docker" /var/lib/docker; fi

# ------------------------------------------------------------------ restart --
log "starting docker + yaver"
systemctl start docker 2>/dev/null || true
XDG_RUNTIME_DIR=/run/user/0 systemctl --user start yaver 2>/dev/null || true

log "DONE — /root and /var/lib/docker now live on volume $VOL_ID"
log "Boot disk is now slim: snapshot it as the base image for fast wakes."
df -h "$MNT" /root | sed 's/^/[volume-migrate] /'
