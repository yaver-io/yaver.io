async function modules() {
  const sdkManifest = require('../../sdk-manifest.json');

  console.log(`\n📦 Yaver SDK v${sdkManifest.sdkVersion} — Native Modules\n`);
  console.log(`  React Native: ${sdkManifest.reactNative}`);
  console.log(`  Hermes BC:    ${sdkManifest.hermes.bytecodeVersion}`);
  console.log(`  New Arch:     ${sdkManifest.arch.newArch ? 'enabled' : 'disabled'}\n`);

  const entries = Object.entries(sdkManifest.nativeModules);

  // Group by prefix
  const expo = entries.filter(([n]) => n.startsWith('expo-'));
  const rn = entries.filter(([n]) => n.startsWith('react-native-') || n.startsWith('@react-native'));
  const other = entries.filter(([n]) => !n.startsWith('expo-') && !n.startsWith('react-native-') && !n.startsWith('@react-native'));

  if (rn.length > 0) {
    console.log('  React Native Community:');
    for (const [name, version] of rn) {
      console.log(`    ${name}@${version}`);
    }
    console.log('');
  }

  if (expo.length > 0) {
    console.log('  Expo Modules:');
    for (const [name, version] of expo) {
      console.log(`    ${name}@${version}`);
    }
    console.log('');
  }

  if (other.length > 0) {
    console.log('  Other:');
    for (const [name, version] of other) {
      console.log(`    ${name}@${version}`);
    }
    console.log('');
  }

  console.log(`  Total: ${entries.length} native modules\n`);
}

module.exports = { modules };
