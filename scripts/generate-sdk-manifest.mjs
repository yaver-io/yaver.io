import fs from 'fs';
import path from 'path';
import process from 'process';

const repoRoot = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const config = readJson(path.join(repoRoot, 'scripts', 'sdk-manifest.config.json'));
const mobilePackage = readJson(path.join(repoRoot, 'mobile', 'package.json'));
const mobileAppConfig = readJson(path.join(repoRoot, 'mobile', 'app.json'));
const mobileLock = readJson(path.join(repoRoot, 'mobile', 'package-lock.json'));

const outputPaths = [
  path.join(repoRoot, 'mobile', 'sdk-manifest.json'),
  path.join(repoRoot, 'cli', 'sdk-manifest.json'),
  path.join(repoRoot, 'mobile', 'ios', 'sdk-manifest.json'),
  path.join(repoRoot, 'mobile', 'ios', 'Yaver', 'sdk-manifest.json'),
];

const pluginPackages = new Set(
  normalizePlugins(mobileAppConfig.expo?.plugins || []).filter(Boolean)
);

const dependencyNames = Object.keys(mobilePackage.dependencies || {});
const nativeModuleNames = dependencyNames
  .filter((name) => isNativeModule(name, config.manualNativeModules))
  .sort((a, b) => a.localeCompare(b));

const nativeModules = {};
const moduleSupport = {};

for (const name of nativeModuleNames) {
  const version = getInstalledVersion(name);
  if (!version) {
    throw new Error(`Missing installed version for ${name} in mobile/package-lock.json`);
  }
  nativeModules[name] = version;
  moduleSupport[name] = {
    version,
    ...config.moduleSupportDefaults,
    pluginEnabled: pluginPackages.has(name),
  };
}

const manifest = {
  sdkVersion: config.sdkVersion,
  reactNative: getInstalledVersion('react-native'),
  react: getInstalledVersion('react'),
  hermes: {
    version: getInstalledVersion('react-native'),
    bytecodeVersion: config.hermesBytecodeVersion,
  },
  arch: config.arch,
  supportedRNRange: config.supportedRNRange,
  nativeModules,
  moduleSupport,
};

const serialized = `${JSON.stringify(manifest, null, 2)}\n`;
const checkOnly = process.argv.includes('--check');

const driftedPaths = [];
for (const outputPath of outputPaths) {
  if (checkOnly) {
    const existing = fs.existsSync(outputPath) ? fs.readFileSync(outputPath, 'utf8') : '';
    if (existing !== serialized) {
      driftedPaths.push(path.relative(repoRoot, outputPath));
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
    process.exit(1);
  }
  console.log(`sdk-manifest is in sync across ${outputPaths.length} outputs.`);
  process.exit(0);
}

console.log(
  `Generated sdk-manifest (${Object.keys(nativeModules).length} native modules) -> ${outputPaths
    .map((p) => path.relative(repoRoot, p))
    .join(', ')}`
);

function isNativeModule(name, manualNativeModules) {
  if (manualNativeModules.includes(name)) return true;
  return (
    name.startsWith('expo-') ||
    name.startsWith('react-native-') ||
    name.startsWith('@react-native-') ||
    name.startsWith('@react-native/') ||
    name.startsWith('@shopify/react-native-')
  );
}

function normalizePlugins(plugins) {
  return plugins.map((entry) => {
    if (typeof entry === 'string') return entry;
    if (Array.isArray(entry) && typeof entry[0] === 'string') return entry[0];
    return null;
  });
}

function getInstalledVersion(name) {
  const lockEntry = mobileLock.packages?.[`node_modules/${name}`];
  if (lockEntry?.version) return lockEntry.version;
  const declared = mobilePackage.dependencies?.[name] || mobilePackage.devDependencies?.[name];
  return declared ? declared.replace(/^[~^]/, '') : null;
}

function readJson(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}
