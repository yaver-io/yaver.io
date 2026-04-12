#!/usr/bin/env bash
# dev.sh — start every surface in parallel.
set -euo pipefail
cd "$(dirname "$0")/.."

pids=()
(cd backend && npx convex dev) & pids+=($!)
(cd apps/mobile && npx expo start) & pids+=($!)
trap 'kill "${pids[@]}" 2>/dev/null || true' EXIT
wait
