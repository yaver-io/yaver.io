#!/usr/bin/env bash
# Build and certificate-pin the Windows Store EXE candidate on the release Mac.
# This does not publish, tag, upload, or mutate Partner Center.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$(uname -s)" == Darwin ]] || { echo "This SimplySign PKCS#11 lane requires macOS." >&2; exit 2; }
: "${YAVER_WINDOWS_CERT_ALIAS:?Set the public 32-hex SimplySign certificate alias}"
: "${YAVER_WINDOWS_CERT_SHA1:?Set the public 40-hex certificate SHA-1 thumbprint}"
[[ "$YAVER_WINDOWS_CERT_ALIAS" =~ ^[A-Fa-f0-9]{32}$ ]] || { echo "Invalid certificate alias." >&2; exit 2; }
normalized_sha="$(printf '%s' "$YAVER_WINDOWS_CERT_SHA1" | tr -d '[:space:]:' | tr '[:lower:]' '[:upper:]')"
[[ "$normalized_sha" =~ ^[A-F0-9]{40}$ ]] || { echo "Invalid certificate thumbprint." >&2; exit 2; }
command -v osslsigncode >/dev/null 2>&1 || { echo "Install osslsigncode before building." >&2; exit 2; }

# Probe the operation before spending time compiling. SimplySign Desktop can
# be running while its virtual card is logged out, in which case the PKCS#11
# library exposes zero slots and Jsign otherwise waits roughly two minutes
# before returning a deeply nested provider error.
if command -v pkcs11-tool >/dev/null 2>&1; then
  slot_output="$(pkcs11-tool --module /usr/local/lib/libSimplySignPKCS.dylib --list-slots 2>&1)" || {
    echo "SimplySign PKCS#11 preflight failed:" >&2
    printf '%s\n' "$slot_output" >&2
    echo "Open SimplySign Desktop, activate the virtual card, and retry." >&2
    exit 2
  }
  if printf '%s\n' "$slot_output" | grep -Eq '^[[:space:]]*No slots\.'; then
    echo "SimplySign Desktop has no active virtual-card session." >&2
    echo "Open SimplySign Desktop, activate the virtual card, and retry." >&2
    exit 2
  fi
fi

gui_version="$(node -p "require('$root/versions.json').gui")"
cli_version="$(node -p "require('$root/versions.json').cli")"
package_version="$(node -p "require('$root/electron/package.json').version")"
[[ "$gui_version" == "$package_version" ]] || { echo "GUI version mismatch: versions.json=$gui_version package=$package_version" >&2; exit 2; }

export YAVER_WINDOWS_CERT_SHA1="$normalized_sha"
export YAVER_JSIGN_JAR="${YAVER_JSIGN_JAR:-/tmp/yaver-jsign-7.5.jar}"
export PATH="/opt/homebrew/bin:$PATH"

mkdir -p "$root/electron/resources/bin"
(cd "$root/desktop/agent" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-s -w -X main.version=$cli_version" -o "$root/electron/resources/bin/yaver.exe" .)
(cd "$root/electron" && bash scripts/prepare-jsign.sh)
(cd "$root/electron" && node -e "require('./sign-windows-hook.js').default({path:require('path').resolve('resources/bin/yaver.exe')})")
osslsigncode verify -require-leaf-hash "sha1:$normalized_sha" -in "$root/electron/resources/bin/yaver.exe" >/dev/null

(cd "$root/electron" && npm test && npm run dist:win)
installer="$root/electron/dist/yaver-gui-${gui_version}-win-x64-setup.exe"
[[ -f "$installer" ]] || { echo "Expected installer was not produced: $installer" >&2; exit 1; }
osslsigncode verify -require-leaf-hash "sha1:$normalized_sha" -in "$installer" >/dev/null
sha256="$(shasum -a 256 "$installer" | awk '{print $1}')"
size="$(stat -f '%z' "$installer")"
printf 'Signed Windows Store candidate\nPath: %s\nSize: %s\nSHA-256: %s\n' "$installer" "$size" "$sha256"
printf 'Next: run electron/store/windows-store-preflight.ps1 on a clean Windows 11 x64 VM.\n'
