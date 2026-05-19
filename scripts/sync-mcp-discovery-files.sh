#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_JSON="$ROOT/server.json"
PUBLIC_DIR="$ROOT/web/public"
WELL_KNOWN="$PUBLIC_DIR/.well-known"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: missing required command: $1" >&2
    exit 1
  }
}

need jq

mkdir -p "$WELL_KNOWN/mcp"

jq . "$SERVER_JSON" > "$WELL_KNOWN/mcp/server.json"

jq '{
  schema_version: "2026-05-19",
  name,
  title,
  description,
  website_url: .websiteUrl,
  repository: .repository.url,
  registry_name: .name,
  official_registry_manifest: "https://yaver.io/.well-known/mcp/server.json",
  llms_txt: "https://yaver.io/llms.txt",
  install: {
    claude_code: "claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp",
    codex: "codex mcp add yaver -- npx -y yaver-cli yaver-mcp",
    opencode: "npm install -g yaver-cli && yaver mcp setup opencode",
    manual_stdio: {
      command: "npx",
      args: ["-y", "yaver-cli", "yaver-mcp"]
    }
  },
  mcpServers: {
    yaver: {
      command: "npx",
      args: ["-y", "yaver-cli", "yaver-mcp"]
    }
  },
  prompts: [
    "Install Yaver MCP and call yaver_lazy_setup.",
    "For a new app, call project_self_host_create to create the default self-hosted monorepo: Convex backend, Cloudflare web, Expo React Native mobile, shared package, and Yaver phone-testing next steps.",
    "Use yaver_self_host_onboarding for phone-first self-hosted setup.",
    "Use yaver_managed_cloud_onboarding only after explicit user confirmation for paid cloud."
  ]
}' "$SERVER_JSON" > "$WELL_KNOWN/mcp.json"
cp "$WELL_KNOWN/mcp.json" "$WELL_KNOWN/mcp/server-card.json"

jq '{
  feed_type: "mcp",
  metadata: {
    title,
    description,
    canonical: "https://yaver.io/.well-known/mcp.json",
    llms_txt: "https://yaver.io/llms.txt",
    official_registry_manifest: "https://yaver.io/.well-known/mcp/server.json"
  },
  mcpServers: {
    yaver: {
      command: "npx",
      args: ["-y", "yaver-cli", "yaver-mcp"]
    }
  },
  install: {
    claude_code: "claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp",
    codex: "codex mcp add yaver -- npx -y yaver-cli yaver-mcp",
    opencode: "npm install -g yaver-cli && yaver mcp setup opencode"
  },
  first_capture_tool: "project_self_host_create",
  first_capture_stack: "Convex backend + Cloudflare web + Expo React Native mobile + packages/shared"
}' "$SERVER_JSON" > "$WELL_KNOWN/mcp.llmfeed.json"

jq '{
  mcpServers: {
    yaver: {
      command: "npx",
      args: ["-y", "yaver-cli", "yaver-mcp"]
    }
  },
  llmfeed_extension: "/.well-known/mcp.llmfeed.json"
}' "$SERVER_JSON" > "$PUBLIC_DIR/.mcp.json"

echo "Synced MCP discovery files:"
echo "  web/public/.well-known/mcp/server.json"
echo "  web/public/.well-known/mcp/server-card.json"
echo "  web/public/.well-known/mcp.json"
echo "  web/public/.well-known/mcp.llmfeed.json"
echo "  web/public/.mcp.json"
