import assert from 'node:assert/strict';
import test from 'node:test';
import { agentHttpBase, urlHost } from './endpoints';

test('URL hosts preserve IPv4 and bracket IPv6 exactly once', () => {
  assert.equal(urlHost('192.0.2.10'), '192.0.2.10');
  assert.equal(urlHost('box.local'), 'box.local');
  assert.equal(urlHost('2001:db8::10'), '[2001:db8::10]');
  assert.equal(urlHost('[2001:db8::10]'), '[2001:db8::10]');
  assert.equal(agentHttpBase('2001:db8::10', 18080), 'http://[2001:db8::10]:18080');
});
