const { init } = require('./commands/init');
const { push } = require('./commands/push');
const { doctor } = require('./commands/doctor');
const { devices } = require('./commands/devices');
const { modules } = require('./commands/modules');
const { reset } = require('./commands/reset');
const { status } = require('./commands/status');

const HELP = `
yaver-push — Push existing React Native projects to yaver.io

Commands:
  init                              Analyze project, show compatibility, create yaver.json
  push [--device <ip>] [--watch]    Bundle + validate + push to device
  push --ignore-missing             Push even with missing native modules
  doctor                            Deep compatibility report with fix suggestions
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

async function run(args) {
  const command = args[0];
  const options = parseArgs(args.slice(1));

  if (!command || command === '--help' || command === '-h') {
    console.log(HELP);
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
        console.log(HELP);
        process.exit(1);
    }
  } catch (err) {
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
    } else if (arg === '--force') {
      opts.force = true;
    } else if (arg === '--quiet' || arg === '-q') {
      opts.quiet = true;
    }
  }
  return opts;
}

module.exports = { run };
