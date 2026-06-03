/**
 * yaver-sdk tests — pure, network-free. Run: `npm test` (node --test dist/test.js).
 * Covers the generic policy resolver mirror and the transport candidate ladder.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { selectRunner, selectProvider, isWorkKindEnabled, type CompanyAIOptions } from './policy';
import { buildCandidates } from './connect';
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
