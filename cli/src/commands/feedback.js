const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const FEEDBACK_HELP = `
yaver feedback — install the Yaver Feedback SDK into the current project

Commands:
  init [--platform <p>]   Install the right feedback SDK for this project
                          Platforms: web, react-native, expo, flutter
                          (autodetected from package.json / pubspec.yaml)

The web and mobile SDKs are separate packages on purpose:
  yaver-feedback-web              — browsers (screen recording, voice, shake-free trigger)
  yaver-feedback-react-native     — RN / Expo (shake-to-report, BlackBox, native recording)
  yaver_feedback                  — Flutter (NavigatorObserver, Flutter error handler)

Other feedback subcommands (setup, list, etc.) are forwarded to the Go agent.
`;

function detectPackageManager(dir) {
  if (fs.existsSync(path.join(dir, 'bun.lockb')) || fs.existsSync(path.join(dir, 'bun.lock'))) return 'bun';
  if (fs.existsSync(path.join(dir, 'pnpm-lock.yaml'))) return 'pnpm';
  if (fs.existsSync(path.join(dir, 'yarn.lock'))) return 'yarn';
  return 'npm';
}

function jsInstallCommand(pm, pkg) {
  switch (pm) {
    case 'bun':  return ['bun',  ['add', pkg]];
    case 'pnpm': return ['pnpm', ['add', pkg]];
    case 'yarn': return ['yarn', ['add', pkg]];
    default:     return ['npm',  ['install', pkg]];
  }
}

function detectPlatform(dir) {
  if (fs.existsSync(path.join(dir, 'pubspec.yaml'))) return 'flutter';

  const pkgPath = path.join(dir, 'package.json');
  if (!fs.existsSync(pkgPath)) return null;

  let pkg;
  try {
    pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
  } catch {
    return null;
  }
  const deps = { ...(pkg.dependencies || {}), ...(pkg.devDependencies || {}) };

  if (deps.expo) return 'expo';
  if (deps['react-native']) return 'react-native';
  if (deps.next || deps.vite || deps.react || deps.vue || deps.svelte || deps['@angular/core']) return 'web';

  return null;
}

function nextSteps(platform) {
  switch (platform) {
    case 'web':
      return [
        'Call YaverFeedback.init({ trigger: "floating-button" }) in your app entry (dev mode only).',
        'On first click, the SDK prompts the user to sign in via its built-in modal',
        '(Apple / Google / GitHub / GitLab / Microsoft / email).',
        'Make sure `yaver serve` is running on your dev machine so feedback has somewhere to land.',
      ];
    case 'expo':
    case 'react-native':
      return [
        'Wrap your dev app with YaverFeedback.init(...) in a debug-only entry block.',
        'Add <FeedbackModal /> (or the floating button) to your root component.',
        'Shake the device / simulator to trigger the report sheet.',
      ];
    case 'flutter':
      return [
        'Call YaverFeedback.init(...) in main() before runApp().',
        'Add the YaverFeedback overlay or button to your app root.',
        'Use wrapFlutterErrorHandler() if you want automatic error capture.',
      ];
    default:
      return [];
  }
}

function platformPackage(platform) {
  switch (platform) {
    case 'web':          return { pm: null, pkg: 'yaver-feedback-web' };
    case 'expo':
    case 'react-native': return { pm: null, pkg: 'yaver-feedback-react-native' };
    case 'flutter':      return { pm: 'flutter', pkg: 'yaver_feedback' };
    default:             return null;
  }
}

function parsePlatformFlag(args) {
  const i = args.indexOf('--platform');
  if (i >= 0 && args[i + 1]) return args[i + 1];
  return null;
}

async function feedbackInit(args) {
  const dir = process.cwd();
  let platform = parsePlatformFlag(args);
  const autodetected = !platform;
  if (!platform) platform = detectPlatform(dir);

  if (!platform) {
    console.error('❌ Could not detect a supported project in this directory.');
    console.error('   Run from a folder with package.json (web / RN / Expo) or pubspec.yaml (Flutter).');
    console.error('   Or force it: yaver feedback init --platform <web|react-native|expo|flutter>');
    process.exit(1);
  }

  const target = platformPackage(platform);
  if (!target) {
    console.error(`❌ Unknown platform: ${platform}`);
    console.error('   Supported: web, react-native, expo, flutter');
    process.exit(1);
  }

  if (autodetected) {
    console.log(`🔍 Detected platform: ${platform}`);
  }

  let cmd, cmdArgs;
  if (target.pm === 'flutter') {
    cmd = 'flutter';
    cmdArgs = ['pub', 'add', target.pkg];
  } else {
    const pm = detectPackageManager(dir);
    [cmd, cmdArgs] = jsInstallCommand(pm, target.pkg);
    console.log(`📦 Package manager: ${pm}`);
  }

  console.log(`➡️  ${cmd} ${cmdArgs.join(' ')}\n`);

  const res = spawnSync(cmd, cmdArgs, { stdio: 'inherit', cwd: dir });
  if (res.error) {
    console.error(`\n❌ Failed to run ${cmd}: ${res.error.message}`);
    process.exit(1);
  }
  if (res.status !== 0) {
    process.exit(res.status || 1);
  }

  console.log(`\n✅ Installed ${target.pkg}\n`);
  const steps = nextSteps(platform);
  if (steps.length > 0) {
    console.log('Next steps:');
    for (const s of steps) console.log(`  • ${s}`);
    console.log('');
  }
}

async function feedback(args) {
  const sub = args[0];

  if (!sub || sub === '--help' || sub === '-h' || sub === 'help') {
    console.log(FEEDBACK_HELP);
    process.exit(0);
  }

  if (sub === 'init') {
    await feedbackInit(args.slice(1));
    return;
  }

  // Forward everything else (setup, list, ...) to the Go agent.
  const { runAgentCommand } = require('../agent-runtime');
  await runAgentCommand(['feedback', ...args]);
}

module.exports = { feedback };
