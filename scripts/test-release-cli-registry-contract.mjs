import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

test('accepted CLI publishes have a bounded propagation window and an immutable-version retry guard', () => {
  const workflow = readFileSync(new URL('../.github/workflows/release-cli.yml', import.meta.url), 'utf8');
  assert.ok(workflow.includes('deadline=$((SECONDS + 600))'));
  assert.ok(workflow.includes('npm view "yaver-cli@$VERSION" version'));
  assert.ok(workflow.includes('[ "$GOT" = "$WANT" ] && [ "$EXACT" = "$WANT" ]'));
  assert.ok(!workflow.includes('for i in 1 2 3 4 5; do'));
  assert.ok(!workflow.includes('NOT PUBLISHED TO NPM'));
});
