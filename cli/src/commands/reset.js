const { discoverDevice } = require('../discovery');
const { resetDevice } = require('../transport');

async function reset(options = {}) {
  console.log('📡 Finding device...');
  const device = await discoverDevice(options.device);
  console.log(`✅ Found: ${device.name} (${device.ip})`);

  console.log('🔄 Resetting device...');
  const result = await resetDevice(device);

  if (result.status === 'ok') {
    console.log('✅ Bundle cleared — device will show default UI');
  } else {
    console.error(`❌ Reset failed: ${result.message}`);
    process.exit(1);
  }
}

module.exports = { reset };
