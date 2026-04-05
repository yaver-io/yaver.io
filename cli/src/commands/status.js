const fs = require('fs');
const { discoverDevice, fetchHealth } = require('../discovery');

async function status(options = {}) {
  // Project status
  console.log('\n📋 Project Status\n');

  if (fs.existsSync('yaver.json')) {
    const yj = JSON.parse(fs.readFileSync('yaver.json', 'utf8'));
    console.log(`  SDK Version:   ${yj.sdkVersion}`);
    console.log(`  Project RN:    ${yj.projectRN}`);
    console.log(`  Compatible:    ${yj.compatible ? '✅' : '❌'}`);
    console.log(`  Available:     ${yj.availableModules?.length || 0} native modules`);
    console.log(`  Missing:       ${yj.missingModules?.length || 0} native modules`);
    console.log(`  Analyzed:      ${yj.analyzedAt}`);
  } else {
    console.log('  No yaver.json found. Run: yaver-push init');
  }

  // Device status
  console.log('\n📱 Device Status\n');

  try {
    const device = await discoverDevice(options.device);
    const health = await fetchHealth(device);

    console.log(`  Name:          ${health.deviceName || device.name}`);
    console.log(`  Platform:      ${health.platform}`);
    console.log(`  IP:            ${device.ip}:${device.port}`);
    console.log(`  SDK Version:   ${health.sdkVersion}`);
    console.log(`  RN Version:    ${health.reactNative}`);
    console.log(`  Hermes BC:     ${health.hermes?.bytecodeVersion}`);
    console.log(`  Has Bundle:    ${health.hasBundle ? '✅' : '❌'}`);
    console.log(`  App Version:   ${health.appVersion} (build ${health.build})`);
  } catch (err) {
    console.log(`  Not connected: ${err.message}`);
  }

  console.log('');
}

module.exports = { status };
