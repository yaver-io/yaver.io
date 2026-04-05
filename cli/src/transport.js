const http = require('http');
const fs = require('fs');
const path = require('path');

const { YAVER_PORT } = require('./discovery');

/** Push a Hermes bytecode bundle to the device */
function pushBundle(device, bundleData, metadata = {}) {
  return new Promise((resolve, reject) => {
    const options = {
      hostname: device.ip,
      port: device.port || YAVER_PORT,
      path: '/bundle',
      method: 'POST',
      timeout: 30000,
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Length': bundleData.length,
        'X-Module-Name': metadata.moduleName || 'main',
        'X-App-Name': metadata.appName || '',
        'X-SDK-Version': metadata.sdkVersion || '',
      },
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try {
          const result = JSON.parse(data);
          if (res.statusCode !== 200) {
            reject(new Error(result.message || `HTTP ${res.statusCode}`));
          } else {
            resolve(result);
          }
        } catch {
          reject(new Error(`Invalid response from device: ${data}`));
        }
      });
    });

    req.on('error', (err) => reject(new Error(`Push failed: ${err.message}`)));
    req.on('timeout', () => {
      req.destroy();
      reject(new Error('Push timed out'));
    });

    req.write(bundleData);
    req.end();
  });
}

/** Push assets directory to the device */
async function pushAssets(device, assetsDir) {
  // For now, send individual files. A tar approach would be more efficient.
  const files = getAllFiles(assetsDir);
  if (files.length === 0) return;

  // Concatenate all assets into a simple tar-like format
  // The device will unpack them
  const buffers = [];
  for (const file of files) {
    const relPath = path.relative(assetsDir, file);
    const data = fs.readFileSync(file);
    buffers.push(data);
  }

  const allData = Buffer.concat(buffers);

  return new Promise((resolve, reject) => {
    const options = {
      hostname: device.ip,
      port: device.port || YAVER_PORT,
      path: '/assets',
      method: 'POST',
      timeout: 30000,
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Length': allData.length,
      },
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try { resolve(JSON.parse(data)); } catch { resolve({}); }
      });
    });

    req.on('error', (err) => reject(new Error(`Asset push failed: ${err.message}`)));
    req.write(allData);
    req.end();
  });
}

/** Send POST /reset to device */
function resetDevice(device) {
  return new Promise((resolve, reject) => {
    const options = {
      hostname: device.ip,
      port: device.port || YAVER_PORT,
      path: '/reset',
      method: 'POST',
      timeout: 10000,
      headers: { 'Content-Length': 0 },
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try { resolve(JSON.parse(data)); } catch { resolve({}); }
      });
    });

    req.on('error', (err) => reject(new Error(`Reset failed: ${err.message}`)));
    req.end();
  });
}

function getAllFiles(dir) {
  const results = [];
  if (!fs.existsSync(dir)) return results;
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results.push(...getAllFiles(full));
    } else {
      results.push(full);
    }
  }
  return results;
}

module.exports = { pushBundle, pushAssets, resetDevice };
