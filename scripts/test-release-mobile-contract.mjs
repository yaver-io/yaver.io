import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

test('iOS CI delegates archive and collision-safe build numbering to the canonical deploy path', () => {
  const workflow = readFileSync(new URL('../.github/workflows/release-mobile.yml', import.meta.url), 'utf8');
  const ios = workflow.slice(workflow.indexOf('\n  ios:'), workflow.indexOf('\n  android:'));
  assert.ok(ios.includes('environment: production'));
  assert.ok(ios.includes('./deploy/deploy.sh ios'));
  assert.ok(ios.includes('YAVER_PYTHON='));
  assert.ok(ios.includes('APP_STORE_KEY_PATH='));
  assert.ok(!ios.includes('100 + ${{ github.run_number }}'));
  assert.ok(!ios.includes('xcodebuild -workspace'));
  assert.ok(!ios.includes('expo prebuild'));
});
