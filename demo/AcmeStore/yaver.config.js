/**
 * Yaver SDK build-time config generator.
 *
 * Runs before metro starts (via npm prestart script).
 * Reads ~/.yaver/config.json and writes a static JSON
 * that the SDK can import at runtime.
 *
 * Usage: node yaver.config.js
 * Output: src/yaver-sdk/config.generated.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const configPath = path.join(os.homedir(), '.yaver', 'config.json');
const outputPath = path.join(__dirname, 'src', 'yaver-sdk', 'config.generated.json');

let authToken = '';
let convexUrl = '';

try {
  const raw = fs.readFileSync(configPath, 'utf8');
  const cfg = JSON.parse(raw);
  authToken = cfg.auth_token || '';
  convexUrl = cfg.convex_site_url || '';
} catch (err) {
  console.warn('[yaver.config.js] No ~/.yaver/config.json — run `yaver auth` first');
}

// Discover local IP for direct LAN connection
let localIP = '';
const interfaces = os.networkInterfaces();
for (const name of Object.keys(interfaces)) {
  for (const iface of interfaces[name] || []) {
    if (iface.family === 'IPv4' && !iface.internal) {
      localIP = iface.address;
      break;
    }
  }
  if (localIP) break;
}

const config = {
  authToken,
  convexUrl,
  agentUrl: localIP ? `http://${localIP}:18080` : '',
};

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, JSON.stringify(config, null, 2));
console.log(`[yaver.config.js] Generated SDK config → ${config.agentUrl}`);
