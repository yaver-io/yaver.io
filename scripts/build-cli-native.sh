#!/usr/bin/env bash
set -euo pipefail

# build-cli-native.sh — build, sign, notarize and release the CLI ON THIS MAC.
#
# CLAUDE.md's rule is local-first: every deploy that can run on this machine
# should. CI was doing this only because nothing here did it, and CI costs
# minutes, queues behind other repos, and needs the signing material mirrored
# into GitHub secrets. A Mac with the Developer ID identity already in its
# keychain can do the whole thing in one pass.
#
# WHAT IT PRODUCES — byte-identical in shape to release-cli.yml, because
# cli/src/postinstall.js downloads these exact names with no retry:
#   yaver-darwin-arm64.tar.gz   yaver-darwin-amd64.tar.gz
#   yaver-linux-amd64.tar.gz    yaver-linux-arm64.tar.gz
#   yaver-windows-amd64.zip     yaver-windows-amd64.exe
#   checksums.txt
#
# DIFFERENCE FROM CI, deliberately: CI creates a throwaway keychain and imports
# a P12 from a secret. This box already HAS the Developer ID identity in
# yaver-ci.keychain, so it signs with that directly. Fewer moving parts, and no
# copy of the private key materialised on disk.
#
# Notarization needs the App Store Connect key — the same one TestFlight uses,
# from ~/.appstoreconnect/yaver.env. Raw Mach-O binaries are notarized inside a
# ZIP wrapper and NOT stapled: stapling does not apply to a bare executable.

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

VERSION="$(python3 -c "import json;print(json.load(open('versions.json'))['cli'])")"
PKG_VERSION="$(python3 -c "import json;print(json.load(open('cli/package.json'))['version'])")"
if [ "$VERSION" != "$PKG_VERSION" ]; then
  echo "versions.json ($VERSION) != cli/package.json ($PKG_VERSION) — refusing to build a mismatched release"
  exit 1
fi

TARGETS=(
  "darwin arm64"
  "darwin amd64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
)

OUT="$REPO_ROOT/dist/cli-$VERSION"
rm -rf "$OUT"; mkdir -p "$OUT"

echo "== Building yaver-cli $VERSION for ${#TARGETS[@]} targets =="
cd desktop/agent

# ldflags mirror the workflow so `yaver --version` matches the release.
LDFLAGS="-s -w -X main.version=$VERSION"

for t in "${TARGETS[@]}"; do
  set -- $t
  GOOS_="$1"; GOARCH_="$2"
  BIN="yaver-${GOOS_}-${GOARCH_}"
  [ "$GOOS_" = "windows" ] && BIN="${BIN}.exe"
  printf '  %-22s ' "${GOOS_}/${GOARCH_}"
  CGO_ENABLED=0 GOOS="$GOOS_" GOARCH="$GOARCH_" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$BIN" .
  echo "ok"
done

# ── sign + notarize the darwin binaries ────────────────────────────────────
# Only darwin. Gatekeeper is the reason this script cannot be a plain
# cross-compile: an unsigned binary is quarantined on first run and the user
# sees a scary dialog instead of a CLI.
set -a; [ -f "$HOME/.appstoreconnect/yaver.env" ] && source "$HOME/.appstoreconnect/yaver.env"; set +a
set -a; [ -f "$HOME/.yaver/local-secrets.env" ] && source "$HOME/.yaver/local-secrets.env"; set +a

KC="${YAVER_CI_KEYCHAIN_PATH:-$HOME/Library/Keychains/yaver-ci.keychain-db}"
if [ -n "${YAVER_CI_KEYCHAIN_PASSWORD:-}" ]; then
  security unlock-keychain -p "$YAVER_CI_KEYCHAIN_PASSWORD" "$KC" >/dev/null 2>&1 || true
  security set-keychain-settings "$KC" >/dev/null 2>&1 || true
  security set-key-partition-list -S apple-tool:,apple:,codesign: \
    -s -k "$YAVER_CI_KEYCHAIN_PASSWORD" "$KC" >/dev/null 2>&1 || true
fi

SIGN_ID="$(security find-identity -v -p codesigning "$KC" 2>/dev/null \
  | awk '/Developer ID Application/ {print $2; exit}')"
if [ -z "$SIGN_ID" ]; then
  echo "no Developer ID Application identity in $KC — cannot sign darwin binaries."
  echo "Gatekeeper would quarantine them, so this is fatal rather than a warning."
  exit 1
fi

say_notarize=1
if [ -z "${APP_STORE_KEY_PATH:-}" ] || [ ! -f "${APP_STORE_KEY_PATH:-/nonexistent}" ]; then
  echo "WARN: no App Store Connect key — signing but NOT notarizing."
  echo "      Signed-but-unnotarized binaries still warn on first run."
  say_notarize=0
fi

for arch in arm64 amd64; do
  BIN="$OUT/yaver-darwin-$arch"
  echo "== Signing darwin/$arch =="
  codesign --force --timestamp --options runtime \
    --sign "$SIGN_ID" --keychain "$KC" "$BIN"
  codesign --verify --verbose=2 "$BIN"

  if [ "$say_notarize" = "1" ]; then
    echo "== Notarizing darwin/$arch (this waits on Apple) =="
    ZIP="$OUT/.notarize-$arch.zip"
    /usr/bin/ditto -c -k --keepParent "$BIN" "$ZIP"
    xcrun notarytool submit "$ZIP" \
      --key "$APP_STORE_KEY_PATH" \
      --key-id "$APP_STORE_KEY_ID" \
      --issuer "$APP_STORE_KEY_ISSUER" \
      --wait --timeout 20m
    rm -f "$ZIP"
  fi
done

# ── package exactly as postinstall expects ─────────────────────────────────
echo "== Packaging =="
cd "$OUT"
for t in "${TARGETS[@]}"; do
  set -- $t
  GOOS_="$1"; GOARCH_="$2"
  if [ "$GOOS_" = "windows" ]; then
    zip -q "yaver-windows-${GOARCH_}.zip" "yaver-windows-${GOARCH_}.exe"
  else
    cp "yaver-${GOOS_}-${GOARCH_}" yaver
    tar czf "yaver-${GOOS_}-${GOARCH_}.tar.gz" yaver
    rm -f yaver "yaver-${GOOS_}-${GOARCH_}"
  fi
done
shasum -a 256 * > checksums.txt
ls -la

echo
echo "== Artifacts ready in $OUT =="
echo "Create the release with:"
echo "  gh release create v$VERSION --title \"Yaver CLI v$VERSION\" --generate-notes $OUT/*"
