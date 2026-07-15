const http = require('http');
const dgram = require('dgram');

const YAVER_PORT = 8347;
const BEACON_PORT = 19837;
const DISCOVERY_TIMEOUT = 1500;
const HEALTH_TIMEOUT = 1200;
const LAN_SCAN_TIMEOUT = 350;

/**
 * Discover a yaver.io device on the network.
 * Priority: 1) manual IP, 2) saved config, 3) UDP beacon scan, 4) mDNS
 */
async function discoverDevice(manualIp) {
  if (manualIp) {
    const health = await fetchHealth({ ip: manualIp, port: YAVER_PORT });
    return { ip: manualIp, port: YAVER_PORT, name: health.deviceName || manualIp, platform: health.platform };
  }

  // Try UDP beacon discovery
  const beaconDevice = await listenForBeacon(DISCOVERY_TIMEOUT);
  if (beaconDevice) {
    return beaconDevice;
  }

  throw new Error(
    'No yaver.io device found on network.\n' +
    '  Make sure the yaver.io app is open on your phone (same WiFi).\n' +
    '  Or specify device IP: yaver push --device <ip>'
  );
}

/** Listen for UDP beacon from yaver.io app */
function listenForBeacon(timeout) {
  return new Promise((resolve) => {
    const socket = dgram.createSocket({ type: 'udp4', reuseAddr: true });
    const timer = setTimeout(() => {
      socket.close();
      resolve(null);
    }, timeout);

    socket.on('message', (msg) => {
      try {
        const beacon = JSON.parse(msg.toString());
        if (beacon.v === 1 && beacon.p) {
          clearTimeout(timer);
          socket.close();
          resolve({
            ip: beacon.ip || socket.remoteAddress,
            port: beacon.p,
            name: beacon.n || 'Unknown Device',
            id: beacon.id,
          });
        }
      } catch {}
    });

    socket.on('error', () => {
      clearTimeout(timer);
      socket.close();
      resolve(null);
    });

    socket.bind(BEACON_PORT, () => {
      socket.setBroadcast(true);
    });
  });
}

/** Fetch /health from a device */
function fetchHealth(device, timeout = HEALTH_TIMEOUT) {
  return new Promise((resolve, reject) => {
    const url = `http://${device.ip}:${device.port || YAVER_PORT}/health`;
    const req = http.get(url, { timeout }, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try {
          resolve(JSON.parse(data));
        } catch {
          reject(new Error(`Invalid JSON from ${url}`));
        }
      });
    });
    req.on('error', (err) => reject(new Error(`Cannot reach ${device.ip}:${device.port || YAVER_PORT} — ${err.message}`)));
    req.on('timeout', () => {
      req.destroy();
      reject(new Error(`Timeout connecting to ${device.ip}:${device.port || YAVER_PORT}`));
    });
  });
}

/** Scan common LAN subnets for yaver.io devices */
async function scanLAN() {
  const os = require('os');
  const interfaces = os.networkInterfaces();
  const found = [];
  const seen = new Set();

  for (const [, addrs] of Object.entries(interfaces)) {
    for (const addr of addrs) {
      if (addr.family !== 'IPv4' || addr.internal) continue;
      // Try common IPs on this subnet
      const subnet = addr.address.split('.').slice(0, 3).join('.');
      const ips = [];
      for (let i = 1; i <= 254; i++) {
        const ip = `${subnet}.${i}`;
        if (ip === addr.address) continue;
        ips.push(ip);
      }
      await runLimited(ips, 48, async (ip) => {
        try {
          const h = await fetchHealth({ ip, port: YAVER_PORT }, LAN_SCAN_TIMEOUT);
          const key = h.deviceId || `${ip}:${YAVER_PORT}`;
          if (seen.has(key)) return;
          seen.add(key);
          found.push({ ip, port: YAVER_PORT, name: h.deviceName, platform: h.platform });
        } catch {}
      });
    }
  }

  return found;
}

async function runLimited(items, limit, fn) {
  let next = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    for (;;) {
      const idx = next++;
      if (idx >= items.length) return;
      await fn(items[idx]);
    }
  });
  await Promise.all(workers);
}

module.exports = { discoverDevice, fetchHealth, scanLAN, YAVER_PORT };
