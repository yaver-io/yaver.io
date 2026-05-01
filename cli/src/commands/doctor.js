const fs = require('fs');
const path = require('path');
const dgram = require('dgram');
const { execSync } = require('child_process');
const { analyzeProject } = require('../analyzer');
const { loadSDKManifest } = require('../sdk-manifest');

// commandExists checks PATH for an executable. Matches the helper used
// by postinstall.js — kept inline to avoid a new module dependency.
function commandExists(name) {
  try {
    execSync(`command -v ${name}`, { stdio: ['ignore', 'pipe', 'ignore'] });
    return true;
  } catch (_) {
    return false;
  }
}

// stunPing sends one STUN binding request to the given host:port and
// resolves true if a response comes back within `timeoutMs`. Used to
// verify WebRTC ICE connectivity during `yaver doctor`. No external
// libraries required — STUN binding requests are 20 bytes.
function stunPing(host, port, timeoutMs = 1500) {
  return new Promise((resolve) => {
    const sock = dgram.createSocket('udp4');
    let done = false;
    const finish = (ok) => {
      if (done) return;
      done = true;
      try { sock.close(); } catch (_) {}
      resolve(ok);
    };
    const timer = setTimeout(() => finish(false), timeoutMs);
    sock.on('message', () => {
      clearTimeout(timer);
      finish(true);
    });
    sock.on('error', () => {
      clearTimeout(timer);
      finish(false);
    });
    // STUN binding request (RFC 5389): 0x0001, length 0, magic cookie,
    // 12-byte transaction ID. Total: 20 bytes.
    const buf = Buffer.alloc(20);
    buf.writeUInt16BE(0x0001, 0); // message type: binding request
    buf.writeUInt16BE(0x0000, 2); // length
    buf.writeUInt32BE(0x2112A442, 4); // magic cookie
    for (let i = 0; i < 12; i++) buf[8 + i] = Math.floor(Math.random() * 256);
    sock.send(buf, port, host, (err) => {
      if (err) {
        clearTimeout(timer);
        finish(false);
      }
    });
  });
}

// reportWebRTCReadiness prints a one-block summary of WebRTC bootstrap
// state: ffmpeg (mobile container frame encoding), Yaver agent binary
// (Pion server-side), and STUN reachability for ICE. Each line is
// independently actionable.
async function reportWebRTCReadiness() {
  console.log('─── WebRTC + Streaming Readiness ──────────────\n');

  const ffmpeg = commandExists('ffmpeg');
  console.log(`  ${ffmpeg ? '✅' : '❌'} ffmpeg ${ffmpeg ? 'installed' : 'missing — needed for screen capture transcode (auto-installed via `yaver install vibe-preview`)'}`);

  const yaverBin = commandExists('yaver') || commandExists('yaver.exe');
  console.log(`  ${yaverBin ? '✅' : '❌'} yaver agent binary ${yaverBin ? 'on PATH (embeds Pion WebRTC server)' : 'not on PATH'}`);

  // Best-effort STUN ping to Google's public STUN server. Confirms UDP
  // egress works — without it, ICE candidate gathering fails and the
  // WebRTC stream falls back to relay-jpeg-poll.
  const stunHost = 'stun.l.google.com';
  const stunPort = 19302;
  process.stdout.write(`  ⏳ STUN reachability (${stunHost}:${stunPort})…`);
  const stunOk = await stunPing(stunHost, stunPort, 1500);
  process.stdout.write('\r');
  console.log(`  ${stunOk ? '✅' : '⚠️ '} STUN ${stunOk ? 'reachable' : 'unreachable — ICE may fail; configure TURN on your relay (relay/deploy/coturn.conf, when added)'} (${stunHost}:${stunPort})`);

  // Note about TURN. Yaver doesn't ship coturn yet (planned in the
  // Phase 2 WebRTC plan). When the user has a managed relay running
  // coturn, `yaver relay test` will surface its TURN port.
  console.log('  ℹ️  TURN: configured per-relay. `yaver relay test` to verify your relay\'s TURN port.');
  console.log('');
}

async function doctor(options = {}) {
  if (!fs.existsSync('package.json')) {
    console.error('❌ No package.json found. Run this from your RN project root.');
    process.exit(1);
  }

  const pkg = JSON.parse(fs.readFileSync('package.json', 'utf8'));
  const sdkManifest = loadSDKManifest();
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

  // WebRTC + streaming readiness — opt-out with --no-webrtc so CI
  // doesn't pay the 1.5s STUN probe per run. Default on.
  if (options.webrtc !== false) {
    await reportWebRTCReadiness();
  }

  // Summary
  const total = analysis.availableModules.length + analysis.missingModules.length;
  console.log(`─── Summary ────────────────────────────────────\n`);
  console.log(`  ${analysis.availableModules.length}/${total} native modules available`);
  console.log(`  ${analysis.missingModules.length} missing (push with --ignore-missing)`);
  console.log(`  ${analysis.warnings.length} warnings`);
  console.log(`  ${hardErrors.length} critical issues\n`);

  if (options.strict) {
    const failCount = hardErrors.length + analysis.missingModules.length;
    if (failCount > 0) {
      console.error(`❌ doctor --strict: ${hardErrors.length} critical issue(s), ${analysis.missingModules.length} missing module(s)`);
      process.exit(1);
    }
    console.log('✅ doctor --strict: project is Yaver-compatible');
  }
}

module.exports = { doctor };
