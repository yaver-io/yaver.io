#!/usr/bin/env bash
set -euo pipefail

HOST="${HOST:-root@***REMOVED***}"
WATCHDOG_NAME="${WATCHDOG_NAME:-public-relay}"
UNIT_BASENAME="yaver-${WATCHDOG_NAME}-watchdog"
REMOTE_SCRIPT="/usr/local/bin/${UNIT_BASENAME}"
REMOTE_ENV="/etc/${UNIT_BASENAME}.env"
REMOTE_SERVICE="/etc/systemd/system/${UNIT_BASENAME}.service"
REMOTE_TIMER="/etc/systemd/system/${UNIT_BASENAME}.timer"
ALERT_TO="${ALERT_TO:-kivanccakmak@gmail.com}"
ALERT_FROM="${ALERT_FROM:-Kivanc from Yaver <kivanc@yaver.io>}"
HEALTH_URL="${HEALTH_URL:-https://public.yaver.io/health}"
DESCRIPTION="${DESCRIPTION:-Yaver ${WATCHDOG_NAME} watchdog}"
INTERVAL="${INTERVAL:-2min}"
CHECK_LABEL="${CHECK_LABEL:-Yaver ${WATCHDOG_NAME}}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

quote_env() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

scp -o StrictHostKeyChecking=no "${SCRIPT_DIR}/public-relay-watchdog.sh" "${HOST}:${REMOTE_SCRIPT}"

ssh -o StrictHostKeyChecking=no "$HOST" \
  "chmod 0755 ${REMOTE_SCRIPT} && \
   install -d -m 0755 /var/lib/${UNIT_BASENAME} && \
   printf '%s\n' \
     'ALERT_TO=\"$(quote_env "$ALERT_TO")\"' \
     'ALERT_FROM=\"$(quote_env "$ALERT_FROM")\"' \
     'HEALTH_URL=\"$(quote_env "$HEALTH_URL")\"' \
     'CHECK_LABEL=\"$(quote_env "$CHECK_LABEL")\"' \
     'STATE_DIR=/var/lib/${UNIT_BASENAME}' \
     > ${REMOTE_ENV} && \
   chmod 0600 ${REMOTE_ENV} && \
   printf '%s\n' \
     '[Unit]' \
     'Description=${DESCRIPTION}' \
     'After=network-online.target' \
     'Wants=network-online.target' \
     '' \
     '[Service]' \
     'Type=oneshot' \
     'EnvironmentFile=/etc/talos-agent/talos-logo-sync.env' \
     'EnvironmentFile=${REMOTE_ENV}' \
     'ExecStart=${REMOTE_SCRIPT}' \
     > ${REMOTE_SERVICE} && \
   printf '%s\n' \
     '[Unit]' \
     'Description=Run ${DESCRIPTION} every ${INTERVAL}' \
     '' \
     '[Timer]' \
     'OnBootSec=2min' \
     'OnUnitActiveSec=${INTERVAL}' \
     'AccuracySec=30s' \
     'Unit=${UNIT_BASENAME}.service' \
     '' \
     '[Install]' \
     'WantedBy=timers.target' \
     > ${REMOTE_TIMER} && \
   systemctl daemon-reload && \
   systemctl enable --now ${UNIT_BASENAME}.timer && \
   systemctl start ${UNIT_BASENAME}.service"
