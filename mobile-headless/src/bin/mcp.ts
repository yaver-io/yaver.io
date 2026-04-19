// yaver-mobile-headless MCP server.
//
// Two namespaces on one stdio MCP server:
//   mobile_tap_*   screen-level actions  ("tap Install")
//   mobile_api_*   raw endpoint probes   ("GET /install/list")
//
// Both backed by the same MobileClient. An AI agent can use whichever
// granularity it wants. Structured install streams come back as
// JSONL on stdout so a caller can watch progress without a second
// channel.

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { CallToolRequestSchema, ListToolsRequestSchema } from "@modelcontextprotocol/sdk/types.js";
import { MobileClient } from "../mobile-client.js";

const mobile = new MobileClient({
  dataDir: process.env.YMH_DATA_DIR,
  convexUrl: process.env.YMH_CONVEX_URL,
  authToken: process.env.YMH_AUTH_TOKEN,
  platform: (process.env.YMH_PLATFORM as any) || undefined,
  agentBaseUrl: process.env.YMH_AGENT_URL,
});

const server = new Server(
  { name: "yaver-mobile-headless", version: "0.1.0" },
  { capabilities: { tools: {} } },
);

// ── tool definitions ────────────────────────────────────────────
const tools = [
  // screen-level ("tap ...")
  { name: "mobile_sign_in",                description: "Sign in to Yaver with a bearer token or email+password.", inputSchema: schema({ token: str(), email: str(), password: str() }) },
  { name: "mobile_tap_devices",            description: "Refresh the devices list; like the user opening the Devices tab.", inputSchema: schema({}) },
  { name: "mobile_tap_select_device",      description: "Select a paired device so further API calls route to it.", inputSchema: schema({ deviceId: str() }, ["deviceId"]) },
  { name: "mobile_tap_infra",              description: "Show machine specs — CPU, RAM, disk, package managers, binaries.", inputSchema: schema({ target: str() }) },
  { name: "mobile_tap_install_list",       description: "Show the installable tool catalogue (built-in + Convex registry).", inputSchema: schema({ target: str() }) },
  { name: "mobile_tap_install_tool",       description: "Install a tool on the connected machine or a paired peer.", inputSchema: schema({ tool: str(), target: str() }, ["tool"]) },
  { name: "mobile_respond_sudo",           description: "Answer an in-flight install's sudo password prompt.", inputSchema: schema({ tool: str(), password: str(), target: str() }, ["tool", "password"]) },
  { name: "mobile_tap_new_project",        description: "Start the phone-first project wizard — returns the first question.", inputSchema: schema({}) },
  { name: "mobile_wizard_answer",          description: "Answer a wizard question, returns the next one.", inputSchema: schema({ sessionId: str(), questionId: str(), answer: str() }, ["sessionId", "questionId", "answer"]) },
  { name: "mobile_wizard_generate",        description: "Finish the wizard and generate the project on disk.", inputSchema: schema({ sessionId: str(), parentDir: str() }, ["sessionId"]) },
  { name: "mobile_tap_guests",             description: "List people the host has invited or granted access.", inputSchema: schema({}) },
  { name: "mobile_tap_primary_get",        description: "Read the user's primaryDeviceId — null when no preference is set.", inputSchema: schema({}) },
  { name: "mobile_tap_primary_set",        description: "Mark a device as the auto-connect target. Pass clear:true to unset.", inputSchema: schema({ deviceId: str(), clear: { type: "boolean" } as any }) },
  { name: "mobile_api_relay_presence",     description: "Bulk-probe relay tunnel-up state for a set of deviceIds (authoritative, no heartbeat lag).", inputSchema: schema({ relayHttpUrl: str(), deviceIds: { type: "array", items: { type: "string" } } as any }, ["relayHttpUrl", "deviceIds"]) },
  { name: "mobile_api_race_paths",         description: "Race beacon + lanIps + host in parallel against /health. Returns the winning path or null.", inputSchema: schema({ deviceId: str(), beaconIp: str(), beaconPort: { type: "number" } as any, perProbeMs: { type: "number" } as any }, ["deviceId"]) },
  { name: "mobile_auto_connect_target",    description: "Return the device the real app would auto-connect to given current online state + primary preference.", inputSchema: schema({}) },

  // raw API ("one endpoint per tool")
  { name: "mobile_api_get",                description: "Raw GET against any agent endpoint. Thin passthrough.", inputSchema: schema({ path: str() }, ["path"]) },
  { name: "mobile_api_post",               description: "Raw POST against any agent endpoint.", inputSchema: schema({ path: str(), body: { type: "object" } as any }, ["path"]) },
  { name: "mobile_api_install_list",       description: "Raw GET /install/list (optionally /peer/<id>/install/list).", inputSchema: schema({ target: str() }) },
  { name: "mobile_api_install",            description: "Raw POST /install/<tool>. Returns the stream name only; use mobile_tap_install_tool for live events.", inputSchema: schema({ tool: str(), target: str() }, ["tool"]) },
  { name: "mobile_api_infra_summary",      description: "Raw GET /infra/summary.", inputSchema: schema({ target: str() }) },
  { name: "mobile_api_wizard_start",       description: "Raw POST /project/wizard/start.", inputSchema: schema({}) },
  { name: "mobile_api_wizard_answer",      description: "Raw POST /project/wizard/answer.", inputSchema: schema({ sessionId: str(), questionId: str(), answer: str() }, ["sessionId", "questionId", "answer"]) },
  { name: "mobile_api_wizard_generate",    description: "Raw POST /project/wizard/generate.", inputSchema: schema({ sessionId: str(), parentDir: str() }, ["sessionId"]) },
];

function str() { return { type: "string" as const }; }
function schema(props: Record<string, any>, required: string[] = []) {
  return {
    type: "object" as const,
    properties: props,
    ...(required.length ? { required } : {}),
    additionalProperties: false,
  };
}

server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools }));

server.setRequestHandler(CallToolRequestSchema, async (req) => {
  const name = req.params.name;
  const args = (req.params.arguments ?? {}) as Record<string, any>;
  const reply = (v: unknown) => ({ content: [{ type: "text", text: JSON.stringify(v, null, 2) }] });

  try {
    switch (name) {
      // ── tap ────────────────────────────────────────────────────
      case "mobile_sign_in": {
        await mobile.signIn({ token: args.token, email: args.email, password: args.password });
        return reply({ ok: true });
      }
      case "mobile_tap_devices":       return reply(await mobile.listDevices());
      case "mobile_tap_select_device": await mobile.connect(args.deviceId); return reply({ ok: true });
      case "mobile_tap_infra":         return reply(await mobile.infraSummary(args.target));
      case "mobile_tap_install_list":  return reply(await mobile.listInstallables(args.target));
      case "mobile_tap_install_tool": {
        const frames: any[] = [];
        for await (const f of mobile.installTool(args.tool, { target: args.target })) {
          frames.push(f);
          // Don't hold the MCP caller forever on a sudo prompt —
          // yield once the prompt arrives so they can respond.
          if (f.kind === "sudo_prompt") break;
        }
        return reply({ frames });
      }
      case "mobile_respond_sudo":      await mobile.respondSudo(args.tool, args.password, { target: args.target }); return reply({ ok: true });
      case "mobile_tap_new_project":   return reply(await mobile.wizard.start());
      case "mobile_wizard_answer":     return reply(await mobile.wizard.answer(args.sessionId, args.questionId, args.answer));
      case "mobile_wizard_generate":   return reply(await mobile.wizard.generate(args.sessionId, args.parentDir));
      case "mobile_tap_guests":        return reply(await mobile.guests.list());
      case "mobile_tap_primary_get":   return reply({ primaryDeviceId: await mobile.getPrimaryDevice() });
      case "mobile_tap_primary_set": {
        if (args.clear) { await mobile.setPrimaryDevice(null); return reply({ ok: true, primaryDeviceId: null }); }
        if (!args.deviceId) return reply({ ok: false, error: "deviceId or clear=true required" });
        await mobile.setPrimaryDevice(String(args.deviceId));
        return reply({ ok: true, primaryDeviceId: args.deviceId });
      }
      case "mobile_api_relay_presence": {
        const ids = Array.isArray(args.deviceIds) ? args.deviceIds.map(String) : [];
        return reply(await mobile.relayPresence(String(args.relayHttpUrl), ids));
      }
      case "mobile_api_race_paths": {
        const devices = await mobile.listDevices();
        const d = devices.find((x) => x.id === args.deviceId);
        if (!d) return reply({ ok: false, error: `device not found: ${args.deviceId}` });
        return reply(await mobile.raceDevicePaths(d, {
          beaconIp: args.beaconIp,
          beaconPort: typeof args.beaconPort === "number" ? args.beaconPort : undefined,
          perProbeMs: typeof args.perProbeMs === "number" ? args.perProbeMs : undefined,
        }));
      }
      case "mobile_auto_connect_target": {
        const devices = await mobile.listDevices();
        const primary = await mobile.getPrimaryDevice();
        const target = mobile.pickAutoConnectTarget(devices, primary);
        return reply({ primaryDeviceId: primary, target: target ? { id: target.id, name: target.name } : null });
      }

      // ── api ────────────────────────────────────────────────────
      case "mobile_api_get":           return reply(await mobile.raw.get(args.path));
      case "mobile_api_post":          return reply(await mobile.raw.post(args.path, args.body));
      case "mobile_api_install_list":  return reply(await mobile.raw.get(args.target ? `/peer/${encodeURIComponent(args.target)}/install/list` : "/install/list"));
      case "mobile_api_install":       return reply(await mobile.raw.post(args.target ? `/peer/${encodeURIComponent(args.target)}/install/${encodeURIComponent(args.tool)}` : `/install/${encodeURIComponent(args.tool)}`));
      case "mobile_api_infra_summary": return reply(await mobile.raw.get(args.target ? `/peer/${encodeURIComponent(args.target)}/infra/summary` : "/infra/summary"));
      case "mobile_api_wizard_start":  return reply(await mobile.raw.post("/project/wizard/start"));
      case "mobile_api_wizard_answer": return reply(await mobile.raw.post("/project/wizard/answer", { sessionId: args.sessionId, questionId: args.questionId, answer: args.answer }));
      case "mobile_api_wizard_generate": return reply(await mobile.raw.post("/project/wizard/generate", { sessionId: args.sessionId, parentDir: args.parentDir }));

      default: return reply({ ok: false, error: `unknown tool: ${name}` });
    }
  } catch (e: any) {
    return reply({ ok: false, error: e?.message || String(e) });
  }
});

await server.connect(new StdioServerTransport());
