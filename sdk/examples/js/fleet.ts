/**
 * Fleet example — drive a SET of machines from code.
 *
 *   YAVER_TOKEN=... RELAY_URL=... RELAY_PW=... npx tsx fleet.ts
 *
 * Selects every online GPU machine, fans a command across them, then tags one.
 */
import { Fleet } from 'yaver-sdk';

async function main() {
  const fleet = await Fleet.connect({
    token: process.env.YAVER_TOKEN!,
    relay: process.env.RELAY_URL
      ? { url: process.env.RELAY_URL, password: process.env.RELAY_PW ?? '' }
      : null,
  });

  // 1. Select by tag (auto-seeded: gpu / arm64 / x64 / docker / linux / ...).
  const gpu = await fleet.select({ tags: ['gpu'], online: true });
  console.log(`${gpu.length} online GPU machine(s)`);

  // 2. Fan a command across them; output streams back tagged by machine.
  for await (const { machine, stream, text } of gpu.exec('nvidia-smi -L || uname -a')) {
    process[stream === 'stderr' ? 'stderr' : 'stdout'].write(
      `[${machine.alias ?? machine.deviceId.slice(0, 8)}] ${text}`,
    );
  }

  // 3. Collect results instead of streaming.
  const uptimes = await fleet.all().then((s) => s.run('uptime -p'));
  for (const r of uptimes) console.log(r.machine.alias, '→', r.stdout.trim(), `(exit ${r.code})`);

  // 4. Label a machine for future selectors.
  const [first] = gpu.machines;
  if (first) await first.tag({ add: ['ml-pool'] });

  // 5. Dispatch an autonomous coding agent to a machine and stream its session
  //    — "run claude-code on box N" with no SSH / manual attach. preferLocal
  //    routes to the on-device model when the machine can run one.
  if (first) {
    for await (const { text } of first.agent('profile the CUDA kernel in ./bench and open a PR', {
      runner: 'claude-code',
      preferLocal: true,           // → local model on `local-inference` machines (P12)
    })) {
      process.stdout.write(text);
    }
  }

  // 6. Interactive PTY shell on a machine (P5).
  if (first) {
    const sh = await first.shell();
    sh.onData((t) => process.stdout.write(t));
    sh.write('uptime && exit\n');
  }

  // 7. Store-and-forward: park a command for a machine that may be offline,
  //    flush it later when reachable (P13). Durable across restarts.
  const queue = fleet.queue('/tmp/yaver-fleet-queue.json');
  await queue.enqueue('edge-pi-3', 'sudo apt upgrade -y');
  const flushed = await queue.flush();
  console.log(`${flushed.filter((f) => f.ran).length} queued commands ran`);
}

main().catch((e) => { console.error(e); process.exit(1); });
