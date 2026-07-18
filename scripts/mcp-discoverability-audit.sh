#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_NAME="${MCP_SERVER_NAME:-io.github.yaver-io/yaver}"
NPM_PACKAGE="${MCP_NPM_PACKAGE:-yaver-cli}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

need curl
need jq
need npm

echo "== local well-known discovery files =="
"$ROOT/scripts/sync-mcp-discovery-files.sh" >/dev/null
jq -e --arg name "$SERVER_NAME" '.name == $name' "$ROOT/web/public/.well-known/mcp/server.json" >/dev/null
jq -e --arg name "$SERVER_NAME" '.name == $name' "$ROOT/web/public/.well-known/mcp/server-card.json" >/dev/null
jq -e '.mcpServers.yaver.command == "npx"' "$ROOT/web/public/.well-known/mcp.json" >/dev/null
jq -e '.mcpServers.yaver.args == ["-y","yaver-cli","yaver-mcp"]' "$ROOT/web/public/.mcp.json" >/dev/null
echo "local well-known files are synced"
echo

echo "== npm =="
npm_json="$(npm view "$NPM_PACKAGE" name version description keywords mcpName repository homepage --json)"
echo "$npm_json" | jq '{name,version,mcpName,description,keywords,homepage}'
npm_version="$(echo "$npm_json" | jq -r '.version')"
npm_mcp_name="$(echo "$npm_json" | jq -r '.mcpName // empty')"
if [[ "$npm_mcp_name" != "$SERVER_NAME" ]]; then
  echo "ERROR: npm mcpName is '$npm_mcp_name', expected '$SERVER_NAME'" >&2
  exit 1
fi
for kw in mcp mcp-server model-context-protocol claude-code codex ai-agent; do
  if ! echo "$npm_json" | jq -e --arg kw "$kw" '(.keywords // []) | index($kw)' >/dev/null; then
    echo "WARN: npm latest is missing keyword '$kw' (will fix on next npm publish)"
  fi
done

echo
echo "== local metadata =="
jq --arg name "$SERVER_NAME" --arg pkg "$NPM_PACKAGE" \
  '{
    name,
    version,
    packageVersion: (.packages[] | select(.registryType=="npm" and .identifier==$pkg) | .version),
    packageIdentifier: (.packages[] | select(.registryType=="npm") | .identifier)
  }' "$ROOT/server.json"
local_name="$(jq -r '.name' "$ROOT/server.json")"
local_pkg="$(jq -r '.packages[] | select(.registryType=="npm") | .identifier' "$ROOT/server.json" | head -1)"
if [[ "$local_name" != "$SERVER_NAME" || "$local_pkg" != "$NPM_PACKAGE" ]]; then
  echo "ERROR: server.json name/package mismatch" >&2
  exit 1
fi

echo
echo "== public well-known URLs =="
for url in \
  "https://yaver.io/.well-known/mcp/server.json" \
  "https://yaver.io/.well-known/mcp/server-card.json" \
  "https://yaver.io/.well-known/mcp.json" \
  "https://yaver.io/.well-known/mcp.llmfeed.json" \
  "https://yaver.io/.mcp.json"; do
  if body="$(curl -fsSL "$url")"; then
    title="$(echo "$body" | jq -r '.title // .metadata.title // .mcpServers.yaver.command // empty' 2>/dev/null || true)"
    echo "OK: $url ${title:+($title)}"
  else
    echo "WARN: public URL not reachable yet: $url"
  fi
done

echo
echo "== official MCP registry =="
registry_json="$(curl -fsSL "https://registry.modelcontextprotocol.io/v0.1/servers?search=${SERVER_NAME}&limit=100")"
registry_versions="$(echo "$registry_json" | jq -r --arg name "$SERVER_NAME" '.servers[] | select(.server.name==$name) | .server.version')"
if [[ -z "$registry_versions" ]]; then
  echo "ERROR: $SERVER_NAME is not listed in the official MCP Registry" >&2
  exit 1
fi
echo "$registry_versions" | sort -V | tail -10 | sed 's/^/  /'
registry_latest="$(echo "$registry_versions" | sort -V | tail -1)"
if [[ "$registry_latest" != "$npm_version" ]]; then
  echo "WARN: official MCP Registry latest seen is $registry_latest, npm latest is $npm_version"
  echo "      Run scripts/publish-mcp-registries.sh --official after npm publish, or check release-cli.yml."
else
  echo "official MCP Registry is current: $registry_latest"
fi

echo
echo "== Glama =="
if glama_json="$(curl -fsSL 'https://glama.ai/api/mcp/v1/servers?query=yaver')"; then
  echo "$glama_json" | jq -r '.servers[] | select(.name=="Yaver" or .repository.url=="https://github.com/kivanccakmak/yaver.io") | {name,namespace,slug,url,tools:(.tools|length),attributes,environmentVariablesJsonSchema}'
else
  echo "WARN: Glama API query failed"
fi

echo
echo "== Smithery =="
if smithery_json="$(curl -fsSL 'https://api.smithery.ai/servers?q=yaver')"; then
  matches="$(echo "$smithery_json" | jq -r '.servers[]? | select((.qualifiedName // "" | test("yaver"; "i")) or (.displayName // "" | test("yaver"; "i"))) | .qualifiedName' | paste -sd ', ' -)"
  if [[ -z "$matches" ]]; then
    echo "WARN: no Smithery search result for Yaver. Submit/publish with Smithery after verifying smithery.yaml."
  else
    echo "Smithery matches: $matches"
  fi
else
  echo "WARN: Smithery API query failed"
fi

echo
echo "Done."
