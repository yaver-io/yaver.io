#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_NAME="${MCP_SERVER_NAME:-io.github.kivanccakmak/yaver}"
NPM_PACKAGE="${MCP_NPM_PACKAGE:-yaver-cli}"
MODE="dry-run"

usage() {
  cat <<'USAGE'
Usage:
  scripts/publish-mcp-registries.sh [--dry-run] [--official] [--all] [--version <version>]

Publishes or prepares Yaver MCP registry metadata.

Modes:
  --dry-run   Validate metadata and print the exact publish actions. Default.
  --official  Publish server.json to the official MCP Registry with mcp-publisher.
              Requires prior `mcp-publisher login github` or CI GitHub OIDC login.
  --all       Run official publish, then run `npx mcp-submit` if available.

Environment:
  MCP_VERSION       Override npm latest as the version to write into server.json.
  MCP_SERVER_NAME   Default: io.github.kivanccakmak/yaver
  MCP_NPM_PACKAGE   Default: yaver-cli
USAGE
}

version="${MCP_VERSION:-}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) MODE="dry-run"; shift ;;
    --official) MODE="official"; shift ;;
    --all) MODE="all"; shift ;;
    --version) version="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

need jq
need npm
need curl

if [[ -z "$version" ]]; then
  version="$(npm view "$NPM_PACKAGE" version)"
fi
if [[ -z "$version" || "$version" == "undefined" ]]; then
  echo "could not resolve npm version for $NPM_PACKAGE" >&2
  exit 1
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
jq --arg v "$version" --arg name "$SERVER_NAME" --arg pkg "$NPM_PACKAGE" '
  .name = $name
  | .version = $v
  | (.packages[] | select(.registryType == "npm" and .identifier == $pkg) | .version) = $v
' "$ROOT/server.json" > "$tmp"

echo "Prepared server.json for $SERVER_NAME@$version"
jq '{name,title,version,description,packages:[.packages[] | {registryType,identifier,version,runtimeHint,transport}]}' "$tmp"

if [[ "$MODE" != "dry-run" ]]; then
  cp "$tmp" "$ROOT/server.json"
  "$ROOT/scripts/sync-mcp-discovery-files.sh" >/dev/null
  echo "Synced web/public MCP discovery files"
else
  echo "Dry run only: would sync web/public MCP discovery files after publishing metadata"
fi

echo
echo "Checking npm propagation..."
curl -fsSL "https://registry.npmjs.org/${NPM_PACKAGE}/${version}" >/dev/null
echo "npm package is visible: ${NPM_PACKAGE}@${version}"

case "$MODE" in
  dry-run)
    echo
    echo "Dry run only. To publish official registry metadata:"
    echo "  MCP_VERSION='$version' scripts/publish-mcp-registries.sh --official"
    echo
    echo "For broad directory submission after official publish:"
    echo "  npx -y mcp-submit --status"
    echo "  npx -y mcp-submit --yes"
    ;;
  official|all)
    need mcp-publisher
    publish_log="$(mktemp)"
    if (cd "$ROOT" && mcp-publisher publish 2>&1 | tee "$publish_log"); then
      echo "official MCP Registry publish completed"
    elif grep -qi 'cannot publish duplicate version' "$publish_log"; then
      echo "official MCP Registry already has $SERVER_NAME@$version; verifying it is active..."
      if (cd "$ROOT" && mcp-publisher status --status active "$SERVER_NAME" "$version" 2>&1 | tee "$publish_log"); then
        echo "official MCP Registry version is active"
      elif grep -qi 'No changes to apply' "$publish_log"; then
        echo "official MCP Registry version is already active"
      else
        exit 1
      fi
    else
      exit 1
    fi
    rm -f "$publish_log"
    if [[ "$MODE" == "all" ]]; then
      echo
      echo "Running mcp-submit for secondary directories..."
      (cd "$ROOT" && npx -y mcp-submit --yes)
    fi
    ;;
esac
