const fs = require('fs');
const { analyzeProject } = require('../analyzer');

async function doctor() {
  if (!fs.existsSync('package.json')) {
    console.error('❌ No package.json found. Run this from your RN project root.');
    process.exit(1);
  }

  const pkg = JSON.parse(fs.readFileSync('package.json', 'utf8'));
  const sdkManifest = require('../../sdk-manifest.json');
  const analysis = analyzeProject(pkg, sdkManifest);

  console.log('\n📋 Yaver Compatibility Report\n');
  console.log(`  Yaver SDK:     v${sdkManifest.sdkVersion}`);
  console.log(`  SDK RN:        ${sdkManifest.reactNative}`);
  console.log(`  SDK Hermes BC: ${sdkManifest.hermes.bytecodeVersion}`);
  console.log(`  Your RN:       ${analysis.reactNativeVersion || 'not found'}`);
  console.log(`  New Arch:      ${sdkManifest.arch.newArch ? 'enabled' : 'disabled'}\n`);

  // Available modules
  if (analysis.availableModules.length > 0) {
    console.log('─── Available Native Modules ────────────────────\n');
    console.log('  These will work in yaver.io:\n');
    for (const m of analysis.availableModules) {
      const warn = analysis.warnings.find(w => w.module === m.name);
      if (warn) {
        console.log(`  ⚠️  ${m.name}: project ${m.projectVersion}, yaver ${m.sdkVersion}`);
      } else {
        console.log(`  ✅ ${m.name}@${m.projectVersion}`);
      }
    }
    console.log('');
  }

  // Missing modules
  if (analysis.missingModules.length > 0) {
    console.log('─── Missing Native Modules ─────────────────────\n');
    console.log('  These need native code that yaver.io doesn\'t ship.');
    console.log('  Your app WILL crash if it calls them.\n');

    for (const m of analysis.missingModules) {
      console.log(`  ❌ ${m.name}@${m.version}`);
    }

    console.log('\n  Handle gracefully in your existing code:\n');
    console.log('  import { NativeModules } from \'react-native\';');
    console.log('  const isYaver = !!NativeModules.YaverInfo;\n');
    console.log('  if (isYaver) {');
    console.log('    // skip this feature or show placeholder');
    console.log('  } else {');
    console.log('    // use the native module normally');
    console.log('  }\n');

    console.log('  For lazy-loaded modules (avoids import crash):\n');
    console.log('  const MyModule = NativeModules.MyModule');
    console.log('    ? require(\'react-native-my-module\').default');
    console.log('    : null;\n');
  }

  // Errors
  const hardErrors = analysis.errors.filter(e => e.type !== 'missing_module');
  if (hardErrors.length > 0) {
    console.log('─���─ Critical Issues ────────────────────────────\n');
    for (const e of hardErrors) {
      console.log(`  🚫 ${e.message}`);
    }
    console.log('');
  }

  // All SDK modules
  console.log('─── All Yaver SDK Modules ──────────────────────\n');
  for (const [name, version] of Object.entries(sdkManifest.nativeModules)) {
    const inProject = analysis.availableModules.find(m => m.name === name);
    console.log(`  ${name}@${version}${inProject ? ' ← used in your project' : ''}`);
  }
  console.log('');

  // Summary
  const total = analysis.availableModules.length + analysis.missingModules.length;
  console.log(`─── Summary ────────────────────────────────────\n`);
  console.log(`  ${analysis.availableModules.length}/${total} native modules available`);
  console.log(`  ${analysis.missingModules.length} missing (push with --ignore-missing)`);
  console.log(`  ${analysis.warnings.length} warnings`);
  console.log(`  ${hardErrors.length} critical issues\n`);
}

module.exports = { doctor };
