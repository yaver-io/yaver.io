import { NextRequest, NextResponse } from "next/server";
import { auditYaverAppManifest, formatYaverAppPolicyAudit } from "@/lib/yaver-app-policy";
import { auditYaverGameManifest, formatYaverGamePolicyAudit } from "@/lib/yaver-game-policy";
import {
  requiredYaverNativeScopes,
  yaverNativeAuthAdapterText,
  type YaverNativeAppKind,
} from "@/lib/yaver-native-auth";

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
    name: "yaver_app_runtime_guide",
    title: "Build A Yaver-Native App",
    description: "Explain the Yaver app runtime contract for external release, optional Yaver catalog distribution, MCP, cloud inference, and multi-surface support.",
    inputSchema: {
      type: "object",
      properties: {
        appType: {
          type: "string",
          enum: ["game", "simulation", "workflow", "assistant_connector", "devtool", "unknown"],
          description: "The app family being integrated.",
        },
      },
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_native_oauth_guide",
    title: "Wire Yaver-Native OAuth",
    description: "Explain how a Yaver-native app or game should use Yaver OAuth as the platform identity layer below its own standalone auth providers.",
    inputSchema: {
      type: "object",
      properties: {
        appId: {
          type: "string",
          description: "Yaver app/game id, for example game_sfmg or game_carrotbet.",
        },
        appKind: {
          type: "string",
          enum: ["app", "game"],
          description: "Whether the integration is a general Yaver-native app or game.",
        },
        standaloneAuthAllowedOutsideYaver: {
          type: "boolean",
          description: "Whether the app can keep its own auth outside Yaver builds.",
        },
        externalProvidersOutsideYaver: {
          type: "array",
          items: { type: "string" },
          description: "Optional standalone providers outside Yaver, such as google, apple, email, github.",
        },
      },
      required: ["appId", "appKind"],
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_app_manifest_audit",
    title: "Audit A Yaver App Manifest",
    description: "Validate that a Yaver-native app manifest preserves external release freedom while enforcing Yaver OAuth, billing, source-sharing, native host, MCP, and surface policy for catalog builds.",
    inputSchema: {
      type: "object",
      properties: {
        manifest: {
          type: "object",
          description: "Parsed yaver.app.yaml or yaver.app.json manifest.",
          additionalProperties: true,
        },
      },
      required: ["manifest"],
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_strategy_game_native_guide",
    title: "Build A Yaver-Native Strategy Game",
    description: "Explain the Yaver-native strategy game contract for mobile, tablet, TV, watch, browser, and remote-runner compatibility.",
    inputSchema: {
      type: "object",
      properties: {
        gameType: {
          type: "string",
          enum: ["sfmg", "strategy", "simulation", "unknown"],
          description: "The game or strategy-game family being integrated.",
        },
      },
      additionalProperties: false,
    },
    outputSchema: textResultSchema,
    annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  },
  {
    name: "yaver_game_manifest_audit",
    title: "Audit A Yaver Game Manifest",
    description: "Validate that a Yaver-native game manifest requires Yaver OAuth, Yaver billing ownership, no source copying, native host declarations, and release-only source sharing.",
    inputSchema: {
      type: "object",
      properties: {
        manifest: {
          type: "object",
          description: "Parsed yaver.game.yaml or yaver.game.json manifest.",
          additionalProperties: true,
        },
      },
      required: ["manifest"],
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
    case "yaver_app_runtime_guide":
      return jsonRpc(id, textResult(appRuntimeText(String(args.appType || "unknown"))));
    case "yaver_native_oauth_guide": {
      const appKind = args.appKind === "game" ? "game" : "app";
      return jsonRpc(id, textResult(yaverNativeOauthText({
        appId: String(args.appId || "app_unknown"),
        appKind,
        standaloneAuthAllowedOutsideYaver: args.standaloneAuthAllowedOutsideYaver !== false,
        externalProvidersOutsideYaver: Array.isArray(args.externalProvidersOutsideYaver)
          ? args.externalProvidersOutsideYaver.filter((item: unknown): item is string => typeof item === "string")
          : undefined,
      })));
    }
    case "yaver_app_manifest_audit": {
      const audit = auditYaverAppManifest(args.manifest);
      return jsonRpc(id, textResult(formatYaverAppPolicyAudit(audit)));
    }
    case "yaver_strategy_game_native_guide":
      return jsonRpc(id, textResult(strategyGameText(String(args.gameType || "unknown"))));
    case "yaver_game_manifest_audit": {
      const audit = auditYaverGameManifest(args.manifest);
      return jsonRpc(id, textResult(formatYaverGamePolicyAudit(audit)));
    }
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

function yaverNativeOauthText(args: {
  appId: string;
  appKind: YaverNativeAppKind;
  standaloneAuthAllowedOutsideYaver: boolean;
  externalProvidersOutsideYaver?: readonly string[];
}) {
  return [
    yaverNativeAuthAdapterText({
      appId: args.appId,
      appKind: args.appKind,
      standaloneAuthAllowedOutsideYaver: args.standaloneAuthAllowedOutsideYaver,
      yaverAuthRequiredInYaverBuild: true,
      externalProvidersOutsideYaver: args.externalProvidersOutsideYaver,
    }),
    "",
    "Required Yaver scopes:",
    ...requiredYaverNativeScopes(args.appKind).map((scope) => `- ${scope}`),
    "",
    "Manifest:",
    `- Declare this in ${args.appKind === "game" ? "yaver.game.yaml" : "yaver.app.yaml"} with auth.provider = yaver-oauth and requiredInYaverBuild = true.`,
    "- Declare native.host.requiresYaverOAuth = true and list any Apple Info.plist / Android manifest requirements under native.apple or native.android.",
    "- Keep standalone auth providers only for outside-Yaver builds unless a reviewed catalog exception says otherwise.",
    "",
    "Recommended backend bridge:",
    "- Add a /yaver/auth/bootstrap route or equivalent server function.",
    "- Read Authorization: Bearer <Yaver token> from the request header.",
    "- Verify the bearer against Yaver /auth/validate or the approved introspection endpoint.",
    "- Enforce bootstrap appId/appKind/surface/scopes.",
    "- Upsert a local account link keyed by yaverUserId.",
    "- Derive local user/player/agent id server-side for all saves, multiplayer state, entitlements, and audit events.",
    "- Do not accept userId/playerId/agentId from the client as an authorization boundary.",
  ].join("\n");
}

function appRuntimeText(appType: string) {
  const label = appType === "unknown" ? "the app" : appType.replace("_", " ");
  return [
    "Yaver-native app runtime contract:",
    `- ${label} can be developed with Yaver and still released independently by the developer. Yaver catalog distribution is optional, reviewed, and separately monetized.`,
    "- External app releases may use developer-owned auth, hosting, payments, and stores. Yaver charges only for Yaver services the developer keeps using: cloud runners, inference, relay, feedback, testing, MCP hosting, and release automation.",
    "- In-Yaver catalog builds use Yaver OAuth, Yaver entitlements, Yaver billing, and no direct developer payments inside the Yaver app.",
    "- The reusable runtime path is intent -> command/tool call -> validation -> event log/audit -> render/result -> surface-specific summary.",
    "- MCP tools should be grouped into reviewed packs with declared scopes, risk, approval policy, data locality, and billing meter.",
    "- Surface support should be explicit: phone/web are full UI, TV is D-pad/wallboard/couch mode, watch is glance/approval, car is voice/status, XR is spatial panels, remote-runner does heavy work.",
    "- Yaver-managed inference should be sold as app capability, not raw tokens: intent parsing, scenario generation, test bots, summaries, feedback triage, and workflow planning.",
  ].join("\n");
}

function strategyGameText(gameType: string) {
  const gameLabel = gameType === "sfmg" ? "SFMG" : "the game";
  return [
    "Yaver-native strategy game contract:",
    `- ${gameLabel} should stay in its own repo/source system; Yaver imports it through a yaver.game.yaml manifest and a reviewed game package, not by copying code into yaver.io.`,
    "- Yaver is primarily a platform for strategy, simulation, tactics, management, and command/state-driven games rather than low-latency action games.",
    "- Required runtime shape: intent -> command -> validation -> deterministic reducer -> event log -> snapshot -> render.",
    "- Yaver OAuth/session is the account of record for Yaver-hosted builds. Future mobile/TV purchases use Yaver-owned IAP, Play Billing, or web entitlements.",
    "- Target surfaces should be declared explicitly: web, iOS phone, Android phone, tablet, tvOS, Android TV, watch companion, car/voice companion, and remote runner.",
    "- TV/tablet/mobile are primary play surfaces. Watch and car should be companion/briefing/approval surfaces, not full dense gameplay.",
    "- Development can use GitHub, GitLab, self-hosted Git, local folders, Yaver Cloud, self-hosted runtime, Codex, Claude Code, OpenCode, or other MCP/coding-agent tools.",
    "- Developers can still use Yaver to develop, test, run privately, self-host, or do whatever their own project/license allows without sharing source with Yaver.",
    "- Source/package sharing is only required for official in-Yaver catalog release/distribution, where private review access and Yaver compliance checks are mandatory.",
  ].join("\n");
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
