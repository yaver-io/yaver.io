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
  //    — "run claude-code on box N" with no SSH / manual attach.
  if (first) {
    for await (const { text } of first.agent('profile the CUDA kernel in ./bench and open a PR', {
      runner: 'claude-code',
    })) {
      process.stdout.write(text);
    }
  }
}

main().catch((e) => { console.error(e); process.exit(1); });
