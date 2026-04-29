import fs from 'fs';
import path from 'path';
import process from 'process';
import { fileURLToPath, pathToFileURL } from 'url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

export function defaultOutputPaths(root = repoRoot) {
  return [
    path.join(root, 'mobile', 'sdk-manifest.json'),
    path.join(root, 'cli', 'sdk-manifest.json'),
    path.join(root, 'desktop', 'agent', 'sdk-manifest.json'),
    path.join(root, 'mobile', 'ios', 'sdk-manifest.json'),
    path.join(root, 'mobile', 'ios', 'Yaver', 'sdk-manifest.json'),
  ];
}

export function loadRepoInputs(root = repoRoot) {
  return {
    repoRoot: root,
    config: readJson(path.join(root, 'scripts', 'sdk-manifest.config.json')),
    mobilePackage: readJson(path.join(root, 'mobile', 'package.json')),
    mobileAppConfig: readJson(path.join(root, 'mobile', 'app.json')),
    mobileLock: readJson(path.join(root, 'mobile', 'package-lock.json')),
  };
}

export function collectNativeModuleNames(inputs) {
  return Object.keys(inputs.mobilePackage.dependencies || {})
    .filter((name) => isNativeModule(name, inputs))
    .sort((a, b) => a.localeCompare(b));
}

export function buildSDKManifest(inputs = loadRepoInputs(repoRoot)) {
  const pluginPackages = new Set(
    normalizePlugins(inputs.mobileAppConfig.expo?.plugins || []).filter(Boolean)
  );
  const nativeModuleNames = collectNativeModuleNames(inputs);
  const nativeModules = {};
  const moduleSupport = {};

  for (const name of nativeModuleNames) {
    const version = getInstalledVersion(name, inputs);
    if (!version) {
      throw new Error(`Missing installed version for ${name} in mobile/package-lock.json`);
    }
    nativeModules[name] = version;
    moduleSupport[name] = {
      version,
      ...inputs.config.moduleSupportDefaults,
      pluginEnabled: pluginPackages.has(name),
    };
  }

  return {
    sdkVersion: inputs.config.sdkVersion,
    expo: getInstalledVersion('expo', inputs),
    reactNative: getInstalledVersion('react-native', inputs),
    react: getInstalledVersion('react', inputs),
    hermes: {
      version: getInstalledVersion('react-native', inputs),
      bytecodeVersion: inputs.config.hermesBytecodeVersion,
    },
    arch: inputs.config.arch,
    supportedRNRange: inputs.config.supportedRNRange,
    nativeModules,
    moduleSupport,
  };
}

export function runGenerateSDKManifest({ root = repoRoot, checkOnly = false } = {}) {
  const inputs = loadRepoInputs(root);
  const outputPaths = defaultOutputPaths(root);
  const manifest = buildSDKManifest(inputs);
  const serialized = `${JSON.stringify(manifest, null, 2)}\n`;
  const driftedPaths = [];

  for (const outputPath of outputPaths) {
    if (checkOnly) {
      const existing = fs.existsSync(outputPath) ? fs.readFileSync(outputPath, 'utf8') : '';
      if (existing !== serialized) {
        driftedPaths.push(path.relative(root, outputPath));
      }
      continue;
    }
    fs.writeFileSync(outputPath, serialized);
  }

  if (checkOnly) {
    if (driftedPaths.length > 0) {
      console.error('sdk-manifest drift detected in:');
      for (const driftedPath of driftedPaths) {
        console.error(`- ${driftedPath}`);
      }
      return 1;
    }
    console.log(`sdk-manifest is in sync across ${outputPaths.length} outputs.`);
    return 0;
  }

  console.log(
    `Generated sdk-manifest (${Object.keys(manifest.nativeModules).length} native modules) -> ${outputPaths
      .map((value) => path.relative(root, value))
      .join(', ')}`
  );
  return 0;
}

export function isNativeModule(name, inputs) {
  const config = inputs.config;
  if ((config.jsOnlyExact || []).includes(name)) return false;
  if ((config.manualNativeModules || []).includes(name)) return true;

  const packageDir = path.join(inputs.repoRoot, 'mobile', 'node_modules', ...name.split('/'));
  if (fs.existsSync(packageDir) && fs.statSync(packageDir).isDirectory()) {
    return hasNativePackageMarkers(packageDir);
  }
  return fallbackNativeNameHeuristic(name);
}

function hasNativePackageMarkers(packageDir) {
  const entries = fs.readdirSync(packageDir, { withFileTypes: true });
  return entries.some((entry) =>
    entry.name.endsWith('.podspec') ||
    (entry.isDirectory() && (entry.name === 'ios' || entry.name === 'android'))
  );
}

function fallbackNativeNameHeuristic(name) {
  return (
    name.includes('react-native') ||
    name.startsWith('expo-') ||
    name.startsWith('@expo/')
  );
}

function normalizePlugins(plugins) {
  return plugins.map((entry) => {
    if (typeof entry === 'string') return entry;
    if (Array.isArray(entry) && typeof entry[0] === 'string') return entry[0];
    return null;
  });
}

function getInstalledVersion(name, inputs) {
  const lockEntry = inputs.mobileLock.packages?.[`node_modules/${name}`];
  if (lockEntry?.version) return lockEntry.version;
  const declared = inputs.mobilePackage.dependencies?.[name] || inputs.mobilePackage.devDependencies?.[name];
  return declared ? declared.replace(/^[~^]/, '') : null;
}

function readJson(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exit(runGenerateSDKManifest({ checkOnly: process.argv.includes('--check') }));
}
