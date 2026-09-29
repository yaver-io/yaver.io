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
  # darwin REQUIRES cgo: ghost's macOS screen/input/AX tree are tagged
  # `darwin && cgo`. With CGO_ENABLED=0 the released Mac agent silently
  # ships the unsupported stubs, so every desktop feature (Remote Desktop,
  # ghost_*, desktop_voice, desktop-screen) is dead while every doc says it
  # works. This script runs on a Mac, so cgo is native — no cross-toolchain.
  # Linux/Windows stay CGO_ENABLED=0 (their ghost is pure Go).
  CGO_="0"; [ "$GOOS_" = "darwin" ] && CGO_="1"
  printf '  %-22s ' "${GOOS_}/${GOARCH_} (cgo=$CGO_)"
  CGO_ENABLED="$CGO_" GOOS="$GOOS_" GOARCH="$GOARCH_" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$BIN" .
  echo "ok"
done

# ── sign + notarize the darwin binaries ────────────────────────────────────
# Only darwin. Gatekeeper is the reason this script cannot be a plain
# cross-compile: an unsigned binary is quarantined on first run and the user
# sees a scary dialog instead of a CLI.
set -a; [ -f "$HOME/.appstoreconnect/yaver.env" ] && source "$HOME/.appstoreconnect/yaver.env"; set +a
set -a; [ -f "$HOME/.yaver/local-secrets.env" ] && source "$HOME/.yaver/local-secrets.env"; set +a

# WHERE THE DEVELOPER ID LIVES IS NOT A CONSTANT (2026-08-02).
#
# This looked in yaver-ci.keychain ONLY, and died with "no Developer ID
# Application identity" on a Mac that had one — in login.keychain. Two Apple
# identities are in play and they do not live together: TestFlight signs with
# Apple DISTRIBUTION (yaver-ci.keychain here), while the CLI signs with
# Developer ID APPLICATION, which is a login-keychain cert on this box. A
# script that knows only one location reports "you have no certificate" when
# the truth is "I looked in one place" — the inventory-vs-operation error, in
# the failure message itself.
#
# So: try the CI keychain, then the user's default search list (which includes
# login.keychain), and SAY which one answered. Unlocking is best-effort per
# keychain — a locked login keychain in an SSH session is exactly what
# YAVER_LOGIN_PASSWORD in ~/.yaver/local-secrets.env exists for.
KC_CI="${YAVER_CI_KEYCHAIN_PATH:-$HOME/Library/Keychains/yaver-ci.keychain-db}"
KC_LOGIN="${YAVER_LOGIN_KEYCHAIN_PATH:-$HOME/Library/Keychains/login.keychain-db}"

unlock_keychain() {  # $1 = keychain path, $2 = password (may be empty)
  [ -n "${2:-}" ] || return 0
  [ -e "$1" ] || return 0
  security unlock-keychain -p "$2" "$1" >/dev/null 2>&1 || true
  security set-keychain-settings "$1" >/dev/null 2>&1 || true
  security set-key-partition-list -S apple-tool:,apple:,codesign: \
    -s -k "$2" "$1" >/dev/null 2>&1 || true
}
unlock_keychain "$KC_CI"    "${YAVER_CI_KEYCHAIN_PASSWORD:-}"
unlock_keychain "$KC_LOGIN" "${YAVER_LOGIN_PASSWORD:-}"

find_developer_id() {  # $1 = keychain path, or empty for the default search list
  if [ -n "${1:-}" ]; then
    security find-identity -v -p codesigning "$1" 2>/dev/null \
      | awk '/Developer ID Application/ {print $2; exit}'
  else
    security find-identity -v -p codesigning 2>/dev/null \
      | awk '/Developer ID Application/ {print $2; exit}'
  fi
}

SIGN_ID=""; KC=""
for candidate in "$KC_CI" "$KC_LOGIN" ""; do
  [ -n "$candidate" ] && [ ! -e "$candidate" ] && continue
  found="$(find_developer_id "$candidate")"
  if [ -n "$found" ]; then
    SIGN_ID="$found"
    KC="$candidate"
    echo "Signing identity $SIGN_ID from ${candidate:-the default keychain search list}"
    break
  fi
done

if [ -z "$SIGN_ID" ]; then
  echo "No 'Developer ID Application' identity found in any of:" >&2
  echo "  $KC_CI" >&2
  echo "  $KC_LOGIN" >&2
  echo "  the default keychain search list" >&2
  echo >&2
  echo "This is the cert that signs the CLI binaries — NOT the Apple" >&2
  echo "Distribution cert TestFlight uses, so a working TestFlight deploy" >&2
  echo "does not imply this one exists. Check with:" >&2
  echo "  security find-identity -v -p codesigning | grep 'Developer ID'" >&2
  echo "If it lists one, the keychain holding it is locked: set" >&2
  echo "YAVER_LOGIN_PASSWORD / YAVER_CI_KEYCHAIN_PASSWORD in" >&2
  echo "~/.yaver/local-secrets.env (0600) and re-run." >&2
  echo >&2
  echo "Gatekeeper would quarantine unsigned binaries, so this is fatal." >&2
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
  # --keychain only when we resolved a specific one; with the default search
  # list an empty --keychain argument would make codesign fail on a path of "".
  if [ -n "$KC" ]; then
    codesign --force --timestamp --options runtime \
      --sign "$SIGN_ID" --keychain "$KC" "$BIN"
  else
    codesign --force --timestamp --options runtime \
      --sign "$SIGN_ID" "$BIN"
  fi
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
echo "Finish the release with, IN THIS ORDER:"
echo
echo "  1. gh release create v$VERSION --title \"Yaver CLI v$VERSION\" --generate-notes $OUT/*"
echo "  2. (cd cli && npm publish)"
echo "  3. yaver announce-release latest"
echo
# ORDER IS LOAD-BEARING, and step 3 is not optional.
#
# 1 before 2: cli/src/postinstall.js downloads the platform tarballs from the
# GitHub release with NO RETRY, so a package live on npm before its release
# exists is a hard-failing install for every user in that window.
#
# 3 at all: without it, owned boxes wait out the 1-2h auto-update cycle and the
# real update mechanism is the maintainer ssh-ing in. That happened three times
# on 2026-08-03 alone — the box sat on 1.99.397 while the fix was in 1.99.399,
# then on 1.99.398 while 1.99.400 shipped. `announce-release` sets desired state
# on every owned device; each box claims it on its next heartbeat (~30s) and
# decides for itself WHEN to apply, so a running coding turn is not killed.
echo "  Step 3 is not optional: without it, boxes wait out the 1-2h auto-update"
echo "  cycle and the real update mechanism is you, over ssh."
