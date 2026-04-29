import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  buildSDKManifest,
  collectNativeModuleNames,
  defaultOutputPaths,
  isNativeModule,
  loadRepoInputs,
} from './generate-sdk-manifest.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const inputs = loadRepoInputs(repoRoot);

test('sdk-manifest outputs include the embedded desktop agent copy', () => {
  const outputs = defaultOutputPaths(repoRoot).map((value) => path.relative(repoRoot, value));
  assert.deepEqual(outputs, [
    'mobile/sdk-manifest.json',
    'cli/sdk-manifest.json',
    'desktop/agent/sdk-manifest.json',
    'mobile/ios/sdk-manifest.json',
    'mobile/ios/Yaver/sdk-manifest.json',
  ]);
});

test('native classifier keeps real host runtime modules', () => {
  for (const name of [
    '@amplitude/analytics-react-native',
    '@intercom/intercom-react-native',
    '@notifee/react-native',
    '@sentry/react-native',
    '@stripe/stripe-react-native',
    'expo-mail-composer',
    'react-native-worklets',
  ]) {
    assert.equal(isNativeModule(name, inputs), true, `${name} should stay in the host manifest`);
  }
});

test('native classifier excludes JS and build-time packages from the host manifest', () => {
  for (const name of [
    '@expo/metro-runtime',
    '@expo/vector-icons',
    '@gorhom/bottom-sheet',
    '@shopify/flash-list',
    'expo',
    'expo-build-properties',
    'expo-router',
    'posthog-react-native',
    'react-native-modal',
    'react-native-reanimated-carousel',
    'react-native-toast-message',
    'victory-native',
  ]) {
    assert.equal(isNativeModule(name, inputs), false, `${name} should not be treated as host-native`);
  }
});

test('generated manifest includes current compatibility-critical host modules and excludes JS-only packages', () => {
  const manifest = buildSDKManifest(inputs);
  const names = Object.keys(manifest.nativeModules);

  assert.ok(names.includes('expo-mail-composer'));
  assert.ok(names.includes('react-native-worklets'));
  assert.ok(names.includes('@amplitude/analytics-react-native'));
  assert.ok(names.includes('@stripe/stripe-react-native'));

  assert.ok(!names.includes('expo-router'));
  assert.ok(!names.includes('expo-build-properties'));
  assert.ok(!names.includes('@expo/vector-icons'));
  assert.ok(!names.includes('posthog-react-native'));
});

test('collectNativeModuleNames stays sorted and unique', () => {
  const names = collectNativeModuleNames(inputs);
  assert.deepEqual(names, Array.from(new Set(names)));
  assert.deepEqual(names, [...names].sort((a, b) => a.localeCompare(b)));
});
