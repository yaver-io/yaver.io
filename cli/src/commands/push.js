const fs = require('fs');
const path = require('path');
const { analyzeProject } = require('../analyzer');
const { bundle, compileHermes, readBytecodeVersion } = require('../bundler');
const { discoverDevice, fetchHealth } = require('../discovery');
const { loadSDKManifest } = require('../sdk-manifest');
const { pushBundle, pushAssets } = require('../transport');

async function push(options = {}) {
  const startTime = Date.now();
  const quiet = options.quiet;
  const pkg = JSON.parse(fs.readFileSync('package.json', 'utf8'));
  const sdkManifest = loadSDKManifest();

  // Find device
  if (!quiet) console.log('📡 Finding device...');
  const device = await discoverDevice(options.device);
  if (!quiet) console.log(`✅ Found: ${device.name} (${device.ip})`);

  // Fetch health + verify SDK
  const health = await fetchHealth(device);
  const platform = health.platform || 'ios';

  if (health.hermes?.bytecodeVersion !== sdkManifest.hermes.bytecodeVersion) {
    console.error(`�� Hermes BC mismatch: device BC${health.hermes?.bytecodeVersion}, CLI BC${sdkManifest.hermes.bytecodeVersion}`);
    console.error('   Update yaver-cli or yaver.io app.');
    process.exit(1);
  }

  // Analyze compatibility
  const analysis = analyzeProject(pkg, sdkManifest);

  const hardErrors = analysis.errors.filter(e =>
    e.type === 'rn_major_mismatch' || e.type === 'arch_mismatch'
  );
  if (hardErrors.length > 0) {
    console.error('\n🚫 INCOMPATIBLE:\n');
    hardErrors.forEach(e => console.error(`  ${e.message}`));
    process.exit(1);
  }

  if (analysis.missingModules.length > 0 && !options.ignoreMissing) {
    console.warn(`\n⚠���  ${analysis.missingModules.length} native module(s) NOT in yaver SDK:`);
    analysis.missingModules.forEach(m => console.warn(`    • ${m.name}@${m.version}`));
    console.warn('\n  App will crash if it calls these modules.');
    console.warn('  Push anyway: yaver-push push --ignore-missing\n');
    if (!options.force) process.exit(1);
  }

  if (!quiet) console.log('✅ Compatible');

  // Bundle
  if (!quiet) console.log(`🔨 Bundling for ${platform}...`);
  const entryFile = findEntryFile(pkg);
  const buildDir = path.resolve('.yaver-build');
  const bundlePath = await bundle({ platform, entryFile, outputDir: buildDir, dev: false, minify: true });

  // Hermes compile
  if (!quiet) console.log('⚡ Compiling Hermes bytecode...');
  await compileHermes({ inputPath: bundlePath, outputPath: bundlePath });

  const bcVersion = readBytecodeVersion(bundlePath);
  if (bcVersion !== sdkManifest.hermes.bytecodeVersion) {
    console.error(`❌ hermesc produced BC${bcVersion}, expected BC${sdkManifest.hermes.bytecodeVersion}`);
    process.exit(1);
  }

  // Push
  const moduleName = getModuleName(pkg);
  const bundleData = fs.readFileSync(bundlePath);
  if (!quiet) console.log(`📤 Pushing ${(bundleData.length / 1024).toFixed(1)} KB...`);

  const result = await pushBundle(device, bundleData, {
    moduleName,
    appName: moduleName,
    sdkVersion: sdkManifest.sdkVersion,
  });

  if (result.status !== 'ok') {
    console.error(`❌ Device rejected: ${result.message}`);
    process.exit(1);
  }

  // Assets
  const assetsDir = path.join(buildDir, 'assets');
  if (fs.existsSync(assetsDir)) {
    const files = fs.readdirSync(assetsDir, { recursive: true });
    if (files.length > 0) {
      if (!quiet) console.log('📤 Pushing assets...');
      await pushAssets(device, assetsDir);
    }
  }

  const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
  if (!quiet) console.log(`\n🚀 Done in ${elapsed}s — app loading on ${device.name}\n`);

  // Watch mode
  if (options.watch) {
    await watchAndPush(options, device, pkg, sdkManifest);
  }
}

async function watchAndPush(options, device, pkg, sdkManifest) {
  console.log('👀 Watching for changes...');

  const watchDirs = ['src', 'app', 'lib', 'components', 'screens', 'utils', 'hooks']
    .filter(d => fs.existsSync(d));

  if (watchDirs.length === 0) watchDirs.push('.');

  const { watch } = require('fs');
  let debounce = null;

  for (const dir of watchDirs) {
    fs.watch(dir, { recursive: true }, (event, filename) => {
      if (!filename || !filename.match(/\.(js|jsx|ts|tsx|json)$/)) return;
      if (filename.includes('node_modules') || filename.includes('.yaver-build')) return;

      clearTimeout(debounce);
      debounce = setTimeout(async () => {
        console.log(`📝 ${filename} changed`);
        try {
          await push({ ...options, watch: false, quiet: true, device: device.ip });
          console.log('📤 Re-pushed — done');
        } catch (err) {
          console.error(`❌ Re-push failed: ${err.message}`);
        }
      }, 300);
    });
  }
}

function findEntryFile(pkg) {
  if (pkg.main && fs.existsSync(pkg.main)) return pkg.main;
  const candidates = ['index.js', 'index.tsx', 'index.ts', 'src/index.js', 'src/index.tsx', 'App.js', 'App.tsx'];
  return candidates.find(f => fs.existsSync(f)) || 'index.js';
}

function getModuleName(pkg) {
  if (fs.existsSync('app.json')) {
    try {
      const a = JSON.parse(fs.readFileSync('app.json', 'utf8'));
      if (a.name) return a.name;
      if (a.displayName) return a.displayName;
      if (a.expo?.name) return a.expo.name;
    } catch {}
  }
  return pkg.name || 'App';
}

module.exports = { push };
