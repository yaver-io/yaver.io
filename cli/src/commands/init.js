const fs = require('fs');
const path = require('path');
const { analyzeProject } = require('../analyzer');

async function init() {
  if (!fs.existsSync('package.json')) {
    console.error('❌ No package.json found. Run this from your RN project root.');
    process.exit(1);
  }

  const pkg = JSON.parse(fs.readFileSync('package.json', 'utf8'));
  if (!pkg.dependencies?.['react-native']) {
    console.error('❌ react-native not found in dependencies. Is this a React Native project?');
    process.exit(1);
  }

  console.log('🔍 Analyzing your project...\n');

  const sdkManifest = require('../../sdk-manifest.json');
  const analysis = analyzeProject(pkg, sdkManifest);

  printAnalysis(analysis, sdkManifest);

  // Create yaver.json — does NOT touch the developer's code
  const yaverJson = {
    sdkVersion: sdkManifest.sdkVersion,
    analyzedAt: new Date().toISOString(),
    projectRN: analysis.reactNativeVersion,
    compatible: analysis.errors.length === 0,
    missingModules: analysis.missingModules.map(m => m.name),
    availableModules: analysis.availableModules.map(m => m.name),
    warnings: analysis.warnings.map(w => w.message),
  };

  fs.writeFileSync('yaver.json', JSON.stringify(yaverJson, null, 2));
  console.log('\n✅ Created yaver.json');

  if (analysis.errors.filter(e => e.type === 'missing_module').length > 0) {
    console.log('\n⚠️  Your project has compatibility issues (see above).');
    console.log('   You can still push with: yaver-push push --ignore-missing');
    console.log('   Features using missing modules will crash.\n');
  } else if (analysis.errors.length > 0) {
    console.log('\n🚫 Incompatible — see errors above.\n');
  } else {
    console.log('\n🎉 Fully compatible! Run: yaver-push push\n');
  }
}

function printAnalysis(analysis, sdkManifest) {
  // RN version
  const rnStatus = analysis.errors.find(e => e.type === 'rn_major_mismatch') ? '❌' :
    analysis.warnings.find(w => w.type === 'rn_minor_mismatch') ? '⚠️' : '✅';
  console.log(`  React Native:  ${analysis.reactNativeVersion || 'unknown'} ${rnStatus} (yaver supports ${sdkManifest.supportedRNRange})`);

  // Hermes
  console.log(`  Hermes:        enabled ✅`);

  // Arch
  const archStatus = analysis.errors.find(e => e.type === 'arch_mismatch') ? '❌' : '✅';
  console.log(`  New Arch:      ${sdkManifest.arch.newArch ? 'enabled' : 'disabled'} ${archStatus}`);

  // Available modules
  if (analysis.availableModules.length > 0) {
    console.log('\n  Native modules found in your project:');
    for (const m of analysis.availableModules) {
      const warn = analysis.warnings.find(w => w.module === m.name);
      const icon = warn ? '⚠️' : '✅';
      console.log(`    ${m.name}@${m.projectVersion}  ${icon} available in yaver`);
    }
  }

  // Missing modules
  if (analysis.missingModules.length > 0) {
    console.log(`\n  ⚠️  ${analysis.missingModules.length} native module(s) NOT available in yaver.io:`);
    for (const m of analysis.missingModules) {
      console.log(`    • ${m.name}@${m.version} — ${m.reason}`);
    }
  }

  // Warnings
  const otherWarnings = analysis.warnings.filter(w => w.type !== 'rn_minor_mismatch' && w.type !== 'version_mismatch');
  if (otherWarnings.length > 0) {
    console.log('\n  Warnings:');
    for (const w of otherWarnings) {
      console.log(`    ⚠️  ${w.message}`);
    }
  }
}

module.exports = { init };
