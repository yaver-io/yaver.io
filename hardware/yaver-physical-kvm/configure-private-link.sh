#!/usr/bin/env bash
# Factory-only helper: create a private Pi Wi-Fi link for the AtomS3U.
# The Pi must use Ethernet (or another adapter) for upstream connectivity while
# this connection owns the selected Wi-Fi interface.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run with sudo" >&2; exit 1; }
[ $# -eq 2 ] && [ "$1" = "--interface" ] || {
  echo "usage: sudo $0 --interface <wifi-interface>" >&2
  exit 2
}
WIFI_INTERFACE="$2"
case "$WIFI_INTERFACE" in
  *[!A-Za-z0-9_.:-]*|'') echo "invalid Wi-Fi interface" >&2; exit 2 ;;
esac
command -v nmcli >/dev/null || {
  echo "NetworkManager/nmcli is required on the factory Pi image" >&2
  exit 1
}
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }

CONNECTION_NAME="yaver-kvm-private-link"
if nmcli -g NAME connection show | grep -Fxq "$CONNECTION_NAME"; then
  echo "$CONNECTION_NAME already exists; refusing to overwrite its credential" >&2
  exit 1
fi
id yaver >/dev/null 2>&1 || {
  echo "install-pi.sh must create the yaver service account first" >&2
  exit 1
}
install -d -o yaver -g yaver -m 0700 /var/lib/yaver/.yaver
CREDENTIAL_FILE=/var/lib/yaver/.yaver/private-m5-link.json
NM_PROFILE=/etc/NetworkManager/system-connections/yaver-kvm-private-link.nmconnection
if [ -e "$CREDENTIAL_FILE" ] || [ -e "$NM_PROFILE" ]; then
  echo "private-link credential or NetworkManager profile already exists; refusing to overwrite it" >&2
  exit 1
fi

MACHINE_SUFFIX="$(tr -cd 'A-Fa-f0-9' </etc/machine-id | tail -c 7)"
[ -n "$MACHINE_SUFFIX" ] || { echo "machine identity is unavailable" >&2; exit 1; }
LINK_SSID="Yaver-KVM-${MACHINE_SUFFIX}"
LINK_PASSWORD="$(openssl rand -hex 16)"
LINK_UUID="$(tr -d '[:space:]' </proc/sys/kernel/random/uuid)"
[ -n "$LINK_UUID" ] || { echo "could not generate NetworkManager profile UUID" >&2; exit 1; }

umask 077
printf '%s\n' \
  '[connection]' \
  "id=$CONNECTION_NAME" \
  "uuid=$LINK_UUID" \
  'type=wifi' \
  "interface-name=$WIFI_INTERFACE" \
  'autoconnect=true' \
  '' \
  '[wifi]' \
  'mode=ap' \
  'band=bg' \
  "ssid=$LINK_SSID" \
  '' \
  '[wifi-security]' \
  'key-mgmt=wpa-psk' \
  "psk=$LINK_PASSWORD" \
  '' \
  '[ipv4]' \
  'method=shared' \
  '' \
  '[ipv6]' \
  'method=disabled' >"$NM_PROFILE"
chmod 0600 "$NM_PROFILE"
printf '{"ssid":"%s","password":"%s"}\n' "$LINK_SSID" "$LINK_PASSWORD" >"$CREDENTIAL_FILE"
chown yaver:yaver "$CREDENTIAL_FILE"
chmod 0600 "$CREDENTIAL_FILE"
nmcli connection load "$NM_PROFILE" >/dev/null
nmcli connection up "$CONNECTION_NAME"

echo "Private AtomS3U Wi-Fi is active on $WIFI_INTERFACE."
echo "Credentials were written owner-only to $CREDENTIAL_FILE; this command does not print them."
echo "Use that file in the factory commissioning station, then press AtomS3U and run yaver kvm pair --auto."
