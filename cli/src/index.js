const PACKAGE = require('../package.json');
const { resolveAgentInfo, runAgentCommand } = require('./agent-runtime');
const { init } = require('./commands/init');
const { push } = require('./commands/push');
const { doctor } = require('./commands/doctor');
const { devices } = require('./commands/devices');
const { modules } = require('./commands/modules');
const { reset } = require('./commands/reset');
const { status } = require('./commands/status');

const PUSH_HELP = `
yaver push — Push existing React Native projects to the Yaver mobile host

Commands:
  init                              Analyze project, show compatibility, create yaver.json
  push [--device <ip>] [--watch]    Bundle + validate + push to device
  push --ignore-missing             Push even with missing native modules
  push --bundle-only [--platform P] Bundle + Hermes compile without pushing (CI)
  doctor [--strict]                 Deep compatibility report (non-zero exit in --strict)
  devices                           List discovered devices
  modules                           List all SDK native modules
  reset [--device <ip>]             Clear pushed bundle on device
  status [--device <ip>]            Device + project status

Options:
  --device <ip>                     Target device IP (skips discovery)
  --watch                           Re-push on file save
  --ignore-missing                  Push despite missing native modules
  --force                           Skip confirmation prompts
  --help                            Show this help
`;

const UNIFIED_HELP = `
yaver — single npm install for the Go agent and the RN push CLI

Agent commands:
  yaver serve                       Start the Yaver agent (downloads platform binary if needed)
  yaver auth                        Sign in and configure the agent
  yaver version                     Print agent version
  yaver <agent-command>             Forward any other command to the Go agent

Push-to-device commands:
  yaver push                        Bundle + validate + push current RN/Expo project
  yaver push init                   Analyze current project and create yaver.json
  yaver push doctor                 Deep compatibility report for current project
  yaver push modules                List native modules compiled into the mobile host
  yaver push devices                Scan LAN for Yaver mobile hosts
  yaver push reset                  Clear pushed bundle on the selected device
  yaver push status                 Show project + device push status

Compatibility:
  yaver-push <command>              Legacy alias for the push subcommands above

Options:
  --help                            Show this help
`;

const PUSH_SUBCOMMANDS = new Set(['init', 'push', 'doctor', 'devices', 'modules', 'reset', 'status']);
const DIRECT_PUSH_ALIASES = new Map([
  ['init', 'init'],
  ['modules', 'modules'],
]);

async function runPushCli(args) {
  const command = args[0];
  const options = parseArgs(args.slice(1));

  if (!command || command === '--help' || command === '-h' || args.includes('--help') || args.includes('-h')) {
    console.log(PUSH_HELP);
    process.exit(0);
  }

  try {
    switch (command) {
      case 'init':
        await init(options);
        break;
      case 'push':
        await push(options);
        break;
      case 'doctor':
        await doctor(options);
        break;
      case 'devices':
        await devices(options);
        break;
      case 'modules':
        await modules(options);
        break;
      case 'reset':
        await reset(options);
        break;
      case 'status':
        await status(options);
        break;
      default:
        console.error(`Unknown command: ${command}`);
        console.log(PUSH_HELP);
        process.exit(1);
    }
  } catch (err) {
    console.error(`\n❌ ${err.message}`);
    if (process.env.YAVER_DEBUG) console.error(err.stack);
    process.exit(1);
  }
}

async function runUnified(args) {
  const command = args[0];

  if (!command || command === '--help' || command === '-h' || command === 'help') {
    console.log(UNIFIED_HELP);
    process.exit(0);
  }

  if (command === 'push') {
    const next = args[1];
    if (!next || next.startsWith('-')) {
      await runPushCli(['push', ...args.slice(1)]);
      return;
    }
    if (PUSH_SUBCOMMANDS.has(next)) {
      const subcommand = next === 'push' ? 'push' : next;
      await runPushCli([subcommand, ...args.slice(2)]);
      return;
    }
    console.error(`Unknown push subcommand: ${next}`);
    console.log(PUSH_HELP);
    process.exit(1);
  }

  if (DIRECT_PUSH_ALIASES.has(command)) {
    await runPushCli([DIRECT_PUSH_ALIASES.get(command), ...args.slice(1)]);
    return;
  }

  if (command === 'npm-agent-info') {
    const info = resolveAgentInfo();
    console.log(JSON.stringify(info, null, 2));
    return;
  }

  try {
    await runAgentCommand(args);
  } catch (err) {
    if (typeof err.exitCode === 'number') {
      process.exit(err.exitCode);
    }
    console.error(`\n❌ ${err.message}`);
    if (process.env.YAVER_DEBUG) console.error(err.stack);
    process.exit(1);
  }
}

function parseArgs(args) {
  const opts = {};
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === '--device' && args[i + 1]) {
      opts.device = args[++i];
    } else if (arg === '--watch') {
      opts.watch = true;
    } else if (arg === '--ignore-missing') {
      opts.ignoreMissing = true;
    } else if (arg === '--strict') {
      opts.strict = true;
    } else if (arg === '--bundle-only') {
      opts.bundleOnly = true;
    } else if (arg === '--platform' && args[i + 1]) {
      opts.platform = args[++i];
    } else if (arg === '--force') {
      opts.force = true;
    } else if (arg === '--quiet' || arg === '-q') {
      opts.quiet = true;
    }
  }
  return opts;
}

module.exports = { runPushCli, runUnified };
