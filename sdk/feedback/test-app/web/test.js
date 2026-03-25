/**
 * Web SDK smoke test — runs in Node.js (no browser)
 * Tests: types compile, discovery logic, P2P client construction
 *
 * Run with: npx tsx test.js (needed for TypeScript require support)
 */

let passed = 0;
let failed = 0;

function assert(condition, msg) {
  if (condition) { passed++; console.log(`  PASS ${msg}`); }
  else { failed++; console.log(`  FAIL ${msg}`); }
}

// Mock browser APIs for Node.js
global.localStorage = {
  _store: {},
  getItem(k) { return this._store[k] || null; },
  setItem(k, v) { this._store[k] = v; },
  removeItem(k) { delete this._store[k]; },
};
global.fetch = async () => ({ ok: false, json: async () => ({}) });
global.navigator = { userAgent: 'test', platform: 'test', appVersion: 'test',
  mediaDevices: { getDisplayMedia: async () => ({}), getUserMedia: async () => ({}) } };
global.window = { innerWidth: 1920, innerHeight: 1080, location: { href: 'http://test', hostname: 'test' },
  addEventListener: () => {} };
global.document = { createElement: () => ({ style: {}, onclick: null, onmouseenter: null, onmouseleave: null,
  appendChild: () => {}, innerHTML: '', id: '', title: '', textContent: '',
  querySelector: () => null, querySelectorAll: () => [] }),
  body: { appendChild: () => {} }, addEventListener: () => {}, getElementById: () => null };
global.AbortSignal = { timeout: () => ({}) };
global.FormData = class { append() {} };
global.Blob = class { constructor(p, o) { this.size = p?.[0]?.length || 0; } };
global.MediaRecorder = class { start() {} stop() {} };
const realExit = process.exit.bind(process);
global.process = { ...process, env: { NODE_ENV: 'development' }, exit: realExit };

async function main() {
  console.log('Web Feedback SDK Smoke Test\n');

  // Test 1: Import discovery
  console.log('--- Discovery ---');
  const { YaverDiscovery } = require('../../web/src/discovery');
  assert(typeof YaverDiscovery.discover === 'function', 'YaverDiscovery.discover exists');
  assert(typeof YaverDiscovery.probe === 'function', 'YaverDiscovery.probe exists');
  assert(typeof YaverDiscovery.connect === 'function', 'YaverDiscovery.connect exists');

  // Test stored connection
  assert(YaverDiscovery.getStored() === null, 'No stored connection initially');
  YaverDiscovery.store({ url: 'http://test:18080', hostname: 'mac', version: '1.0', latency: 5 });
  const stored = YaverDiscovery.getStored();
  assert(stored && stored.url === 'http://test:18080', 'Store and retrieve connection');
  YaverDiscovery.clear();
  assert(YaverDiscovery.getStored() === null, 'Clear removes connection');

  // Test 2: Import P2P client
  console.log('\n--- P2PClient ---');
  const { P2PClient } = require('../../web/src/P2PClient');
  const client = new P2PClient('http://localhost:18080', 'test-token');
  assert(typeof client.health === 'function', 'P2PClient.health exists');
  assert(typeof client.uploadFeedback === 'function', 'P2PClient.uploadFeedback exists');
  assert(client.getArtifactUrl('abc') === 'http://localhost:18080/builds/abc/artifact', 'getArtifactUrl correct');

  // Test 3: Import FeedbackWidget
  console.log('\n--- FeedbackWidget ---');
  const { FeedbackWidget } = require('../../web/src/FeedbackWidget');
  assert(typeof FeedbackWidget.mount === 'function', 'FeedbackWidget.mount exists');
  assert(typeof FeedbackWidget.unmount === 'function', 'FeedbackWidget.unmount exists');

  // Test 4: Import YaverFeedback
  console.log('\n--- YaverFeedback ---');
  const { YaverFeedback } = require('../../web/src/YaverFeedback');
  assert(typeof YaverFeedback.init === 'function', 'YaverFeedback.init exists');
  assert(typeof YaverFeedback.startReport === 'function', 'YaverFeedback.startReport exists');
  assert(typeof YaverFeedback.startRecording === 'function', 'YaverFeedback.startRecording exists');
  assert(typeof YaverFeedback.stopAndSend === 'function', 'YaverFeedback.stopAndSend exists');
  assert(YaverFeedback.isInitialized === false, 'Not initialized before init');

  console.log(`\n=== Results: ${passed} passed, ${failed} failed ===`);
  process.exit(failed > 0 ? 1 : 0);
}

main().catch(err => {
  console.error('Error:', err.message);
  process.exit(1);
});
