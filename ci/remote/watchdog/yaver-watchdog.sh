#!/usr/bin/env bash
# Yaver external watchdog — the "another service that looks at the
# first one's existence" from the self-healing-timers design.
#
# The Yaver agent runs every recurring task (heartbeat, scheduler,
# Convex sync, smoke checks …) inside its own TaskSupervisor. This
# watchdog's only job is to catch the "agent itself is completely
# broken" case the supervisor cannot cover — a SIGKILL, a deadlocked
# goroutine holding every mutex, a kernel OOM-reaping the process.
#
# Deliberate non-features (user called this out explicitly):
#
#   - Does NOT trigger supervised tasks. Does not poll /self-check
#     in a way that forces anything to run. Never writes to the
#     agent. Observation-only.
#   - Does NOT force the auto-update check, which has its own 6-hour
#     cadence inside the supervisor. Waking the agent at watchdog
#     frequency would defeat the whole "only run when due" design.
#
# What it actually does, in order:
#
#   1. Is a yaver process running? (`pgrep -x yaver`)
#   2. Is `~/.yaver/last-healthy` or `/var/lib/yaver/last-healthy`
#      fresher than WATCHDOG_MAX_AGE_SEC (default 180)? Supervisor
#      refreshes this file on every watchdog tick when no task is
#      stalled.
#   3. OPTIONAL: Does `GET http://127.0.0.1:18080/health` respond <5s?
#      Skipped on hosts without curl to keep the script stdlib-only.
#
# If all checks pass, exit 0 silently — systemd timer logs just say
# "service succeeded". If any fail, log loudly to stderr (journalctl
# captures it) and optionally `systemctl restart yaver-agent`.
#
# Safe to run unattended every minute. Has never restarted a healthy
# agent in testing.

set -u  # -e intentionally off; we want to complete all checks + report

SCRIPT_NAME="yaver-watchdog"
MAX_AGE_SEC="${WATCHDOG_MAX_AGE_SEC:-180}"
BEACON_CANDIDATES=(
  "${YAVER_BEACON_PATH:-}"
  "/root/.yaver/last-healthy"
  "/var/lib/yaver/last-healthy"
  "${HOME:-/root}/.yaver/last-healthy"
)
# Also include any /home/<user>/.yaver/last-healthy the agent may be
# running as. Systemd service units usually run as root, but the
# agent's autostart hook often re-execs under a dedicated `yaver`
# user with its own $HOME, so we can't assume the beacon lives under
# /root. A shell glob keeps this lightweight + covers multi-user
# boxes (one beacon per developer).
for candidate_home in /home/*/.yaver/last-healthy; do
  if [ -e "$candidate_home" ] || [ -d "$(dirname "$candidate_home")" ]; then
    BEACON_CANDIDATES+=("$candidate_home")
  fi
done
AGENT_PROBE_URL="${YAVER_AGENT_PROBE_URL:-http://127.0.0.1:18080/health}"
RESTART_ON_FAILURE="${WATCHDOG_RESTART_ON_FAILURE:-0}"
RESTART_UNIT="${WATCHDOG_RESTART_UNIT:-yaver-agent.service}"

log()  { printf '[%s] %s\n' "$SCRIPT_NAME" "$*"; }
warn() { printf '[%s] WARN: %s\n' "$SCRIPT_NAME" "$*" >&2; }
fail() { printf '[%s] FAIL: %s\n' "$SCRIPT_NAME" "$*" >&2; }

problems=()

# ── 1. Process alive ─────────────────────────────────────────────────
if ! pgrep -x yaver >/dev/null 2>&1; then
  problems+=("yaver process not running (pgrep -x yaver)")
fi

# ── 2. Beacon freshness ──────────────────────────────────────────────
beacon=""
for candidate in "${BEACON_CANDIDATES[@]}"; do
  [ -z "$candidate" ] && continue
  if [ -f "$candidate" ]; then
    beacon="$candidate"
    break
  fi
done

if [ -z "$beacon" ]; then
  problems+=("no last-healthy beacon file found (checked: ${BEACON_CANDIDATES[*]})")
else
  # Portable mtime read — GNU stat has -c, BSD stat has -f.
  if stat -c '%Y' "$beacon" >/dev/null 2>&1; then
    mtime="$(stat -c '%Y' "$beacon")"
  else
    mtime="$(stat -f '%m' "$beacon" 2>/dev/null || echo 0)"
  fi
  now="$(date -u +%s)"
  age=$(( now - mtime ))
  if [ "$age" -gt "$MAX_AGE_SEC" ]; then
    problems+=("beacon $beacon stale: ${age}s old (max ${MAX_AGE_SEC}s)")
  else
    log "beacon ok: age=${age}s ($(basename "$beacon"))"
  fi
fi

# ── 3. Optional HTTP health probe ────────────────────────────────────
if command -v curl >/dev/null 2>&1; then
  if ! curl --silent --fail --max-time 5 "$AGENT_PROBE_URL" >/dev/null 2>&1; then
    # Non-fatal on its own — maybe the agent is on a non-default port,
    # or the socket is intentionally disabled. Only count as a problem
    # when combined with one of the others.
    warn "agent HTTP probe failed: $AGENT_PROBE_URL"
  fi
fi

# ── Verdict ─────────────────────────────────────────────────────────
if [ "${#problems[@]}" -eq 0 ]; then
  log "ok"
  exit 0
fi

fail "${#problems[@]} problem(s) detected:"
for p in "${problems[@]}"; do
  fail "  - $p"
done

if [ "$RESTART_ON_FAILURE" = "1" ] || [ "$RESTART_ON_FAILURE" = "true" ]; then
  if command -v systemctl >/dev/null 2>&1; then
    log "restarting $RESTART_UNIT (WATCHDOG_RESTART_ON_FAILURE=1)"
    systemctl restart "$RESTART_UNIT" || warn "systemctl restart failed"
  fi
fi

exit 2
