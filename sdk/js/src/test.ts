/**
 * yaver-sdk tests — pure, network-free. Run: `npm test` (node --test dist/test.js).
 * Covers the generic policy resolver mirror and the transport candidate ladder.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { selectRunner, selectProvider, isWorkKindEnabled, type CompanyAIOptions } from './policy';
import { buildCandidates } from './connect';
import { Fleet, Machine, Selection, serviceCmd, type ExecResult, type MachineInfo } from './fleet';
import {
  composeEntitlements, entitlementAllows, entitlementFromGuest, entitlementFromResolved,
  LAYER4_DENIED_TOOLS, type Entitlement,
} from './acl';

function baseOptions(over: Partial<CompanyAIOptions> = {}): CompanyAIOptions {
  return {
    enabled: true,
    runtime: { mode: 'dedicated-compute', defaultProvider: 'hetzner', defaultDeviceId: 'dev_1' },
    convex: { deploymentKind: 'dedicated', envName: 'production' },
    runners: {
      defaultRunner: 'opencode',
      allowedRunners: ['opencode', 'codex', 'claude'],
      allowUserOverride: true,
      requireRunnerAuthPerUser: false,
      credentialMode: 'company-api-key-on-runtime',
    },
    mcp: { enabledServers: ['yaver'], requiredServers: ['yaver'] },
    workKinds: { appCode: true, erpFlow: true, convex: true, webUi: true, harnessCad: false, openScadCad: false, robotTrial: false, inspection: true },
    approvals: { requireApprovalForProductionWrites: true, requireApprovalForDeploy: true, requireApprovalForRobotMotion: true, requireApprovalForSecretsAccess: true },
    dataPolicy: { allowCustomerDataInPrompts: false, allowScreenshotsInPrompts: true, allowTelemetryInPrompts: false, redactPII: true, retentionDays: 30 },
    ...over,
  };
}

test('selectRunner honors the company default when no request', () => {
  assert.equal(selectRunner(baseOptions()), 'opencode');
});

test('selectRunner accepts an allowed override when allowUserOverride', () => {
  assert.equal(selectRunner(baseOptions(), 'codex'), 'codex');
});

test('selectRunner rejects a disallowed override and falls back to default', () => {
  assert.equal(selectRunner(baseOptions(), 'gemini-runner'), 'opencode');
});

test('selectRunner ignores override when allowUserOverride is false', () => {
  const opts = baseOptions({ runners: { ...baseOptions().runners, allowUserOverride: false } });
  assert.equal(selectRunner(opts, 'codex'), 'opencode');
});

test('selectRunner enforces a per-role runner cap (some users cannot wrap codex)', () => {
  const opts = baseOptions({
    appProfile: { app: 'talos', workKinds: [], roles: [{ role: 'operator', allowedRunners: ['opencode'] }] },
  });
  // operator may only use opencode even though they request codex
  assert.equal(selectRunner(opts, 'codex', 'operator'), 'opencode');
  // an engineer with no cap can still get codex
  assert.equal(selectRunner(opts, 'codex', 'engineer'), 'codex');
});

test('selectProvider returns null when no provider catalog', () => {
  assert.equal(selectProvider(baseOptions()), null);
});

test('selectProvider picks an allowed BYOK provider and respects role caps', () => {
  const opts = baseOptions({
    opencode: {
      providers: [
        { id: 'openrouter', label: 'OpenRouter', baseUrl: 'https://openrouter.ai/api/v1', models: ['x'], keyPolicy: 'company-secret' },
        { id: 'ollama', label: 'Ollama', baseUrl: 'http://localhost:11434/v1', models: ['llama'], keyPolicy: 'none' },
      ],
    },
    appProfile: { app: 'acme', workKinds: [], roles: [{ role: 'operator', allowedProviders: ['ollama'] }] },
  });
  assert.equal(selectProvider(opts, 'openrouter')?.id, 'openrouter');
  // operator is capped to the local model only
  assert.equal(selectProvider(opts, 'openrouter', 'operator')?.id, 'ollama');
});

test('isWorkKindEnabled reads the app profile first, then legacy toggles', () => {
  const opts = baseOptions({
    appProfile: { app: 'acme', workKinds: [{ key: 'odds-model', enabled: true }] },
  });
  assert.equal(isWorkKindEnabled(opts, 'odds-model'), true);
  assert.equal(isWorkKindEnabled(opts, 'app-code'), true);   // legacy true
  assert.equal(isWorkKindEnabled(opts, 'robot-trial'), false); // legacy false
  assert.equal(isWorkKindEnabled(opts, 'unknown-kind'), false);
});

test('buildCandidates orders direct -> tunnel -> relay and brackets IPv6', () => {
  const candidates = buildCandidates({
    deviceId: 'dev_1',
    token: 't',
    device: { deviceId: 'dev_1', localIps: ['192.168.1.5', 'fe80::1'], publicEndpoints: ['https://tunnel.example'] },
    relay: { url: 'https://relay.example', password: 'pw' },
  });
  const kinds = candidates.map((c) => c.kind);
  assert.deepEqual([kinds[0], kinds[kinds.length - 1]], ['direct', 'relay']);
  assert.ok(candidates.some((c) => c.baseURL.includes('[fe80::1]')), 'IPv6 host should be bracketed');
  const relay = candidates.find((c) => c.kind === 'relay');
  assert.equal(relay?.headers['X-Relay-Password'], 'pw');
  assert.ok(relay?.baseURL.endsWith('/d/dev_1'));
});

test('composeEntitlements intersects present allowlists and leaves absent ones open', () => {
  const company: Entitlement = { source: 'company-policy', allowedRunners: ['opencode', 'codex', 'claude'] };
  const guest: Entitlement = { source: 'guest:full', allowedRunners: ['opencode', 'codex'], allowedProjects: ['acme-erp'] };
  const eff = composeEntitlements([company, guest]);
  // runners = intersection of the two layers
  assert.deepEqual(eff.allowedRunners, ['opencode', 'codex']);
  // only guest constrained projects → that list wins; company didn't force it
  assert.deepEqual(eff.allowedProjects, ['acme-erp']);
  // nobody constrained providers → unconstrained
  assert.equal(eff.allowedProviders, undefined);
  assert.deepEqual(eff.sources, ['company-policy', 'guest:full']);
});

test('composeEntitlements never forces: a layer that omits a dimension does not narrow it', () => {
  const company: Entitlement = { source: 'company-policy' }; // no runner constraint
  const user: Entitlement = { source: 'user', allowedRunners: ['ollama-runner'] };
  const eff = composeEntitlements([company, user]);
  // user's own choice survives; company didn't force a different set
  assert.deepEqual(eff.allowedRunners, ['ollama-runner']);
});

test('composeEntitlements unions denylists and subtracts them from the tool allowlist', () => {
  const a: Entitlement = { source: 'a', allowedTools: ['code_*', 'vault_get', 'web_preview_start'] };
  const b: Entitlement = { source: 'b', deniedTools: [...LAYER4_DENIED_TOOLS] };
  const eff = composeEntitlements([a, b]);
  assert.ok(!eff.allowedTools?.includes('vault_get'), 'secret tool must be stripped');
  assert.ok(eff.deniedTools.includes('vault_get'));
});

test('composeEntitlements treats "*" tool allowlist as unconstrained', () => {
  const admin: Entitlement = { source: 'admin', allowedTools: ['*'] };
  const eng: Entitlement = { source: 'eng', allowedTools: ['code_*', 'talos_*'] };
  const eff = composeEntitlements([admin, eng]);
  assert.deepEqual(eff.allowedTools, ['code_*', 'talos_*']); // only the real constraint applies
});

test('composeEntitlements takes the tightest (min) numeric cap', () => {
  const eff = composeEntitlements([
    { source: 'a', dailyTokenLimit: 100000, ramLimitMb: 8192 },
    { source: 'b', dailyTokenLimit: 25000 },
  ]);
  assert.equal(eff.dailyTokenLimit, 25000);
  assert.equal(eff.ramLimitMb, 8192);
});

test('entitlementAllows treats undefined as unconstrained', () => {
  assert.equal(entitlementAllows(undefined, 'anything'), true);
  assert.equal(entitlementAllows(['opencode'], 'codex'), false);
  assert.equal(entitlementAllows(['opencode', 'codex'], 'codex'), true);
});

test('end-to-end: company allows codex, guest restricts to opencode → codex is blocked', () => {
  const resolved = {
    role: 'engineer',
    runner: { allowedRunners: ['opencode', 'codex', 'claude'] },
    provider: { allowedProviders: ['openrouter', 'ollama'] },
    workKind: 'app-code',
  };
  const eff = composeEntitlements([
    entitlementFromResolved(resolved),
    entitlementFromGuest({ scope: 'full', allowedRunners: ['opencode'] }),
  ]);
  assert.equal(entitlementAllows(eff.allowedRunners, 'codex'), false);
  assert.equal(entitlementAllows(eff.allowedRunners, 'opencode'), true);
  // providers were only constrained by company → still available
  assert.equal(entitlementAllows(eff.allowedProviders, 'openrouter'), true);
});

test('buildCandidates with forceRelay skips direct and tunnel', () => {
  const candidates = buildCandidates({
    deviceId: 'dev_1',
    token: 't',
    forceRelay: true,
    device: { deviceId: 'dev_1', localIps: ['192.168.1.5'] },
    relay: { url: 'https://relay.example', password: 'pw' },
  });
  assert.ok(candidates.every((c) => c.kind === 'relay'));
});

// --- Fleet: the concurrent merge is the subtle part, so pin it network-free ---
function fakeMachine(
  id: string,
  lines: Array<{ stream: 'stdout' | 'stderr'; text: string }>,
  perLineDelayMs = 0,
): Machine {
  const info: MachineInfo = {
    deviceId: id, name: id, alias: id, platform: 'linux', tags: [],
    online: true, quicHost: '', quicPort: 0, localIps: [], publicEndpoints: [],
  };
  return {
    info,
    async *exec(): AsyncGenerator<{ stream: 'stdout' | 'stderr'; text: string }, ExecResult> {
      for (const l of lines) {
        if (perLineDelayMs) await new Promise((r) => setTimeout(r, perLineDelayMs));
        yield l;
      }
      return {
        machine: info, code: 0,
        stdout: lines.filter((l) => l.stream === 'stdout').map((l) => l.text).join(''),
        stderr: lines.filter((l) => l.stream === 'stderr').map((l) => l.text).join(''),
      };
    },
  } as unknown as Machine;
}

test('Selection.exec merges per-machine streams and collects results', async () => {
  const sel = new Selection([
    fakeMachine('a', [{ stream: 'stdout', text: 'a1' }, { stream: 'stdout', text: 'a2' }], 5),
    fakeMachine('b', [{ stream: 'stderr', text: 'b1' }]),
  ]);
  const seen: string[] = [];
  const it = sel.exec('noop');
  let n = await it.next();
  while (!n.done) {
    seen.push(`${n.value.machine.deviceId}:${n.value.stream}:${n.value.text}`);
    n = await it.next();
  }
  const results = n.value;
  assert.equal(results.length, 2, 'one ExecResult per machine');
  assert.ok(seen.includes('a:stdout:a1') && seen.includes('a:stdout:a2'));
  assert.ok(seen.includes('b:stderr:b1'));
  assert.ok(results.every((r) => r.code === 0));
  // b has no delay so its line must arrive before a's delayed second line.
  assert.ok(seen.indexOf('b:stderr:b1') < seen.indexOf('a:stdout:a2'), 'fast machine not blocked by slow one');
});

// --- Fleet: verified-action loop (precheck/do/verify/rollback) ---------------
// Build a real Machine and script its run() so apply()'s logic is exercised
// network-free. Returns [machine, the ordered list of commands it ran].
async function applyMachine(
  responses: Record<string, { code: number; stdout?: string }>,
): Promise<{ m: Machine; calls: string[] }> {
  const fleet = await Fleet.connect({ token: 't' });
  const info: MachineInfo = {
    deviceId: 'm1', name: 'm1', alias: 'm1', platform: 'linux', tags: [],
    online: true, quicHost: '', quicPort: 0, localIps: [], publicEndpoints: [],
  };
  const m = new Machine(fleet, info);
  const calls: string[] = [];
  (m as unknown as { run: (cmd: string) => Promise<ExecResult> }).run = async (cmd: string) => {
    calls.push(cmd);
    const r = responses[cmd] ?? { code: 0, stdout: '' };
    return { machine: info, code: r.code, stdout: r.stdout ?? '', stderr: '' };
  };
  return { m, calls };
}

test('apply skips the mutation when precheck already passes (idempotency)', async () => {
  const { m, calls } = await applyMachine({ 'get rate': { code: 0, stdout: '5' } });
  const res = await m.apply({ key: 'rate', precheck: 'get rate', do: 'set rate 5', verify: 'get rate', expect: '5' });
  assert.equal(res.status, 'already');
  assert.ok(!calls.includes('set rate 5'), 'mutation must be skipped');
});

test('apply runs do then verifies', async () => {
  const { m, calls } = await applyMachine({
    'get rate': { code: 0, stdout: '5' },     // precheck/verify both read 5
    'set rate 5': { code: 0, stdout: '' },
  });
  // precheck reads 5 already → would skip; drop precheck to force the do path.
  const res = await m.apply({ key: 'rate', do: 'set rate 5', verify: 'get rate', expect: '5' });
  assert.equal(res.status, 'verified');
  assert.equal(res.attempts, 1);
  assert.ok(calls.includes('set rate 5') && calls.includes('get rate'));
});

test('apply rolls back when verify fails and onFail=rollback', async () => {
  const { m, calls } = await applyMachine({
    'set rate 9': { code: 0, stdout: '' },
    'get rate': { code: 0, stdout: '3' },   // verify expects 9 but reads 3 → fail
    'set rate 0': { code: 0, stdout: '' },  // rollback
  });
  const res = await m.apply({ key: 'rate', do: 'set rate 9', verify: 'get rate', expect: '9', rollback: 'set rate 0', onFail: 'rollback' });
  assert.equal(res.status, 'rolled-back');
  assert.ok(calls.includes('set rate 0'), 'rollback must run');
});

test('apply throws by default when verify fails', async () => {
  const { m } = await applyMachine({ 'do x': { code: 0 }, 'check x': { code: 1 } });
  await assert.rejects(() => m.apply({ key: 'x', do: 'do x', verify: 'check x' }));
});

test('Selection.distribute spreads a work-list across machines (work-stealing)', async () => {
  const fleet = await Fleet.connect({ token: 't' });
  const mk = (id: string): Machine => new Machine(fleet, {
    deviceId: id, name: id, alias: id, platform: 'linux', tags: [],
    online: true, quicHost: '', quicPort: 0, localIps: [], publicEndpoints: [],
  });
  const sel = new Selection([mk('a'), mk('b')]);
  const items = [1, 2, 3, 4, 5, 6, 7];
  const handledBy: Record<number, string> = {};
  const out = await sel.distribute(items, async (machine, item) => {
    handledBy[item] = machine.deviceId;
    await new Promise((r) => setTimeout(r, item === 1 ? 20 : 1)); // make 'a' briefly slow
    return item * 10;
  });
  // results in input order
  assert.deepEqual(out, [10, 20, 30, 40, 50, 60, 70]);
  // every item handled, by one of the two machines
  assert.equal(Object.keys(handledBy).length, 7);
  assert.ok(items.every((i) => handledBy[i] === 'a' || handledBy[i] === 'b'));
  // work-stealing: both machines did some work (not all on one)
  const counts = Object.values(handledBy);
  assert.ok(counts.includes('a') && counts.includes('b'), 'both machines should pull work');
});

test('approval gate blocks high-risk exec and records denial; audit fires', async () => {
  const seen: string[] = [];
  const fleet = await Fleet.connect({
    token: 't',
    approve: (ev) => ev.risk !== 'high',          // deny anything high-risk
    onAudit: (ev) => seen.push(`${ev.kind}:${ev.outcome}`),
  });
  const m = new Machine(fleet, {
    deviceId: 'm1', name: 'm1', alias: 'm1', platform: 'linux', tags: [],
    online: true, quicHost: '', quicPort: 0, localIps: [], publicEndpoints: [],
  });
  // high-risk command → gate denies before any transport/network call.
  const res = await m.run('rm -rf /tmp/x');
  assert.equal(res.code, -1);
  assert.match(res.error ?? '', /denied/);
  assert.ok(seen.includes('exec:denied'), 'audit must record the denial');
});

test('low-risk exec does not consult the approval gate', async () => {
  let asked = 0;
  const fleet = await Fleet.connect({ token: 't', approve: () => { asked++; return true; } });
  // No transport coords → a non-denied command fails at transport resolution.
  // We only assert the gate was never consulted for a low-risk command.
  const m = new Machine(fleet, {
    deviceId: 'm1', name: 'm1', alias: 'm1', platform: 'linux', tags: [],
    online: true, quicHost: '', quicPort: 0, localIps: [], publicEndpoints: [],
  });
  await m.run('ls -la').catch(() => { /* transport failure is expected/irrelevant */ });
  assert.equal(asked, 0, 'low-risk exec must not call approve');
});

test('serviceCmd builds platform-native service commands', () => {
  assert.equal(serviceCmd('linux', 'restart', 'yaver'), 'systemctl restart yaver');
  assert.equal(serviceCmd('linux', 'status', 'yaver'), 'systemctl status yaver');
  assert.equal(serviceCmd('windows', 'restart', 'Yaver'), 'sc restart Yaver');
  assert.equal(serviceCmd('windows', 'status', 'Yaver'), 'sc query Yaver');
  assert.equal(serviceCmd('macos', 'restart', 'io.yaver.agent'), 'launchctl kickstart -k system/io.yaver.agent');
  assert.equal(serviceCmd('macos', 'status', 'io.yaver.agent'), 'launchctl print system/io.yaver.agent');
});

test('Selection.mapReduce folds per-machine values', async () => {
  const fleet = await Fleet.connect({ token: 't' });
  const mk = (id: string): Machine => new Machine(fleet, {
    deviceId: id, name: id, alias: id, platform: 'linux', tags: [],
    online: true, quicHost: '', quicPort: 0, localIps: [], publicEndpoints: [],
  });
  const sel = new Selection([mk('a'), mk('b'), mk('c')]);
  const total = await sel.mapReduce(async (m) => m.deviceId.length + 1, (acc, v) => acc + v, 0);
  assert.equal(total, 6); // each deviceId length 1 → (1+1)*3
});
