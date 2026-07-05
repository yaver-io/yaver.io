import { NextRequest, NextResponse } from "next/server";

const SERVER_NAME = "Yaver";
const SERVER_VERSION = "1.0.0";
const PROTOCOL_VERSION = "2025-06-18";

type JsonRpcId = string | number | null;

const textResultSchema = {
  type: "object",
  properties: {
    content: {
      type: "array",
      items: {
        type: "object",
        properties: {
          type: { type: "string" },
          text: { type: "string" },
        },
        required: ["type", "text"],
        additionalProperties: true,
      },
    },
    isError: { type: "boolean" },
  },
  required: ["content"],
  additionalProperties: false,
};

const tools = [
  {
    name: "yaver_codex_setup",
    title: "Set Up Yaver In Codex",
    description: "Get the recommended Yaver MCP setup steps for Codex Desktop, Codex CLI, Claude Code, or OpenCode.",
    inputSchema: {
      type: "object",
      properties: {
        client: {
          type: "string",
          enum: ["codex_desktop", "codex_cli", "claude_code", "opencode", "other"],
          description: "The MCP client the user wants to configure.",
        },
      },
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_mcp_package_info",
    title: "Yaver MCP Package Info",
    description: "Return public package and registry metadata for the local Yaver MCP server.",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_project_bootstrap_guide",
    title: "Bootstrap A Yaver Project",
    description: "Explain the first safe MCP calls for creating a local-first Yaver project and pairing a phone.",
    inputSchema: {
      type: "object",
      properties: {
        projectType: {
          type: "string",
          enum: ["react_native", "web", "full_stack", "unknown"],
          description: "The kind of app the user wants to start or connect.",
        },
      },
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_privacy_summary",
    title: "Yaver Privacy Summary",
    description: "Summarize how Yaver's hosted coordination plane differs from the local MCP server.",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
];

export async function POST(request: NextRequest) {
  const ct = request.headers.get("content-type") || "";
  if (!ct.includes("application/json")) {
    return jsonRpcError(null, -32700, "Content-Type must be application/json");
  }

  let body: any;
  try {
    body = await request.json();
  } catch {
    return jsonRpcError(null, -32700, "Parse error");
  }

  if (Array.isArray(body)) return jsonRpcError(null, -32600, "Batched requests are not supported");
  const id = (body?.id ?? null) as JsonRpcId;
  const method = String(body?.method || "");

  switch (method) {
    case "initialize":
      return jsonRpc(id, {
        protocolVersion: PROTOCOL_VERSION,
        serverInfo: { name: SERVER_NAME, version: SERVER_VERSION },
        capabilities: { tools: { listChanged: false } },
      });
    case "ping":
      return jsonRpc(id, {});
    case "notifications/initialized":
    case "notifications/cancelled":
      return new Response(null, { status: 204 });
    case "tools/list":
      return jsonRpc(id, { tools });
    case "tools/call":
      return callTool(id, body?.params);
    default:
      return jsonRpcError(id, -32601, `Method not found: ${method}`);
  }
}

export async function GET(request: NextRequest) {
  const base = baseUrlFromRequest(request);
  return NextResponse.json({
    server: SERVER_NAME,
    version: SERVER_VERSION,
    protocolVersion: PROTOCOL_VERSION,
    description: "Review-safe hosted MCP endpoint for Yaver setup guidance. Local machine control runs through the user's local Yaver MCP server.",
    tools: tools.map((tool) => tool.name),
    localMcp: {
      registryName: "io.github.kivanccakmak/yaver",
      npmPackage: "yaver-cli",
      command: "npx -y yaver-cli yaver-mcp",
      docs: `${base}/docs/mcp`,
    },
  }, { headers: { "Cache-Control": "public, max-age=60" } });
}

function callTool(id: JsonRpcId, params: any) {
  const name = String(params?.name || "");
  const args = params?.arguments || {};

  switch (name) {
    case "yaver_codex_setup":
      return jsonRpc(id, textResult(setupText(String(args.client || "codex_desktop"))));
    case "yaver_mcp_package_info":
      return jsonRpc(id, textResult([
        "Yaver MCP package:",
        "- Registry name: io.github.kivanccakmak/yaver",
        "- npm package: yaver-cli",
        "- Local stdio command: npx -y yaver-cli yaver-mcp",
        "- Docs: https://yaver.io/docs/mcp",
        "- Website: https://yaver.io",
        "",
        "The hosted ChatGPT app provides safe setup guidance. Full machine control requires the local Yaver MCP server running on the user's own machine.",
      ].join("\n")));
    case "yaver_project_bootstrap_guide":
      return jsonRpc(id, textResult(bootstrapText(String(args.projectType || "unknown"))));
    case "yaver_privacy_summary":
      return jsonRpc(id, textResult([
        "Yaver is local-first. The local MCP server runs on the user's own Mac, Linux, WSL, Pi, or VPS machine.",
        "Source code, prompts, task output, vault secrets, shell sessions, and deployment credentials are not sent through this hosted ChatGPT endpoint.",
        "The hosted Yaver services are used for sign-in, device discovery, and account coordination. Machine control is performed through the local Yaver MCP server after the user explicitly installs and authorizes it.",
        "Privacy policy: https://yaver.io/privacy",
      ].join("\n")));
    default:
      return jsonRpcError(id, -32602, `Unknown Yaver tool: ${name}`);
  }
}

function setupText(client: string) {
  if (client === "claude_code") {
    return [
      "Claude Code setup:",
      "1. Run:",
      "   claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp",
      "2. Start a new Claude Code session.",
      "3. Ask Claude Code to call yaver_lazy_setup.",
      "4. Complete the sign-in/device-code flow and pair your phone or dev machine.",
    ].join("\n");
  }
  if (client === "opencode") {
    return [
      "OpenCode setup:",
      "1. Run:",
      "   npm install -g yaver-cli && yaver mcp setup opencode",
      "2. Restart OpenCode.",
      "3. Ask the agent to call yaver_lazy_setup.",
    ].join("\n");
  }
  if (client === "codex_cli") {
    return [
      "Codex CLI setup:",
      "1. Run:",
      "   codex mcp add yaver -- npx -y yaver-cli yaver-mcp",
      "2. Start a fresh Codex session.",
      "3. Ask Codex to call yaver_lazy_setup.",
    ].join("\n");
  }
  return [
    "Codex Desktop setup:",
    "1. Install the Yaver plugin from the Yaver plugin marketplace entry, or add the MCP server manually:",
    "   codex mcp add yaver -- npx -y yaver-cli yaver-mcp",
    "2. Start a fresh Codex session so the MCP server loads.",
    "3. Ask Codex to call yaver_lazy_setup.",
    "4. Complete sign-in, pair your phone, then use Yaver's local MCP tools for reload/build/dev-loop workflows.",
  ].join("\n");
}

function bootstrapText(projectType: string) {
  const target = projectType === "react_native"
    ? "For React Native, run mobile_hermes_doctor after setup to verify the native Hermes reload path."
    : "For a new local-first app, call project_self_host_create after setup.";
  return [
    "Recommended Yaver bootstrap flow:",
    "1. Install/load the local Yaver MCP server in Codex, Claude Code, or OpenCode.",
    "2. Call yaver_lazy_setup and complete the sign-in flow.",
    "3. Pair the phone or development machine that should run the loop.",
    `4. ${target}`,
    "5. Keep source-code edits, shell execution, vault access, and deployment on the user's local machine through the local Yaver MCP server.",
  ].join("\n");
}

function textResult(text: string) {
  return { content: [{ type: "text", text }], isError: false };
}

function jsonRpc(id: JsonRpcId, result: any) {
  return NextResponse.json({ jsonrpc: "2.0", id, result }, { headers: { "Cache-Control": "no-store" } });
}

function jsonRpcError(id: JsonRpcId, code: number, message: string) {
  return NextResponse.json({ jsonrpc: "2.0", id, error: { code, message } }, { status: 200, headers: { "Cache-Control": "no-store" } });
}

function baseUrlFromRequest(request: NextRequest) {
  const proto = request.headers.get("x-forwarded-proto") || "https";
  const host = request.headers.get("x-forwarded-host") || request.headers.get("host") || "yaver.io";
  return `${proto}://${host}`.replace(/\/+$/, "");
}
