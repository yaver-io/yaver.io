'use strict';

// `yaver run dev[:target]` — start dev servers across a monorepo.
//
// Mirrors `yaver deploy`'s detection: a find/grep scan with framework
// classification, plus optional `yaver.deploy.json` overrides (a
// per-target `dev` field, or a top-level `dev` block). Where deploy
// maps each dir to a build+upload command, run-dev maps it to a hot-
// reload command (npm run dev, npx convex dev, npx expo start,
// npx wrangler dev, flutter run, docker compose up).
//
// Default with no target is `web` (single dev server, stdio inherited
// so reload/interactive keys work). `dev:all` starts every detected
// target concurrently with prefixed/coloured logs; SIGINT propagates
// to all children.

const fs = require('fs');
const path = require('path');
const { spawn, spawnSync } = require('child_process');

const CONFIG_FILE = 'yaver.deploy.json';

// ── Detection constants (same shape as deploy.js — kept local to keep
// deploy's blast radius zero) ───────────────────────────────────────
const PRUNE_DIRS = [
  'node_modules', '.git', 'build', 'dist', '.next', '.expo', 'Pods',
  '.yaver', 'vendor', 'target', '.gradle', 'DerivedData', '.turbo',
  '.cache', 'out', '.svelte-kit', '.output', 'coverage', '.venv',
  '__pycache__', '.dart_tool',
];
const MARKER_FILES = [
  'package.json', 'pubspec.yaml', 'convex.json',
  'wrangler.toml', 'wrangler.jsonc', 'wrangler.json',
  'config.toml', 'Dockerfile', 'docker-compose.yml',
  'docker-compose.yaml', 'compose.yaml', 'compose.yml',
  'Package.swift', 'build.gradle', 'build.gradle.kts',
  'AndroidManifest.xml', 'netlify.toml', 'vercel.json',
  'next.config.*', 'astro.config.*', 'svelte.config.*',
  'nuxt.config.*', 'remix.config.*', 'gatsby-config.*', 'vite.config.*',
];
const MARKER_DIRS = ['convex', 'supabase', '*.xcodeproj', '*.xcworkspace'];
const MAXDEPTH = 6;

// Aliases for dev targets. backend resolves to convex|supabase (first
// that detected); frontend/web/front resolve to whichever web target
// fingerprints in this repo.
const BUILTIN_ALIASES = {
  backend: ['convex', 'supabase'],
  frontend: 'web',
  front: 'web',
};

const RUN_HELP = `
yaver run — start dev servers across a monorepo

Usage:
  yaver run dev                    Start the web dev server (default target)
  yaver run dev:web                Web (Next/Astro/Vite/SvelteKit/Cloudflare etc.)
  yaver run dev:mobile             Expo / React Native / Flutter dev launcher
  yaver run dev:convex             npx convex dev (alias: dev:backend)
  yaver run dev:supabase           Local Supabase stack
  yaver run dev:docker             docker compose up
  yaver run dev:all                Run every detected dev target in parallel
  yaver run dev:list               Show resolved dev targets + their command
  yaver run dev --dry-run          Resolve + print, do not execute

Overrides in ${CONFIG_FILE} at the repo root, either co-located:
  { "targets": { "web": { "dir": "web", "dev": "npm run dev" } } }
or a dedicated dev block:
  { "dev": { "web": { "dir": "web", "run": "npm run dev", "env": {...} } } }

Options:
  --dry-run, --print               Print the plan, don't execute
  --bail                           Stop everything if any child exits non-zero
  --help                           Show this help
`;

// index.js routes `yaver run <token>` here when isLocalRunToken returns
// true. Anything else (e.g. future Go-agent `run` subcommands) falls
// through to the agent.
function isLocalRunToken(token) {
  if (!token) return false;
  if (token === '--help' || token === '-h') return true;
  if (token === 'dev') return true;
  if (token.startsWith('dev:')) return true;
  return false;
}

// ── Detection helpers ────────────────────────────────────────────────
function nameGroup(names) {
  const g = ['('];
  names.forEach((n, i) => {
    if (i) g.push('-o');
    g.push('-name', n);
  });
  g.push(')');
  return g;
}
function loadConfig(start) {
  let dir = path.resolve(start);
  let gitRoot = null;
  // eslint-disable-next-line no-constant-condition
  while (true) {
    const cfgPath = path.join(dir, CONFIG_FILE);
    if (fs.existsSync(cfgPath)) {
      let parsed;
      try {
        parsed = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
      } catch (err) {
        throw new Error(`${CONFIG_FILE} at ${cfgPath} is not valid JSON: ${err.message}`);
      }
      return { repoRoot: dir, config: parsed, configPath: cfgPath };
    }
    if (!gitRoot && fs.existsSync(path.join(dir, '.git'))) gitRoot = dir;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return { repoRoot: gitRoot || path.resolve(start), config: null, configPath: null };
}
function readJson(p) {
  try { return JSON.parse(fs.readFileSync(p, 'utf8')); } catch { return null; }
}
function scanRepo(repoRoot) {
  const args = [
    repoRoot, '-maxdepth', String(MAXDEPTH),
    ...nameGroup(PRUNE_DIRS), '-prune', '-o',
    '(', '-type', 'f', ...nameGroup(MARKER_FILES), ')', '-print', '-o',
    '(', '-type', 'd', ...nameGroup(MARKER_DIRS), ')', '-print',
  ];
  const res = spawnSync('find', args, { encoding: 'utf8', timeout: 20000, maxBuffer: 16 * 1024 * 1024 });
  if (res.error || res.status == null) return jsWalk(repoRoot, MAXDEPTH);
  return res.stdout.split('\n').filter(Boolean);
}
function jsWalk(root, depth, base = root, acc = []) {
  if (depth < 0) return acc;
  let entries;
  try { entries = fs.readdirSync(base, { withFileTypes: true }); } catch { return acc; }
  for (const e of entries) {
    if (e.isDirectory() && PRUNE_DIRS.includes(e.name)) continue;
    const full = path.join(base, e.name);
    const isMarkerDir = MARKER_DIRS.some((m) => m.startsWith('*')
      ? e.name.endsWith(m.slice(1)) : e.name === m);
    const isMarkerFile = MARKER_FILES.some((m) => m.startsWith('*.')
      ? e.name.endsWith(m.slice(1)) : e.name === m);
    if ((e.isDirectory() && isMarkerDir) || (e.isFile() && isMarkerFile)) acc.push(full);
    if (e.isDirectory()) jsWalk(root, depth - 1, full, acc);
  }
  return acc;
}
function rel(repoRoot, p) {
  const r = path.relative(repoRoot, p);
  return r === '' ? '.' : r;
}
function depthOf(relDir) {
  return relDir === '.' ? 0 : relDir.split(path.sep).length;
}

// Group markers by their owning directory.
function dirSignatures(repoRoot) {
  const markers = scanRepo(repoRoot);
  const dirs = {};
  const note = (d, kind, name) => {
    const k = rel(repoRoot, d);
    (dirs[k] || (dirs[k] = { files: new Set(), dirs: new Set() }))[kind].add(name);
  };
  for (const m of markers) {
    let st;
    try { st = fs.statSync(m); } catch { continue; }
    if (st.isDirectory()) note(path.dirname(m), 'dirs', path.basename(m));
    else note(path.dirname(m), 'files', path.basename(m));
  }
  return dirs;
}

// Map each detected dir to its dev command. Prefer a shallower dir
// when two candidates resolve to the same canonical target.
function autoDetectDev(repoRoot) {
  const dirs = dirSignatures(repoRoot);
  const targets = {};

  const consider = (name, spec) => {
    const cur = targets[name];
    if (!cur) { targets[name] = spec; return; }
    // Strength order: has a real command > has an authoritative marker
    // (e.g. convex.json wins over just a convex/ subdir of generated
    // types) > shallower dir.
    const score = (s) => (s.run ? 2 : 0) + (s.strong ? 1 : 0);
    if (score(spec) > score(cur)
      || (score(spec) === score(cur) && depthOf(spec.dir) < depthOf(cur.dir))) {
      targets[name] = spec;
    }
  };

  for (const [relDir, sig] of Object.entries(dirs)) {
    const abs = path.join(repoRoot, relDir);
    const pkg = sig.files.has('package.json') ? readJson(path.join(abs, 'package.json')) : null;
    const deps = pkg ? { ...(pkg.dependencies || {}), ...(pkg.devDependencies || {}) } : {};
    const scripts = (pkg && pkg.scripts) || {};
    const pubspec = sig.files.has('pubspec.yaml');
    let flutter = false;
    if (pubspec) {
      try { flutter = /\bflutter\s*:/.test(fs.readFileSync(path.join(abs, 'pubspec.yaml'), 'utf8')); }
      catch { flutter = true; }
    }

    const isExpo = !!deps.expo;
    const isRN = !!deps['react-native'];

    // Mobile dev
    if (flutter) {
      consider('mobile', {
        dir: relDir, run: 'flutter run', framework: 'flutter',
        description: `Flutter dev (${relDir})`,
      });
    } else if (isExpo) {
      const cmd = scripts.start ? 'npm run start' : 'npx expo start';
      consider('mobile', {
        dir: relDir, run: cmd, framework: 'expo',
        description: `Expo dev server (${relDir})`,
      });
    } else if (isRN) {
      const cmd = scripts.start ? 'npm run start' : 'npx react-native start';
      consider('mobile', {
        dir: relDir, run: cmd, framework: 'react-native',
        description: `React Native Metro (${relDir})`,
      });
    }

    // Convex. `convex.json` is the authoritative backend marker; a
    // bare `convex/` subdir alone is often just generated client types
    // (e.g. web/convex/) — treated as a weak fallback.
    if (sig.files.has('convex.json') || sig.dirs.has('convex')) {
      const cmd = scripts.dev ? 'npm run dev' : 'npx convex dev';
      consider('convex', {
        dir: relDir, run: cmd, framework: 'convex',
        strong: sig.files.has('convex.json'),
        description: `Convex dev (${relDir})`,
      });
    }

    // Supabase
    if (sig.dirs.has('supabase') || (relDir.endsWith('supabase') && sig.files.has('config.toml'))) {
      const sbDir = relDir.endsWith('supabase') ? path.dirname(relDir) || '.' : relDir;
      consider('supabase', {
        dir: sbDir, run: 'npx supabase start', framework: 'supabase',
        description: `Supabase local stack (${sbDir})`,
      });
    }

    // Web / jamstack / Cloudflare worker
    const jamstack = deps.next || deps.astro || deps.vite || deps['@sveltejs/kit']
      || deps.nuxt || deps['@remix-run/dev'] || deps.gatsby;
    const cfConfig = sig.files.has('wrangler.toml') || sig.files.has('wrangler.jsonc')
      || sig.files.has('wrangler.json') || deps.wrangler || deps['@opennextjs/cloudflare'];
    if (jamstack || cfConfig) {
      const fw = deps.next ? 'next' : deps.astro ? 'astro' : deps.nuxt ? 'nuxt'
        : deps['@sveltejs/kit'] ? 'sveltekit' : deps['@remix-run/dev'] ? 'remix'
        : deps.gatsby ? 'gatsby' : deps.vite ? 'vite' : 'jamstack';
      let cmd = null;
      if (scripts.dev) cmd = 'npm run dev';
      else if (cfConfig) cmd = 'npx wrangler dev';
      else if (scripts.start) cmd = 'npm run start';
      consider('web', {
        dir: relDir, run: cmd, framework: fw,
        description: cmd ? `${fw} dev server (${relDir})`
          : `${fw} app — declare dev command in ${CONFIG_FILE}`,
      });
    }

    // Docker compose (logs stream — no -d)
    const compose = ['docker-compose.yml', 'docker-compose.yaml', 'compose.yaml', 'compose.yml']
      .some((f) => sig.files.has(f));
    if (compose) {
      consider('docker', {
        dir: relDir, run: 'docker compose up', framework: 'docker-compose',
        description: `Docker Compose dev (${relDir})`,
      });
    }
  }

  return targets;
}

function resolveTable(repoRoot, config) {
  const targets = autoDetectDev(repoRoot);

  // Per-target `dev` override on the existing deploy spec.
  if (config && config.targets) {
    for (const [name, spec] of Object.entries(config.targets)) {
      if (spec.dev == null) continue;
      const devSpec = typeof spec.dev === 'string' ? { run: spec.dev } : spec.dev;
      targets[name] = {
        dir: devSpec.dir || spec.dir || (targets[name] && targets[name].dir) || '.',
        run: devSpec.run || (devSpec.script ? `bash ${devSpec.script}` : null),
        env: devSpec.env || spec.env || {},
        description: devSpec.description || `${name} dev (config)`,
        framework: spec.framework || 'config',
      };
    }
  }
  // Standalone `dev` block.
  if (config && config.dev && typeof config.dev === 'object') {
    for (const [name, spec] of Object.entries(config.dev)) {
      targets[name] = {
        dir: spec.dir || '.',
        run: spec.run || (spec.script ? `bash ${spec.script}` : null),
        env: spec.env || {},
        description: spec.description || `${name} dev (config)`,
        framework: spec.framework || 'config',
      };
    }
  }

  // Aliases: built-ins, plus top-level config.aliases (these are
  // alias→target maps and are dev-safe), plus any dev-specific aliases.
  const aliases = {
    ...BUILTIN_ALIASES,
    ...(config && config.aliases),
    ...(config && config.dev && config.dev.aliases),
  };
  // Groups: only dev-specific groups; the deploy-side `config.groups`
  // (e.g. `mobile: [ios, android]`) describes builds, not dev servers,
  // so we deliberately don't inherit them here.
  const groups = {
    all: Object.keys(targets).filter((n) => targets[n].run),
    ...(config && config.dev && config.dev.groups),
  };

  return { targets, groups, aliases };
}

// Requested name → ordered, deduped list of concrete target names.
function expand(name, table, seen = new Set(), trail = []) {
  if (trail.includes(name)) {
    throw new Error(`circular run group: ${[...trail, name].join(' → ')}`);
  }
  let canonical = name;
  const aliased = table.aliases[name];
  if (Array.isArray(aliased)) {
    canonical = aliased.find((c) => table.targets[c] || table.groups[c]) || aliased[0];
  } else if (aliased) {
    canonical = aliased;
  }
  if (table.targets[canonical]) {
    if (!seen.has(canonical)) { seen.add(canonical); return [canonical]; }
    return [];
  }
  const group = table.groups[canonical] || table.groups[name];
  if (group) {
    const out = [];
    for (const member of group) out.push(...expand(member, table, seen, [...trail, name]));
    return out;
  }
  return [];
}

function printTarget(name, spec, repoRoot) {
  const where = path.join(repoRoot, spec.dir || '.');
  console.log(`  ${name}${spec.framework ? `  [${spec.framework}]` : ''}`);
  console.log(`    dir: ${where}`);
  if (spec.description) console.log(`    what: ${spec.description}`);
  if (spec.run) console.log(`    run: ${spec.run}`);
  else console.log(`    run: ⚠️  not configured`);
}

// ── Concurrent runner ────────────────────────────────────────────────
// ANSI 16-colour palette for log prefixes; plain when stdout isn't a
// TTY (keeps CI/grep output clean).
const PREFIX_COLORS = [36, 32, 33, 35, 34, 31, 96, 92, 93, 95];
function paint(i, text) {
  if (!process.stdout.isTTY) return text;
  return `\x1b[${PREFIX_COLORS[i % PREFIX_COLORS.length]}m${text}\x1b[0m`;
}

function streamWithPrefix(stream, prefix, sink) {
  let buf = '';
  stream.setEncoding('utf8');
  stream.on('data', (chunk) => {
    buf += chunk;
    const lines = buf.split('\n');
    buf = lines.pop();
    for (const line of lines) sink.write(`${prefix} ${line}\n`);
  });
  stream.on('end', () => { if (buf.length) sink.write(`${prefix} ${buf}\n`); });
}

function spawnTarget(name, spec, repoRoot, colorIdx, soloMode) {
  const cwd = path.join(repoRoot, spec.dir || '.');
  if (!fs.existsSync(cwd)) throw new Error(`target "${name}" dir does not exist: ${cwd}`);
  if (!spec.run) throw new Error(`target "${name}" has no dev command (${spec.description})`);
  const prefix = paint(colorIdx, `[${name}]`);
  console.log(`${prefix} $ (cd ${cwd} && ${spec.run})`);
  // Solo mode inherits stdio so interactive dev servers (expo, RN
  // Metro, vite) keep their keyboard shortcuts. Multi-target mode
  // pipes so we can prefix each line.
  const child = spawn('bash', ['-lc', spec.run], {
    cwd,
    env: { ...process.env, ...(spec.env || {}) },
    stdio: soloMode ? 'inherit' : ['ignore', 'pipe', 'pipe'],
  });
  if (!soloMode) {
    streamWithPrefix(child.stdout, prefix, process.stdout);
    streamWithPrefix(child.stderr, prefix, process.stderr);
  }
  return { name, child, prefix };
}

async function runDevTargets(plan, table, repoRoot, opts) {
  const soloMode = plan.length === 1;
  const procs = [];
  let killing = false;

  const killAll = (signal) => {
    if (killing) return;
    killing = true;
    for (const p of procs) {
      if (!p.child.killed && p.child.exitCode == null) {
        try { p.child.kill(signal); } catch { /* ignore */ }
      }
    }
  };

  process.on('SIGINT', () => {
    if (!soloMode) console.log('\n^C — stopping dev servers');
    killAll('SIGINT');
  });
  process.on('SIGTERM', () => killAll('SIGTERM'));

  for (let i = 0; i < plan.length; i++) {
    try {
      procs.push(spawnTarget(plan[i], table.targets[plan[i]], repoRoot, i, soloMode));
    } catch (err) {
      console.error(`❌ ${plan[i]}: ${err.message}`);
      if (opts.bail) { killAll('SIGTERM'); process.exit(1); }
    }
  }
  if (!procs.length) {
    console.error('❌ no targets started');
    process.exit(1);
  }

  const results = await Promise.all(procs.map((p) => new Promise((resolve) => {
    p.child.on('exit', (code, signal) => {
      if (!soloMode) console.log(`${p.prefix} exited (code=${code}, signal=${signal || 'none'})`);
      if (opts.bail && code !== 0 && !killing) killAll('SIGTERM');
      resolve({ name: p.name, code, signal });
    });
  })));

  let worst = 0;
  for (const r of results) if (r.code && r.code !== 0) worst = r.code;
  process.exit(worst);
}

// ── Entry point ──────────────────────────────────────────────────────
async function run(args) {
  const first = args[0];
  if (!first || first === '--help' || first === '-h') {
    console.log(RUN_HELP);
    process.exit(0);
  }

  // Parse "dev" or "dev:<target>".
  let requested = null;
  if (first === 'dev') {
    requested = null; // resolve from positional or default below
  } else if (first.startsWith('dev:')) {
    requested = first.slice(4);
  } else {
    console.error(`Unknown run subcommand: ${first}`);
    console.log(RUN_HELP);
    process.exit(1);
  }

  const rest = args.slice(1);
  const dryRun = rest.includes('--dry-run') || rest.includes('--print');
  const bail = rest.includes('--bail');
  const positionals = rest.filter((a) => !a.startsWith('-'));
  if (!requested) requested = positionals[0] || 'web';

  const { repoRoot, config, configPath } = loadConfig(process.cwd());
  const table = resolveTable(repoRoot, config);
  const source = configPath
    ? path.relative(process.cwd(), configPath) || CONFIG_FILE
    : `auto-detection (no ${CONFIG_FILE})`;

  if (requested === 'list') {
    console.log(`\nDev targets — source: ${source}`);
    console.log(`Repo root: ${repoRoot}\n`);
    const names = Object.keys(table.targets);
    if (!names.length) {
      console.log('  (none detected — add a ' + CONFIG_FILE + ' with a dev block)');
    }
    for (const n of names) printTarget(n, table.targets[n], repoRoot);
    console.log('\nGroups:');
    for (const [g, members] of Object.entries(table.groups)) {
      console.log(`  ${g}: ${members.join(', ')}`);
    }
    console.log('\nAliases:');
    for (const [a, t] of Object.entries(table.aliases)) {
      console.log(`  ${a} → ${Array.isArray(t) ? t.join('|') : t}`);
    }
    console.log('');
    process.exit(0);
  }

  const plan = expand(requested, table).filter((n) => table.targets[n] && table.targets[n].run);
  if (!plan.length) {
    console.error(`\n❌ "${requested}" resolved to no runnable dev targets.`);
    console.error(`   Source: ${source}`);
    const detected = Object.keys(table.targets);
    console.error(`   Detected targets: ${detected.join(', ') || '(none)'}`);
    console.error(`   Try: yaver run dev:list`);
    process.exit(1);
  }

  console.log(`\nyaver run dev${first.startsWith('dev:') ? first.slice(3) : ''} → ${plan.join(', ')}`);
  console.log(`Source: ${source}  ·  Repo root: ${repoRoot}`);

  if (dryRun) {
    console.log('\n(dry-run — nothing will be executed)\n');
    for (const n of plan) printTarget(n, table.targets[n], repoRoot);
    console.log('');
    process.exit(0);
  }

  await runDevTargets(plan, table, repoRoot, { bail });
}

module.exports = { run, isLocalRunToken, RUN_HELP };
