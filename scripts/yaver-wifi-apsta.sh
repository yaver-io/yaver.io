#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Run a Yaver-managed Wi-Fi AP or AP+STA repeater through a root agent.

Usage:
  sudo -E scripts/yaver-wifi-apsta.sh start --interface wlan0 --ssid kivanc --password 12345678 --upstream-ssid HomeWiFi --upstream-pass "$HOME_WIFI_PASSWORD"
  sudo -E scripts/yaver-wifi-apsta.sh status
  sudo -E scripts/yaver-wifi-apsta.sh stop

Environment overrides:
  YAVER_BIN             yaver binary path
  YAVER_WIFI_PORT       local root-agent HTTP port (default: 18081)
  YAVER_WIFI_WORK_DIR   state/work dir (default: current directory)
  YAVER_WIFI_SSID       AP SSID
  YAVER_WIFI_PASSWORD   AP password
  YAVER_WIFI_INTERFACE  STA Wi-Fi interface, e.g. wlan0
  YAVER_WIFI_AP_IFACE   AP virtual interface, e.g. wlan0ap
  YAVER_WIFI_UPSTREAM_IF uplink interface for NAT (default: same as interface in APSTA)
  YAVER_WIFI_UPSTREAM_SSID upstream Wi-Fi SSID for APSTA
  YAVER_WIFI_UPSTREAM_PASS upstream Wi-Fi password for APSTA
EOF
}

if [[ $# -lt 1 ]]; then
  usage
  exit 2
fi

cmd="$1"
shift

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "APSTA requires Linux hostapd/nl80211 support. This script cannot start APSTA on $(uname -s)." >&2
  exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run with sudo so the agent can configure hostapd, DHCP, and NAT." >&2
  exit 1
fi

owner_home="${YAVER_OWNER_HOME:-}"
if [[ -z "${owner_home}" ]]; then
  if [[ -n "${SUDO_USER:-}" && "${SUDO_USER}" != "root" ]]; then
    owner_home="$(getent passwd "${SUDO_USER}" | cut -d: -f6)"
  else
    owner_home="${HOME}"
  fi
fi

port="${YAVER_WIFI_PORT:-18081}"
work_dir="${YAVER_WIFI_WORK_DIR:-$(pwd)}"
pid_file="${YAVER_WIFI_PID_FILE:-/tmp/yaver-wifi-root-agent-${port}.pid}"
log_file="${YAVER_WIFI_LOG_FILE:-/tmp/yaver-wifi-root-agent-${port}.log}"
ssid="${YAVER_WIFI_SSID:-kivanc}"
password="${YAVER_WIFI_PASSWORD:-}"
mode="${YAVER_WIFI_MODE:-apsta}"
iface="${YAVER_WIFI_INTERFACE:-}"
ap_iface="${YAVER_WIFI_AP_IFACE:-}"
upstream_if="${YAVER_WIFI_UPSTREAM_IF:-}"
upstream_ssid="${YAVER_WIFI_UPSTREAM_SSID:-}"
upstream_pass="${YAVER_WIFI_UPSTREAM_PASS:-}"
channel="${YAVER_WIFI_CHANNEL:-6}"
country="${YAVER_WIFI_COUNTRY:-US}"
frequency="${YAVER_WIFI_FREQUENCY:-2.4GHz}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ssid) ssid="$2"; shift 2 ;;
    --password) password="$2"; shift 2 ;;
    --mode) mode="$2"; shift 2 ;;
    --interface) iface="$2"; shift 2 ;;
    --ap-interface) ap_iface="$2"; shift 2 ;;
    --upstream-if) upstream_if="$2"; shift 2 ;;
    --upstream-ssid) upstream_ssid="$2"; shift 2 ;;
    --upstream-pass) upstream_pass="$2"; shift 2 ;;
    --channel) channel="$2"; shift 2 ;;
    --country) country="$2"; shift 2 ;;
    --frequency) frequency="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) platform="linux-amd64" ;;
  aarch64|arm64) platform="linux-arm64" ;;
  *) platform="" ;;
esac

yaver_bin="${YAVER_BIN:-}"
if [[ -z "${yaver_bin}" ]]; then
  if command -v yaver >/dev/null 2>&1; then
    yaver_bin="$(command -v yaver)"
  elif [[ -n "${platform}" && -x "${owner_home}/.yaver/bin/current/${platform}/yaver" ]]; then
    yaver_bin="${owner_home}/.yaver/bin/current/${platform}/yaver"
  else
    echo "Could not find yaver. Set YAVER_BIN=/path/to/yaver." >&2
    exit 1
  fi
fi

config_file="${owner_home}/.yaver/config.json"
if [[ ! -f "${config_file}" ]]; then
  echo "Missing ${config_file}. Run yaver auth as the normal user first." >&2
  exit 1
fi

token="$(CONFIG_FILE="${config_file}" python3 - <<'PY'
import json, os
with open(os.environ["CONFIG_FILE"], "r", encoding="utf-8") as f:
    cfg = json.load(f)
print(cfg.get("auth_token") or "")
PY
)"
if [[ -z "${token}" ]]; then
  echo "No auth_token in ${config_file}. Run yaver auth as the normal user first." >&2
  exit 1
fi

agent_up() {
  curl -fsS "http://127.0.0.1:${port}/health" >/dev/null 2>&1
}

ensure_agent() {
  if agent_up; then
    return
  fi
  HOME="${owner_home}" "${yaver_bin}" serve \
    --debug \
    --port "${port}" \
    --no-quic \
    --no-relay \
    --no-tls \
    --work-dir "${work_dir}" \
    >"${log_file}" 2>&1 &
  echo "$!" >"${pid_file}"
  for _ in $(seq 1 40); do
    if agent_up; then
      return
    fi
    sleep 0.25
  done
  echo "Root Yaver agent did not become ready. Log: ${log_file}" >&2
  exit 1
}

post_json() {
  local path="$1"
  local body="$2"
  curl -fsS \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" \
    -d "${body}" \
    "http://127.0.0.1:${port}${path}"
}

get_json() {
  local path="$1"
  curl -fsS \
    -H "Authorization: Bearer ${token}" \
    "http://127.0.0.1:${port}${path}"
}

case "${cmd}" in
  start)
    if [[ "${mode}" != "ap" && "${mode}" != "apsta" ]]; then
      echo "--mode must be ap or apsta" >&2
      exit 2
    fi
    if [[ -z "${iface}" ]]; then
      echo "--interface is required, e.g. --interface wlan0" >&2
      exit 2
    fi
    if [[ -z "${password}" ]]; then
      echo "--password or YAVER_WIFI_PASSWORD is required" >&2
      exit 2
    fi
    if [[ "${mode}" == "apsta" && ( -z "${upstream_ssid}" || -z "${upstream_pass}" ) ]]; then
      echo "APSTA requires --upstream-ssid and --upstream-pass." >&2
      exit 2
    fi
    ensure_agent
    payload="$(SSID="${ssid}" PASSWORD="${password}" MODE="${mode}" IFACE="${iface}" AP_IFACE="${ap_iface}" UPSTREAM_IF="${upstream_if}" UPSTREAM_SSID="${upstream_ssid}" UPSTREAM_PASS="${upstream_pass}" CHANNEL="${channel}" COUNTRY="${country}" FREQUENCY="${frequency}" python3 - <<'PY'
import json, os
payload = {
    "ssid": os.environ["SSID"],
    "password": os.environ["PASSWORD"],
    "mode": os.environ["MODE"],
    "interface": os.environ["IFACE"],
    "channel": int(os.environ["CHANNEL"]),
    "countryCode": os.environ["COUNTRY"],
    "frequency": os.environ["FREQUENCY"],
    "enableDhcp": True,
    "enableNat": True,
}
for env, key in [
    ("AP_IFACE", "apInterface"),
    ("UPSTREAM_IF", "upstreamIf"),
    ("UPSTREAM_SSID", "upstreamSsid"),
    ("UPSTREAM_PASS", "upstreamPass"),
]:
    value = os.environ.get(env, "")
    if value:
        payload[key] = value
print(json.dumps(payload))
PY
)"
    if [[ "${mode}" == "apsta" ]]; then
      post_json "/console/wifi/apsta-config" "${payload}" >/dev/null
    fi
    post_json "/console/wifi/start" "${payload}"
    echo
    echo "Root agent log: ${log_file}"
    ;;
  status)
    ensure_agent
    get_json "/console/wifi/status"
    echo
    ;;
  stop)
    ensure_agent
    post_json "/console/wifi/stop" "{}"
    echo
    if [[ -f "${pid_file}" ]]; then
      agent_pid="$(cat "${pid_file}")"
      if [[ -n "${agent_pid}" ]]; then
        kill "${agent_pid}" >/dev/null 2>&1 || true
      fi
      rm -f "${pid_file}"
    fi
    ;;
  *)
    echo "unknown command: ${cmd}" >&2
    usage
    exit 2
    ;;
esac
