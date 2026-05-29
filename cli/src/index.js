const PACKAGE = require('../package.json');
const { resolveAgentInfo, runAgentCommand } = require('./agent-runtime');
const { init } = require('./commands/init');
const { push } = require('./commands/push');
const { doctor } = require('./commands/doctor');
const { devices } = require('./commands/devices');
const { modules } = require('./commands/modules');
const { reset } = require('./commands/reset');
const { status } = require('./commands/status');
const { feedback } = require('./commands/feedback');
const { deploy, isLocalDeployToken } = require('./commands/deploy');
const { run, isLocalRunToken } = require('./commands/run');

const PUSH_HELP = `
yaver push — Push existing React Native projects to the Yaver mobile host

Commands:
  push ios [repo-or-project-dir]          Discover iOS app, build IPA, upload to TestFlight
  push android [repo-or-project-dir]      Discover Android app, build AAB, upload to Play internal testing
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
  yaver push ios                    Discover iOS app in the repo, build IPA, upload to TestFlight
  yaver push android                Discover Android app in the repo, build AAB, upload to Play internal testing
  yaver push init                   Analyze current project and create yaver.json
  yaver push doctor                 Deep compatibility report for current project
  yaver push modules                List native modules compiled into the mobile host
  yaver push devices                Scan LAN for Yaver mobile hosts
  yaver push reset                  Clear pushed bundle on the selected device
  yaver push status                 Show project + device push status

Dev-server commands (monorepo-aware; yaver.deploy.json + find/grep scan):
  yaver run dev                     Start the web dev server (default target)
  yaver run dev:web                 Web (Next/Astro/Vite/SvelteKit/Cloudflare etc.)
  yaver run dev:mobile              Expo / React Native / Flutter dev launcher
  yaver run dev:convex              Convex dev (alias: dev:backend)
  yaver run dev:supabase            Local Supabase stack
  yaver run dev:docker              docker compose up
  yaver run dev:all                 Run every detected dev target in parallel
  yaver run dev:list                Show resolved dev targets + their command
  yaver run dev --dry-run           Resolve + print, do not execute

Deploy commands (monorepo-aware; yaver.deploy.json + find/grep scan):
  yaver deploy ios                  Mobile → TestFlight (RN/Expo, Flutter, Swift)
  yaver deploy android              Mobile → Play internal (RN/Expo, Flutter, Kotlin)
  yaver deploy convex               Deploy Convex backend (alias: backend)
  yaver deploy supabase             Supabase db + edge functions (alias: backend)
  yaver deploy cloudflare           Web/jamstack → Cloudflare (alias: frontend, front)
  yaver deploy docker               Docker compose/image (alias: container, compose)
  yaver deploy npm                  Publish detected npm library (alias: lib, publish)
  yaver deploy mobile               ios + android
  yaver deploy all                  backend → frontend → mobile (npm/docker excluded)
  yaver deploy list                 Show resolved targets, framework + commands
  yaver deploy <t> --dry-run        Print dir + command without running

Feedback SDK commands (web and mobile SDKs are separate packages):
  yaver feedback init               Install the right feedback SDK for this project
  yaver feedback init --platform web          Force web (yaver-feedback-web)
  yaver feedback init --platform react-native Force RN (yaver-feedback-react-native)
  yaver feedback init --platform expo         Expo (yaver-feedback-react-native)
  yaver feedback init --platform flutter      Flutter (yaver_feedback)

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

  // Mark agent commands that originate from the npm bootstrap so the
  // Go binary can keep install/update behavior aligned with the actual
  // distribution path (`yaver-cli` via npm) instead of guessing from
  // argv[0] or filesystem layout.
  process.env.YAVER_INSTALL_SOURCE = 'npm';
  process.env.YAVER_NPM_PACKAGE = PACKAGE.name;
  process.env.YAVER_NPM_VERSION = PACKAGE.version;

  if (command === '--help' || command === '-h' || command === 'help') {
    console.log(UNIFIED_HELP);
    process.exit(0);
  }

  if (!command) {
    // Bare `yaver` (no args): hand off to the Go agent instead of printing
    // JS help. The agent launches the psql-style interactive shell when
    // stdin+stdout are a TTY (shell_repl.go::maybeRunYaverShell), and falls
    // back to its own usage otherwise. Printing UNIFIED_HELP here used to
    // shadow the shell entirely — the binary was never spawned.
    await runAgentCommand([]);
    return;
  }

  if (command === 'push') {
    const next = args[1];
    if (next === 'ios' || next === 'android') {
      await runAgentCommand(args);
      return;
    }
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

  if (command === 'feedback') {
    // `feedback init` is handled locally in JS so web devs who never install
    // the Go binary can still wire up the SDK. Other subcommands (list, show,
    // fix, delete) fall through to the Go agent inside feedback() itself.
    await feedback(args.slice(1));
    return;
  }

  if (command === 'run') {
    // Local-handle `yaver run dev[:target]`. Anything else (future
    // Go-agent `run` subcommands) falls through to the agent.
    if (isLocalRunToken(args[1])) {
      await run(args.slice(1));
      return;
    }
    await runAgentCommand(args);
    return;
  }

  if (command === 'deploy') {
    // Friendly monorepo targets (ios/android/convex/cloudflare/
    // mobile/all/aliases/list/--help) are handled locally. Legacy
    // CI-trigger flags (-repo/-workflow/...) and future agent
    // subcommands (generate/ship/runs/logs/diagnose) fall through to
    // the Go agent untouched.
    if (isLocalDeployToken(args[1], args)) {
      await deploy(args.slice(1));
      return;
    }
    await runAgentCommand(args);
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
