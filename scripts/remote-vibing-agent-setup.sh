#!/usr/bin/env bash
set -euo pipefail

REPO="${REPO:-/opt/yaver}"
TOKEN="${YAVER_REMOTE_SMOKE_TOKEN:-ci-remote-vibing-token}"
HOME_DIR=/root
CONFIG_DIR="$HOME_DIR/.yaver"
WORK_DIR=/tmp/yaver-ci-workdir

log() { printf '\n== %s ==\n' "$*"; }

log "build yaver from synced source"
cd "$REPO/desktop/agent"
export PATH="/usr/local/go/bin:$PATH"
go version
go build -o /tmp/yaver-new .
install -m 0755 /tmp/yaver-new /usr/local/bin/yaver
rm -f /tmp/yaver-new
yaver --version 2>&1 | head -1

log "write local smoke config"
install -d -m 0700 "$CONFIG_DIR" "$WORK_DIR"
python3 - "$CONFIG_DIR/config.json" "$TOKEN" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
token = sys.argv[2]
cfg = {}
if path.exists():
    try:
        cfg = json.loads(path.read_text())
    except Exception:
        cfg = {}
cfg.update({
    "auth_token": token,
    "auto_update": False,
    "headless_keep_awake": False,
    "host_share": {"prepare_prompt_done": True},
})
path.write_text(json.dumps(cfg, indent=2) + "\n")
path.chmod(0o600)
PY

log "install smoke systemd service"
cat > /etc/systemd/system/yaver-agent.service <<EOF
[Unit]
Description=Yaver Agent Remote Vibing Smoke
After=network.target

[Service]
Type=simple
User=root
Environment=HOME=/root
Environment=YAVER_SKIP_AUTO_START=1
Environment=YAVER_VAULT_SKIP_KEYCHAIN=1
Environment=YAVER_DISABLE_WIZARD_AUTOINIT=1
Environment=YAVER_VAULT_AUTO_RESET=1
ExecStart=/usr/local/bin/yaver serve --debug --port 18080 --work-dir $WORK_DIR --no-relay
Restart=always
RestartSec=2
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now yaver-agent.service
systemctl restart yaver-agent.service

log "wait for health"
for i in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:18080/health >/tmp/yaver-health.json 2>/tmp/yaver-health.err; then
    cat /tmp/yaver-health.json
    echo
    exit 0
  fi
  sleep 1
done

echo "agent did not become healthy" >&2
systemctl status --no-pager yaver-agent.service | tail -40 >&2 || true
journalctl -u yaver-agent.service --no-pager -n 120 >&2 || true
exit 1
