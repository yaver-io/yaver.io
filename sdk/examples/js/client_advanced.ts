/**
 * Advanced client — auth, device discovery, task management, callbacks.
 *
 * Run: YAVER_TOKEN=xxx npx tsx examples/js/client_advanced.ts
 */
import { YaverClient, YaverAuthClient } from "../../js/src/index";

const token = process.env.YAVER_TOKEN || "";
if (!token) {
  console.error("Set YAVER_TOKEN env var");
  process.exit(1);
}

async function main() {
  // ── Auth & device discovery ────────────────────────────────────────
  const auth = new YaverAuthClient(token);
  const user = await auth.validateToken();
  console.log(`Authenticated as ${user.email} (${user.provider})`);

  const devices = await auth.listDevices();
  console.log(`Devices (${devices.length}):`);
  for (const d of devices) {
    const status = d.isOnline ? "online" : "offline";
    console.log(`  ${d.deviceId.slice(0, 8)} — ${d.name} (${d.platform}) [${status}]`);
  }

  // ── User settings ──────────────────────────────────────────────────
  const settings = await auth.getSettings();
  console.log(`Runner: ${settings.runnerId || "claude"}, Verbosity: ${settings.verbosity ?? 10}`);

  // ── Connect to first online device ─────────────────────────────────
  const online = devices.filter((d) => d.isOnline);
  if (online.length === 0) {
    console.error("No online devices");
    process.exit(1);
  }

  const target = online[0];
  const host = target.quicHost.includes(":") ? `[${target.quicHost}]` : target.quicHost;
  const agentURL = process.env.YAVER_URL || `http://${host}:18080`;
  console.log(`\nConnecting to ${target.name} at ${agentURL}...`);

  const client = new YaverClient(agentURL, token);
  console.log(`Ping: ${await client.ping()}ms`);

  // ── Task with verbosity ────────────────────────────────────────────
  const task = await client.createTask("What is the current git branch?", {
    speechContext: { verbosity: 3 },
  });
  console.log(`Task ${task.id} created`);

  // Callback-style polling
  await pollWithCallback(client, task.id, (t) => {
    console.log(`  [${t.status}] output: ${(t.output || "").length} chars`);
  });

  // List all tasks
  const tasks = await client.listTasks();
  console.log(`\nAll tasks (${tasks.length}):`);
  for (const t of tasks) {
    console.log(`  ${t.id} — ${t.title.slice(0, 40)} (${t.status})`);
  }

  // Clean up
  await client.deleteTask(task.id);
  console.log("Done.");
}

async function pollWithCallback(
  client: YaverClient,
  taskId: string,
  onUpdate: (task: any) => void
) {
  for (let i = 0; i < 120; i++) {
    const task = await client.getTask(taskId);
    onUpdate(task);
    if (["completed", "failed", "stopped"].includes(task.status)) return;
    await new Promise((r) => setTimeout(r, 500));
  }
}

main().catch(console.error);
