'use strict';

// `yaver deploy <target>` — friendly, monorepo-aware deploy front-end.
//
// This is the human-facing layer. It maps short target names
// (ios / android / convex / supabase / cloudflare / docker / npm /
// backend / frontend / mobile / all) onto concrete build+upload
// commands. `yaver.deploy.json` at the repo root is authoritative;
// on top of (or instead of) it, a find/grep monorepo scan auto-
// detects app dirs at any depth and their framework (Flutter, React
// Native/Expo, native Swift/Kotlin, Next/Astro/Vite/SvelteKit/Nuxt/
// Remix jamstack, Convex, Supabase, Cloudflare/Wrangler, Docker, and
// publishable npm libraries). Config entries always win on name
// conflicts; detection fills the gaps — so the same binary works for
// a bespoke monorepo (Talos) and a zero-config one (yaver.io).
// Anything we do not recognise (legacy `-repo/-workflow` CI-trigger
// flags, or future `generate/ship/runs/logs/diagnose` subcommands) is
// left for the Go agent — index.js decides the split.

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const CONFIG_FILE = 'yaver.deploy.json';

// Subcommands that are NOT ours — index.js forwards these to the Go
// agent untouched so we never regress the existing CI-trigger `deploy`.
const AGENT_SUBCOMMANDS = new Set([
  'generate', 'ship', 'runs', 'logs', 'diagnose',
]);

// Built-in alias → canonical target(s). A value may be an array of
// candidates; expand() picks the first one that actually resolved in
// this repo (so `backend` works whether it's Convex or Supabase).
// Overridable by config.aliases.
const BUILTIN_ALIASES = {
  backend: ['convex', 'supabase'],
  frontend: 'cloudflare',
  front: 'cloudflare',
  web: 'cloudflare',
  library: 'npm',
  lib: 'npm',
  publish: 'npm',
  container: 'docker',
  compose: 'docker',
  television: 'tv',
  leanback: 'android-tv',
  appletv: 'tvos',
};

// Canonical target names we always recognise even before scanning.
const KNOWN_TARGETS = ['ios', 'android', 'android-tv', 'tvos', 'tv', 'convex', 'supabase', 'cloudflare', 'docker', 'npm'];

// Built-in groups. Overridable by config.groups. Expanded lazily so a
// group only pulls in targets that actually resolve.
const BUILTIN_GROUPS = {
  mobile: ['ios', 'android'],
  tv: ['android-tv', 'tvos'],
  all: ['convex', 'cloudflare', 'ios', 'android'],
};

const DEPLOY_HELP = `
yaver deploy — build + ship a monorepo app from your own machine

Targets:
  yaver deploy ios            Mobile → TestFlight (RN/Expo, Flutter, native Swift)
  yaver deploy android        Mobile → Play internal (RN/Expo, Flutter, native Kotlin)
  yaver deploy android-tv     Android TV → Play AAB + leanback verification
  yaver deploy tvos           Apple TV → standalone tvOS build/archive
  yaver deploy convex         Deploy the Convex backend          (alias: backend)
  yaver deploy supabase       Push Supabase db + edge functions  (alias: backend)
  yaver deploy cloudflare     Deploy web/jamstack to Cloudflare  (alias: frontend, front, web)
  yaver deploy docker         Build/up Docker (compose or image) (alias: container, compose)
  yaver deploy npm            Publish a detected npm library     (alias: library, lib, publish)
  yaver deploy mobile         ios + android
  yaver deploy tv             android-tv + tvos
  yaver deploy all            backend → frontend → mobile (npm/docker excluded)

Inspect:
  yaver deploy list           Show every resolved target, its framework + command
  yaver deploy <t> --dry-run  Print the exact dir + command without running it

Options:
  --dry-run, --print          Resolve + print, do not execute
  --continue-on-error         Run remaining targets even if one fails
  --help                      Show this help

Targets come from ${CONFIG_FILE} at the repo root (authoritative).
A find/grep monorepo scan also auto-detects app dirs at any depth and
their framework; config entries win on name conflicts and detection
fills the gaps. Legacy CI-trigger flags (-repo/-workflow/...) and the
generate/ship/runs/logs/diagnose subcommands are handled by the agent.
`;

// A token is "ours" (handle locally) when it is a known target/alias/
// group name, or a config-defined one, or list/help with no agent
// flags. Anything starting with '-' or in AGENT_SUBCOMMANDS is the
// agent's. Used by index.js to route without parsing the world.
function isLocalDeployToken(token, args) {
  if (!token) return false; // bare `yaver deploy` → agent (its usage)
  if (token.startsWith('-')) {
    return token === '--help' || token === '-h';
  }
  if (AGENT_SUBCOMMANDS.has(token)) return false;
  if (token === 'list') return true;
  if (BUILTIN_ALIASES[token] || BUILTIN_GROUPS[token]) return true;
  if (KNOWN_TARGETS.includes(token)) return true;
  // Fall back to config: a name only we'd know about.
  try {
    const { config } = loadConfig(process.cwd());
    if (config) {
      if (config.targets && config.targets[token]) return true;
      if (config.groups && config.groups[token]) return true;
      if (config.aliases && config.aliases[token]) return true;
    }
  } catch {
    /* config errors surface later in run() with a clear message */
  }
  return false;
}

// Walk up from `start` until a yaver.deploy.json is found. If none,
// stop at the git root (or filesystem root) and return that as
// repoRoot with config=null so auto-detection can take over.
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
  try {
    return JSON.parse(fs.readFileSync(p, 'utf8'));
  } catch {
    return null;
  }
}

// Heavy dirs we never descend into during the scan.
const PRUNE_DIRS = [
  'node_modules', '.git', 'build', 'dist', '.next', '.expo', 'Pods',
  '.yaver', 'vendor', 'target', '.gradle', 'DerivedData', '.turbo',
  '.cache', 'out', '.svelte-kit', '.output', 'coverage', '.venv',
  '__pycache__', '.dart_tool',
];
// Marker files (any depth ≤ MAXDEPTH) that hint at an app + its stack.
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
// Marker directories.
const MARKER_DIRS = ['convex', 'supabase', '*.xcodeproj', '*.xcworkspace'];
const MAXDEPTH = 6;

function nameGroup(names) {
  const g = ['('];
  names.forEach((n, i) => {
    if (i) g.push('-o');
    g.push('-name', n);
  });
  g.push(')');
  return g;
}

// One `find`: prune heavy dirs, then print marker files and marker
// dirs up to MAXDEPTH. Falls back to a shallow JS walk if `find` is
// unavailable (e.g. minimal Windows shells).
function scanRepo(repoRoot) {
  const args = [
    repoRoot, '-maxdepth', String(MAXDEPTH),
    ...nameGroup(PRUNE_DIRS), '-prune', '-o',
    '(', '-type', 'f', ...nameGroup(MARKER_FILES), ')', '-print', '-o',
    '(', '-type', 'd', ...nameGroup(MARKER_DIRS), ')', '-print',
  ];
  const res = spawnSync('find', args, { encoding: 'utf8', timeout: 20000, maxBuffer: 16 * 1024 * 1024 });
  if (res.error || res.status == null) {
    return jsWalk(repoRoot, MAXDEPTH);
  }
  return res.stdout.split('\n').filter(Boolean);
}

// Minimal fallback walker (only marker basenames, bounded depth).
function jsWalk(root, depth, base = root, acc = []) {
  if (depth < 0) return acc;
  let entries;
  try {
    entries = fs.readdirSync(base, { withFileTypes: true });
  } catch {
    return acc;
  }
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

// Detect every deployable target by classifying scanned markers.
// Returns { [name]: spec } where spec = {dir, run, env, description,
// framework}. `run: null` means "detected but command unknown — user
// must declare it in yaver.deploy.json".
function autoDetectTargets(repoRoot) {
  const markers = scanRepo(repoRoot);
  const has = (rel2) => fs.existsSync(path.join(repoRoot, rel2));
  const tfScript = has('scripts/deploy-testflight.sh');
  const psScript = has('scripts/deploy-playstore.sh');
  const androidTvScript = has('scripts/deploy-android-tv.sh');
  const tvosScript = has('scripts/deploy-tvos.sh');
  const tvScript = has('scripts/deploy-tv.sh');

  // Group markers by the directory that owns them.
  const dirs = {}; // relDir -> { files:Set, dirs:Set }
  const note = (d, kind, name) => {
    const k = rel(repoRoot, d);
    (dirs[k] || (dirs[k] = { files: new Set(), dirs: new Set() }))[kind].add(name);
  };
  for (const m of markers) {
    let st;
    try { st = fs.statSync(m); } catch { continue; }
    if (st.isDirectory()) {
      note(path.dirname(m), 'dirs', path.basename(m));
    } else {
      note(path.dirname(m), 'files', path.basename(m));
    }
  }

  const targets = {};
  // Keep the strongest candidate per target: prefer a repo deploy
  // script, then a runnable command, then the shallowest dir.
  const consider = (name, spec) => {
    const cur = targets[name];
    if (!cur) { targets[name] = spec; return; }
    const score = (s) => (s.run ? 2 : 0) + (s.scriptBacked ? 1 : 0);
    if (score(spec) > score(cur)
      || (score(spec) === score(cur) && depthOf(spec.dir) < depthOf(cur.dir))) {
      targets[name] = spec;
    }
  };

  const isFile = (relDir, base) => {
    const e = dirs[relDir];
    if (!e) return false;
    for (const f of e.files) {
      if (f === base) return true;
      if (base.startsWith('*.') && f.endsWith(base.slice(1))) return true;
    }
    return false;
  };
  const hasDir = (relDir, base) => {
    const e = dirs[relDir];
    if (!e) return false;
    for (const dd of e.dirs) {
      if (dd === base) return true;
      if (base.startsWith('*') && dd.endsWith(base.slice(1))) return true;
    }
    return false;
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

    // ── Mobile ───────────────────────────────────────────────
    const isExpo = !!deps.expo;
    const isRN = !!deps['react-native'];
    if (flutter) {
      consider('ios', {
        dir: tfScript ? '.' : relDir,
        run: tfScript ? 'bash scripts/deploy-testflight.sh' : 'flutter build ipa',
        scriptBacked: tfScript,
        framework: 'flutter',
        description: tfScript ? 'Flutter iOS via scripts/deploy-testflight.sh'
          : 'Flutter → IPA (build only; declare TestFlight upload in ' + CONFIG_FILE + ')',
      });
      consider('android', {
        dir: psScript ? '.' : relDir,
        run: psScript ? 'bash scripts/deploy-playstore.sh' : 'flutter build appbundle',
        scriptBacked: psScript,
        framework: 'flutter',
        description: psScript ? 'Flutter Android via scripts/deploy-playstore.sh'
          : 'Flutter → AAB (build only; declare Play upload in ' + CONFIG_FILE + ')',
      });
    } else if (isExpo || isRN) {
      const fw = isExpo ? 'expo' : 'react-native';
      consider('ios', {
        dir: '.',
        run: tfScript ? 'bash scripts/deploy-testflight.sh' : null,
        scriptBacked: tfScript,
        framework: fw,
        description: tfScript ? `${fw} iOS via scripts/deploy-testflight.sh`
          : `${fw} app — declare iOS archive+upload in ${CONFIG_FILE}`,
      });
      consider('android', {
        dir: '.',
        run: psScript ? 'bash scripts/deploy-playstore.sh' : null,
        scriptBacked: psScript,
        framework: fw,
        description: psScript ? `${fw} Android via scripts/deploy-playstore.sh`
          : `${fw} app — declare Android bundle+upload in ${CONFIG_FILE}`,
      });
    } else if (hasDir(relDir, '*.xcodeproj') || hasDir(relDir, '*.xcworkspace') || sig.files.has('Package.swift')) {
      consider('ios', {
        dir: tfScript ? '.' : relDir,
        run: tfScript ? 'bash scripts/deploy-testflight.sh' : null,
        scriptBacked: tfScript,
        framework: 'swift',
        description: tfScript ? 'Native iOS via scripts/deploy-testflight.sh'
          : 'Native Xcode project — declare archive+upload in ' + CONFIG_FILE,
      });
    }
    if (!flutter && !isExpo && !isRN
      && (sig.files.has('build.gradle') || sig.files.has('build.gradle.kts'))
      && isFileInTree(dirs, 'AndroidManifest.xml')) {
      consider('android', {
        dir: psScript ? '.' : relDir,
        run: psScript ? 'bash scripts/deploy-playstore.sh' : null,
        scriptBacked: psScript,
        framework: 'kotlin',
        description: psScript ? 'Native Android via scripts/deploy-playstore.sh'
          : 'Native Gradle project — declare bundle+upload in ' + CONFIG_FILE,
      });
    }

    // ── Backend: Convex / Supabase ───────────────────────────
    // Folder-based only: `convex` as a package dep is just the client
    // SDK (a frontend), not the backend. The backend is where the
    // functions/config live (convex.json or a convex/ dir).
    if (sig.files.has('convex.json') || hasDir(relDir, 'convex')) {
      consider('convex', {
        dir: relDir, run: 'npx convex deploy -y', scriptBacked: false,
        framework: 'convex', description: `Convex backend (${relDir})`,
      });
    }
    if (hasDir(relDir, 'supabase') || (relDir.endsWith('supabase') && sig.files.has('config.toml'))) {
      const sbDir = relDir.endsWith('supabase') ? path.dirname(relDir) || '.' : relDir;
      consider('supabase', {
        dir: sbDir,
        run: 'npx supabase db push && npx supabase functions deploy',
        scriptBacked: false, framework: 'supabase',
        description: `Supabase db + edge functions (${sbDir}) — verify before live use`,
      });
    }

    // ── Web / jamstack → Cloudflare ──────────────────────────
    const jamstack = deps.next || deps.astro || deps.vite || deps['@sveltejs/kit']
      || deps.nuxt || deps['@remix-run/dev'] || deps.gatsby;
    const cfConfig = sig.files.has('wrangler.toml') || sig.files.has('wrangler.jsonc')
      || sig.files.has('wrangler.json') || deps.wrangler || deps['@opennextjs/cloudflare'];
    if (jamstack || cfConfig) {
      let run = null;
      let desc;
      const fw = deps.next ? 'next' : deps.astro ? 'astro' : deps.nuxt ? 'nuxt'
        : deps['@sveltejs/kit'] ? 'sveltekit' : deps['@remix-run/dev'] ? 'remix'
        : deps.gatsby ? 'gatsby' : deps.vite ? 'vite' : 'jamstack';
      if (scripts.deploy) { run = 'npm run deploy'; desc = `${fw} → Cloudflare (npm run deploy)`; }
      else if (deps['@opennextjs/cloudflare']) { run = 'npx @opennextjs/cloudflare build && npx wrangler deploy'; desc = `${fw} → Cloudflare (OpenNext)`; }
      else if (cfConfig) { run = 'npx wrangler deploy'; desc = `${fw} → Cloudflare (wrangler)`; }
      else { desc = `${fw} jamstack — no Cloudflare config; declare deploy in ${CONFIG_FILE}`; }
      consider('cloudflare', { dir: relDir, run, scriptBacked: false, framework: fw, description: desc });
    }

    // ── npm library ──────────────────────────────────────────
    if (pkg && pkg.private !== true && pkg.name && pkg.version
      && (pkg.main || pkg.module || pkg.exports || pkg.bin)
      && !jamstack && !isExpo && !isRN) {
      const pubCmd = scripts.release ? 'npm run release'
        : scripts.publish ? 'npm run publish' : 'npm publish';
      consider('npm', {
        dir: relDir, run: pubCmd, scriptBacked: !!(scripts.release || scripts.publish),
        framework: 'npm', description: `npm publish ${pkg.name}@${pkg.version} (${relDir})`,
      });
    }

    // ── Docker ───────────────────────────────────────────────
    const compose = ['docker-compose.yml', 'docker-compose.yaml', 'compose.yaml', 'compose.yml']
      .some((f) => sig.files.has(f));
    if (compose) {
      consider('docker', {
        dir: relDir, run: 'docker compose up -d --build', scriptBacked: false,
        framework: 'docker-compose', description: `Docker Compose (${relDir})`,
      });
    } else if (sig.files.has('Dockerfile')) {
      const tag = (path.basename(abs) || 'app').toLowerCase().replace(/[^a-z0-9._-]/g, '-');
      consider('docker', {
        dir: relDir, run: `docker build -t ${tag} .`, scriptBacked: false,
        framework: 'dockerfile', description: `Docker image build ${tag} (${relDir})`,
      });
    }
  }

  if (androidTvScript) {
    consider('android-tv', {
      dir: '.',
      run: 'bash scripts/deploy-android-tv.sh --upload',
      scriptBacked: true,
      framework: 'android-tv',
      description: 'Android TV Play AAB upload with leanback release-manifest verification',
    });
  }
  if (tvosScript) {
    consider('tvos', {
      dir: '.',
      run: 'bash scripts/deploy-tvos.sh --upload',
      scriptBacked: true,
      framework: 'tvos',
      description: 'Apple TV standalone tvOS archive/upload',
    });
  }
  if (tvScript) {
    consider('tv', {
      dir: '.',
      run: 'bash scripts/deploy-tv.sh --upload',
      scriptBacked: true,
      framework: 'tv',
      description: 'Android TV + Apple TV platform deploy wrapper',
    });
  }

  return targets;
}

// True if any scanned dir holds `base` (used for AndroidManifest.xml,
// which lives several levels under the gradle module root).
function isFileInTree(dirs, base) {
  for (const e of Object.values(dirs)) if (e.files.has(base)) return true;
  return false;
}

// Normalise detection + config into one table. Detection is the base;
// config.targets overlay it (config always wins on name conflicts) so
// the same binary serves a bespoke monorepo and a zero-config one.
function resolveTable(repoRoot, config) {
  const targets = autoDetectTargets(repoRoot);

  if (config && config.targets) {
    for (const [name, spec] of Object.entries(config.targets)) {
      targets[name] = {
        dir: spec.dir || '.',
        run: spec.run || (spec.script ? `bash ${spec.script}` : null),
        env: spec.env || {},
        description: spec.description || name,
        framework: spec.framework || 'config',
        aliases: spec.aliases || [],
      };
    }
  }

  const aliases = {};
  for (const [a, v] of Object.entries(BUILTIN_ALIASES)) aliases[a] = v;
  if (config && config.aliases) for (const [a, v] of Object.entries(config.aliases)) aliases[a] = v;
  for (const [name, spec] of Object.entries(targets)) {
    for (const a of spec.aliases || []) aliases[a] = name;
  }

  const groups = { ...BUILTIN_GROUPS, ...(config && config.groups) };

  return { targets, groups, aliases };
}

// requested name → ordered, deduped list of concrete target names that
// actually exist in the table.
function expand(name, table, seen = new Set(), trail = []) {
  if (trail.includes(name)) {
    throw new Error(`circular deploy group: ${[...trail, name].join(' → ')}`);
  }
  // An alias may map to several candidates; pick the first that
  // actually resolved in this repo (backend → convex|supabase).
  let canonical = name;
  const aliased = table.aliases[name];
  if (Array.isArray(aliased)) {
    canonical = aliased.find((c) => table.targets[c] || table.groups[c]) || aliased[0];
  } else if (aliased) {
    canonical = aliased;
  }

  if (table.targets[canonical]) {
    if (!seen.has(canonical)) {
      seen.add(canonical);
      return [canonical];
    }
    return [];
  }

  const group = table.groups[canonical] || table.groups[name];
  if (group) {
    const out = [];
    for (const member of group) {
      out.push(...expand(member, table, seen, [...trail, name]));
    }
    return out;
  }

  return []; // unknown / not present in this repo
}

function printTarget(name, spec, repoRoot) {
  const where = path.join(repoRoot, spec.dir || '.');
  console.log(`  ${name}${spec.framework ? `  [${spec.framework}]` : ''}`);
  console.log(`    dir: ${where}`);
  if (spec.description) console.log(`    what: ${spec.description}`);
  if (spec.run) {
    console.log(`    run: ${spec.run}`);
  } else {
    console.log(`    run: ⚠️  not configured — ${spec.description}`);
  }
  if (spec.env && Object.keys(spec.env).length) {
    console.log(`    env: ${Object.keys(spec.env).join(', ')}`);
  }
}

function runOne(name, spec, repoRoot) {
  const cwd = path.join(repoRoot, spec.dir || '.');
  if (!spec.run) {
    throw new Error(`target "${name}" has no command (${spec.description}). Add it to ${CONFIG_FILE}.`);
  }
  if (!fs.existsSync(cwd)) {
    throw new Error(`target "${name}" dir does not exist: ${cwd}`);
  }
  console.log(`\n── deploy ${name} ──`);
  console.log(`   $ (cd ${cwd} && ${spec.run})\n`);
  const res = spawnSync('bash', ['-lc', spec.run], {
    cwd,
    stdio: 'inherit',
    env: { ...process.env, ...(spec.env || {}) },
  });
  if (res.error) throw new Error(`failed to launch "${name}": ${res.error.message}`);
  return res.status == null ? 1 : res.status;
}

async function deploy(args) {
  if (!args.length || args[0] === '--help' || args[0] === '-h') {
    console.log(DEPLOY_HELP);
    process.exit(0);
  }

  const dryRun = args.includes('--dry-run') || args.includes('--print');
  const continueOnError = args.includes('--continue-on-error');
  const positionals = args.filter((a) => !a.startsWith('-'));
  const requested = positionals[0];

  const { repoRoot, config, configPath } = loadConfig(process.cwd());
  const table = resolveTable(repoRoot, config);

  const source = configPath
    ? path.relative(process.cwd(), configPath) || CONFIG_FILE
    : 'auto-detection (no ' + CONFIG_FILE + ')';

  if (requested === 'list' || (!requested && dryRun)) {
    console.log(`\nDeploy targets — source: ${source}`);
    console.log(`Repo root: ${repoRoot}\n`);
    const names = Object.keys(table.targets);
    if (!names.length) {
      console.log('  (none resolved — add a ' + CONFIG_FILE + ' or check the layout)');
    }
    for (const n of names) printTarget(n, table.targets[n], repoRoot);
    console.log('\nGroups:');
    for (const [g, members] of Object.entries(table.groups)) {
      console.log(`  ${g}: ${members.join(', ')}`);
    }
    console.log('\nAliases:');
    for (const [a, t] of Object.entries(table.aliases)) {
      console.log(`  ${a} → ${t}`);
    }
    console.log('');
    process.exit(0);
  }

  const plan = expand(requested, table);
  if (!plan.length) {
    console.error(`\n❌ "${requested}" resolved to no deployable targets.`);
    console.error(`   Source: ${source}`);
    console.error(`   Known targets: ${Object.keys(table.targets).join(', ') || '(none)'}`);
    console.error(`   Run: yaver deploy list`);
    process.exit(1);
  }

  console.log(`\nyaver deploy ${requested} → ${plan.join(', ')}`);
  console.log(`Source: ${source}  ·  Repo root: ${repoRoot}`);

  if (dryRun) {
    console.log('\n(dry-run — nothing will be executed)\n');
    for (const n of plan) printTarget(n, table.targets[n], repoRoot);
    console.log('');
    process.exit(0);
  }

  const results = [];
  for (const n of plan) {
    let status;
    try {
      status = runOne(n, table.targets[n], repoRoot);
    } catch (err) {
      console.error(`\n❌ ${err.message}`);
      status = 1;
    }
    results.push({ target: n, status });
    if (status !== 0 && !continueOnError) {
      console.error(`\n❌ ${n} failed (exit ${status}); stopping. Use --continue-on-error to override.`);
      break;
    }
  }

  console.log('\n── composite summary ──');
  let worst = 0;
  for (const r of results) {
    console.log(`  ${r.status === 0 ? '✓' : '✗'} ${r.target} (exit ${r.status})`);
    if (r.status !== 0) worst = r.status;
  }
  const skipped = plan.length - results.length;
  if (skipped > 0) console.log(`  · ${skipped} target(s) skipped after failure`);
  console.log('');
  process.exit(worst);
}

module.exports = { deploy, isLocalDeployToken, DEPLOY_HELP };
