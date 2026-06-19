#!/usr/bin/env node
/**
 * Feedback SDK Integration Test
 *
 * Tests the feedback HTTP API against a running Yaver agent.
 * Run: node test-feedback.js [agent-url]
 *
 * Default agent URL: http://localhost:18080
 */

const http = require('http');
const https = require('https');
const fs = require('fs');
const path = require('path');

const AGENT_URL = process.argv[2] || 'http://localhost:18080';
const AUTH_TOKEN = process.env.YAVER_AUTH_TOKEN || '';

let passed = 0;
let failed = 0;

async function request(method, urlPath, body, headers = {}) {
  const url = new URL(urlPath, AGENT_URL);
  const mod = url.protocol === 'https:' ? https : http;

  return new Promise((resolve, reject) => {
    const opts = {
      method,
      hostname: url.hostname,
      port: url.port,
      path: url.pathname + url.search,
      headers: {
        ...headers,
        ...(AUTH_TOKEN ? { 'Authorization': `Bearer ${AUTH_TOKEN}` } : {}),
      },
    };

    const req = mod.request(opts, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try {
          resolve({ status: res.statusCode, body: JSON.parse(data), headers: res.headers });
        } catch {
          resolve({ status: res.statusCode, body: data, headers: res.headers });
        }
      });
    });
    req.on('error', reject);
    if (body) {
      if (typeof body === 'string') {
        req.write(body);
      } else {
        req.write(JSON.stringify(body));
      }
    }
    req.end();
  });
}

function assert(condition, message) {
  if (condition) {
    passed++;
    console.log(`  ✓ ${message}`);
  } else {
    failed++;
    console.log(`  ✗ ${message}`);
  }
}

async function testHealth() {
  console.log('\n--- Health ---');
  const { status } = await request('GET', '/health');
  assert(status === 200, 'GET /health returns 200');
}

async function testFeedbackList() {
  console.log('\n--- Feedback List ---');
  const { status, body } = await request('GET', '/feedback');
  assert(status === 200, 'GET /feedback returns 200');
  assert(Array.isArray(body), 'Response is array');
}

async function testFeedbackUpload() {
  console.log('\n--- Feedback Upload ---');

  // Create a multipart form manually
  const boundary = '----YaverTestBoundary' + Date.now();
  const metadata = JSON.stringify({
    source: 'test-app',
    deviceInfo: { platform: 'test', model: 'CI', osVersion: '1.0' },
    timeline: [
      { time: 1.0, type: 'annotation', text: 'test bug report' }
    ],
  });

  // Build multipart body
  let body = '';
  body += `--${boundary}\r\n`;
  body += 'Content-Disposition: form-data; name="metadata"\r\n\r\n';
  body += metadata + '\r\n';

  // Add fake screenshot
  body += `--${boundary}\r\n`;
  body += 'Content-Disposition: form-data; name="screenshot"; filename="test.jpg"\r\n';
  body += 'Content-Type: image/jpeg\r\n\r\n';
  body += 'fake-jpeg-data\r\n';

  body += `--${boundary}--\r\n`;

  const { status, body: resp } = await request('POST', '/feedback', body, {
    'Content-Type': `multipart/form-data; boundary=${boundary}`,
  });

  assert(status === 200, 'POST /feedback returns 200');
  assert(resp && resp.id, 'Response has report ID');

  if (resp && resp.id) {
    return resp.id;
  }
  return null;
}

async function testFeedbackGet(id) {
  if (!id) return;
  console.log('\n--- Feedback Get ---');
  const { status, body } = await request('GET', `/feedback/${id}`);
  assert(status === 200, `GET /feedback/${id} returns 200`);
  assert(body.source === 'test-app', 'Source matches');
  assert(body.timeline && body.timeline.length > 0, 'Timeline has events');
}

async function testFeedbackFix(id) {
  if (!id) return;
  console.log('\n--- Feedback Fix ---');
  const { status, body } = await request('POST', `/feedback/${id}/fix`, {});
  assert(status === 200, `POST /feedback/${id}/fix returns 200`);
  assert(body.prompt && body.prompt.includes('Bug report'), 'Generated prompt contains bug report header');
}

async function testFeedbackDelete(id) {
  if (!id) return;
  console.log('\n--- Feedback Delete ---');
  const { status } = await request('DELETE', `/feedback/${id}`);
  assert(status === 200, `DELETE /feedback/${id} returns 200`);

  // Verify deleted
  const { status: status2 } = await request('GET', `/feedback/${id}`);
  assert(status2 === 404, 'Deleted feedback returns 404');
}

async function testBuildsAPI() {
  console.log('\n--- Builds API ---');
  const { status, body } = await request('GET', '/builds');
  assert(status === 200, 'GET /builds returns 200');
  assert(Array.isArray(body), 'Builds response is array');
}

async function testTestsAPI() {
  console.log('\n--- Tests API ---');
  const { status, body } = await request('GET', '/tests');
  assert(status === 200, 'GET /tests returns 200');
  assert(Array.isArray(body), 'Tests response is array');
}

async function testTunnelsAPI() {
  console.log('\n--- Tunnels API ---');
  const { status, body } = await request('GET', '/tunnels');
  assert(status === 200, 'GET /tunnels returns 200');
  assert(Array.isArray(body), 'Tunnels response is array');
}

async function testVaultAPI() {
  console.log('\n--- Vault API ---');
  const { status, body } = await request('GET', '/vault/list');
  assert(status === 200, 'GET /vault/list returns 200');
  assert(Array.isArray(body?.entries), 'Vault list entries is array');
}

async function main() {
  console.log(`Testing feedback SDK against ${AGENT_URL}`);

  try {
    await testHealth();
    await testFeedbackList();
    const reportId = await testFeedbackUpload();
    await testFeedbackGet(reportId);
    await testFeedbackFix(reportId);
    await testFeedbackDelete(reportId);
    await testBuildsAPI();
    await testTestsAPI();
    await testTunnelsAPI();
    await testVaultAPI();
  } catch (err) {
    console.error('\nConnection error:', err.message);
    console.error('Is the agent running? Start with: yaver serve --debug');
    process.exit(1);
  }

  console.log(`\n--- Results: ${passed} passed, ${failed} failed ---`);
  process.exit(failed > 0 ? 1 : 0);
}

main();
