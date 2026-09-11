#!/usr/bin/env bash
# Execute Yaver's canonical Android release entrypoint on a small ARM64 Linux
# host. Product source, credentials, caches, and outputs remain Yaver-scoped.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ACTION="${1:-ensure}"
IMAGE="${YAVER_ANDROID_BUILDER_IMAGE:-yaver-android-builder:api35-ndk27-jdk17-go1.26}"
CACHE_ROOT="${YAVER_ANDROID_CACHE_ROOT:-$HOME/.cache/yaver-android-builder}"
DOCKERFILE="$ROOT/deploy/android-builder/Dockerfile"

fail() { echo "ERROR: $*" >&2; exit 2; }
need() { command -v "$1" >/dev/null 2>&1 || fail "missing command: $1"; }

ensure_host() {
  [ "$(uname -s)" = Linux ] || fail "the Android builder container requires Linux"
  need docker
  docker info >/dev/null 2>&1 || fail "Docker is unavailable to the current user"
  [ -f "$DOCKERFILE" ] || fail "missing Android builder Dockerfile: $DOCKERFILE"
  [ -z "$(git -C "$ROOT" status --porcelain)" ] \
    || fail "release checkout is dirty; use a clean worktree"
  case "$(uname -m)" in
    aarch64|arm64)
      [ -e /proc/sys/fs/binfmt_misc/qemu-x86_64 ] \
        || fail "ARM64 Android builds require the qemu-x86_64 binfmt handler"
      ;;
  esac
}

ensure_image() {
  if [ "${YAVER_ANDROID_BUILDER_REBUILD:-0}" != 1 ] \
      && docker image inspect "$IMAGE" >/dev/null 2>&1; then
    echo "Using cached Yaver Android builder image: $IMAGE"
    return
  fi
  echo "Preparing bounded Yaver Android builder image..."
  docker build --pull=false --platform linux/arm64 \
    --tag "$IMAGE" --file "$DOCKERFILE" "$ROOT"
}

runtime_dir() {
  if [ -n "${YAVER_NODE_RUNTIME_DIR:-}" ]; then
    printf '%s\n' "$YAVER_NODE_RUNTIME_DIR"
    return
  fi
  local node_path
  node_path="$(command -v node 2>/dev/null || true)"
  [ -n "$node_path" ] || fail "Node.js 20+ is required on the host"
  node_path="$(readlink -f "$node_path")"
  dirname "$(dirname "$node_path")"
}

run_release() {
  shift
  [ "$#" -gt 0 ] || fail "missing canonical deploy target"
  local node_root git_common_dir git_worktree_dir min_disk_gib disk_kib
  node_root="$(runtime_dir)"
  [ -x "$node_root/bin/node" ] && [ -x "$node_root/bin/npm" ] \
    || fail "YAVER_NODE_RUNTIME_DIR must contain executable bin/node and bin/npm"
  min_disk_gib="${YAVER_ANDROID_MIN_FREE_GIB:-8}"
  case "$min_disk_gib" in ''|*[!0-9]*) fail "YAVER_ANDROID_MIN_FREE_GIB must be an integer" ;; esac
  [ "$min_disk_gib" -ge 4 ] || fail "YAVER_ANDROID_MIN_FREE_GIB must be at least 4"
  disk_kib="$(df -Pk "$ROOT" | awk 'NR==2 {print $4}')"
  [ "$disk_kib" -ge $((min_disk_gib * 1024 * 1024)) ] \
    || fail "need at least ${min_disk_gib} GiB free before the Android release"

  install -d -m 700 "$CACHE_ROOT/gradle" "$CACHE_ROOT/npm" "$CACHE_ROOT/xdg" "$CACHE_ROOT/metro"
  git_common_dir="$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir)"
  git_worktree_dir="$(git -C "$ROOT" rev-parse --path-format=absolute --absolute-git-dir)"

  local docker_args=(
    run --rm --init --platform linux/arm64
    --cpus "${YAVER_ANDROID_BUILD_CPUS:-3}"
    --memory "${YAVER_ANDROID_BUILD_MEMORY:-6g}"
    --memory-swap "${YAVER_ANDROID_BUILD_MEMORY_SWAP:-9g}"
    --workdir "$ROOT"
    --volume "$ROOT:$ROOT"
    --volume "$git_common_dir:$git_common_dir:ro"
    --volume "$git_worktree_dir:$git_worktree_dir"
    --volume "$node_root:/opt/node:ro"
    --volume "$CACHE_ROOT/gradle:/root/.gradle"
    --volume "$CACHE_ROOT/npm:/root/.npm"
    --volume "$CACHE_ROOT/xdg:/root/.cache/yaver"
    --volume "$CACHE_ROOT/metro:/run/yaver-metro"
    --env YAVER_ANDROID_CONTAINER=1
    --env YAVER_ANDROID_METRO_TMPDIR=/run/yaver-metro
    --env YAVER_PLAYSTORE_MIN_FREE_GB="${YAVER_PLAYSTORE_MIN_FREE_GB:-8}"
    --env YAVER_PLAYSTORE_ABI="${YAVER_PLAYSTORE_ABI:-arm64-v8a}"
    --env NODE_OPTIONS="${NODE_OPTIONS:---max-old-space-size=2048}"
    --env GIT_OPTIONAL_LOCKS=0
  )

  if [ -n "${YAVER_PLAY_SERVICE_ACCOUNT_KEY_FILE:-}" ]; then
    [ -r "$YAVER_PLAY_SERVICE_ACCOUNT_KEY_FILE" ] || fail "Yaver Play credential is unreadable"
    docker_args+=(--volume "$YAVER_PLAY_SERVICE_ACCOUNT_KEY_FILE:/run/yaver-secrets/play.json:ro")
    docker_args+=(--env PLAY_STORE_KEY_FILE=/run/yaver-secrets/play.json)
  fi
  if [ -d "${YAVER_ANDROID_PROOT_DIR:-}" ]; then
    docker_args+=(--volume "$YAVER_ANDROID_PROOT_DIR:/run/yaver-proot:ro")
    docker_args+=(--env PROOT_SRC=/run/yaver-proot)
  fi
  if [ -r "${YAVER_ANDROIDPLAY_ENV_FILE:-}" ]; then
    docker_args+=(--volume "$YAVER_ANDROIDPLAY_ENV_FILE:/root/.androidplay/yaver.env:ro")
  fi

  docker "${docker_args[@]}" "$IMAGE" bash -lc '
    set -euo pipefail
    cd "$1"
    lock_hash=$(sha256sum mobile/package-lock.json | awk "{print \$1}")
    stamp=mobile/node_modules/.yaver-package-lock.sha256
    if [ ! -d mobile/node_modules ] || [ ! -f "$stamp" ] || [ "$(cat "$stamp")" != "$lock_hash" ]; then
      (cd mobile && npm ci --legacy-peer-deps)
      printf "%s\n" "$lock_hash" >"$stamp"
    else
      echo "Reusing lockfile-matched Yaver mobile dependencies."
    fi
    shift
    exec ./deploy/deploy.sh "$@"
  ' bash "$ROOT" "$@"
}

ensure_host
case "$ACTION" in
  ensure) ensure_image ;;
  run) ensure_image; run_release "$@" ;;
  *) fail "usage: $0 ensure | run <deploy-target> [options...]" ;;
esac
