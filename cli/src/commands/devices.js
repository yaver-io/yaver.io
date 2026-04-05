const { scanLAN, fetchHealth, YAVER_PORT } = require('../discovery');

async function devices() {
  console.log('📡 Scanning for yaver.io devices on your network...\n');

  const found = await scanLAN();

  if (found.length === 0) {
    console.log('  No devices found.\n');
    console.log('  Make sure the yaver.io app is open on your phone');
    console.log('  and both devices are on the same WiFi network.\n');
    return;
  }

  console.log(`  Found ${found.length} device(s):\n`);
  for (const d of found) {
    console.log(`  📱 ${d.name} (${d.platform})`);
    console.log(`     IP: ${d.ip}:${d.port}`);
    console.log(`     Push: yaver-push push --device ${d.ip}\n`);
  }
}

module.exports = { devices };
