#!/usr/bin/env bash
set -euo pipefail

# mini-deploy.sh — pull main, then deploy, SEQUENTIALLY, from a dedicated clone.
#
# This is the standing deploy path: commit -> push -> the box pulls -> the box
# deploys. Not "deploy from whatever tree the developer happens to be sitting
# in". Two reasons, both learned the hard way on 2026-07-19:
#
#   1. A shared checkout is never yours alone. That day a sibling session's
#      UNCOMMITTED work (`placementKind` on PendingCloudTaskParams) broke
#      `npm run build` on the MacBook while main itself was perfectly healthy —
#      the same commit built clean here on the first try. Deploying what you
#      pushed, from a clone nobody else edits, removes that whole class.
#   2. Deploys are metered. Building on the box that will ship keeps one
#      converged change to one deploy per target.
#
# SEQUENTIAL IS A HARD REQUIREMENT, not a preference. Parallel mobile builds
# exhaust this machine's RAM and SSD (Xcode archive + Gradle bundleRelease will
# happily take everything). Every step below runs one at a time, and nothing in
# here may be backgrounded.
#
# Usage:
#   scripts/mini-deploy.sh                 # preflight + every ready target
#   scripts/mini-deploy.sh web convex      # only these
#   scripts/mini-deploy.sh --check         # preflight only, deploy nothing

CLONE_DEFAULT="$HOME/Workspace/yaver-deploy-runner"
CLONE="${YAVER_DEPLOY_CLONE:-$CLONE_DEFAULT}"

CHECK_ONLY=0
TARGETS=()
for arg in "$@"; do
  case "$arg" in
    --check) CHECK_ONLY=1 ;;
    -*) echo "unknown flag: $arg" >&2; exit 2 ;;
    *) TARGETS+=("$arg") ;;
  esac
done

# A non-interactive `ssh box 'cmd'` gets almost none of the PATH a human sees:
# no shell profile is sourced, so rbenv/asdf shims and Homebrew are simply
# absent. That is how a mobile deploy dies at `pod: command not found` on a box
# where `pod` works perfectly the moment you log in — the tool is installed,
# the shell just cannot see it. Same class as the tmux-not-on-PATH failure that
# silently killed autorun. Put the usual suspects back before anything runs.
for d in "$HOME/.rbenv/shims" "$HOME/.asdf/shims" /opt/homebrew/bin /usr/local/bin "$HOME/.local/bin"; do
  [ -d "$d" ] && case ":$PATH:" in *":$d:"*) ;; *) PATH="$d:$PATH" ;; esac
done
export PATH

# MUTUAL EXCLUSION. Two concurrent runs of this script are not merely wasteful,
# they corrupt each other: the churn-reset below does `git checkout` on
# Info.plist and project.pbxproj, and doing that underneath a running xcodebuild
# kills the archive ~20 minutes in. Observed exactly that on 2026-07-20 —
# `mini-deploy.sh npm` was started while TestFlight was archiving, the archive
# died, and its xcodebuild then ORPHANED itself (parent gone, child still
# running, still holding the deploy lease) so the next attempt was refused too.
# One build lost to the race, a second to the lease it left behind.
#
# flock(1) does not exist on macOS, so use mkdir: atomic on every POSIX fs.
LOCK_DIR="${TMPDIR:-/tmp}/yaver-mini-deploy.lock"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  HOLDER_PID="$(cat "$LOCK_DIR/pid" 2>/dev/null || echo '?')"
  # Reclaim a lock whose holder is gone — a killed run must not block the box
  # forever. This is the same liveness check the deploy LEASE is missing.
  if [ "$HOLDER_PID" != "?" ] && ! kill -0 "$HOLDER_PID" 2>/dev/null; then
    echo "  reclaiming stale lock from dead pid $HOLDER_PID"
    rm -rf "$LOCK_DIR"; mkdir "$LOCK_DIR" 2>/dev/null || { echo "lost the race for the lock; try again"; exit 1; }
  else
    echo "Another mini-deploy is running (pid $HOLDER_PID). Deploys on this box are"
    echo "SEQUENTIAL by design — parallel Xcode/Gradle builds exhaust its RAM and SSD,"
    echo "and a concurrent git checkout kills a running archive. Wait for it."
    exit 1
  fi
fi
echo $$ > "$LOCK_DIR/pid"
trap 'rm -rf "$LOCK_DIR"' EXIT INT TERM

say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mOK\033[0m    %s\n' "$*"; }
bad()  { printf '  \033[31mBLOCKED\033[0m %s\n' "$*"; }
note() { printf '        %s\n' "$*"; }

# ---------------------------------------------------------------- pull

say "Pull"
if [ ! -d "$CLONE/.git" ]; then
  echo "No deploy clone at $CLONE." >&2
  echo "Create it once:  git clone git@github.com:yaver-io/yaver.io.git $CLONE" >&2
  exit 2
fi
cd "$CLONE"
REMOTE="$(git remote | head -1)"

# Deploying DIRTIES this clone, so the next pull refuses and every run after
# the first one dies at "Your local changes would be overwritten by merge":
#   - deploy-testflight.sh bumps CFBundleVersion in Info.plist (and pbxproj),
#   - npm install rewrites package-lock.json in mobile/ and web/.
# All of it is machine-generated churn, none of it is anyone's work, and the
# build number is re-derived from max(local, App Store Connect) on every run.
# Discard exactly those paths — an explicit allowlist, never a blanket
# `git reset --hard`, because a surprise modification ANYWHERE ELSE is a real
# signal (someone edited the deploy clone) and must still stop the run.
DEPLOY_CHURN=(
  mobile/ios/Yaver/Info.plist
  mobile/ios/Yaver.xcodeproj/project.pbxproj
  mobile/android/app/build.gradle
  mobile/package-lock.json
  web/package-lock.json
)
for f in "${DEPLOY_CHURN[@]}"; do
  if [ -n "$(git status --porcelain -- "$f" 2>/dev/null)" ]; then
    git checkout -- "$f" 2>/dev/null && echo "  reset deploy churn: $f"
  fi
done

# --ff-only on purpose: this clone must never carry local commits. If this
# still fails, something outside the churn list was edited here, and that is
# the bug to fix — not something to force past.
git fetch -q "$REMOTE" main
git checkout -q main
git pull -q --ff-only "$REMOTE" main
echo "  $(git log --oneline -1)"

# ------------------------------------------------------------ preflight
#
# Every check below probes the real capability. This file exists because
# "the credential is present" and "the credential works" turned out to be
# different answers on this exact machine.

say "Preflight"
NPM_OK=0; CF_OK=0; ASC_OK=0; PLAY_OK=0; CONVEX_OK=0

# npm — the mini had NO ~/.npmrc token at all until 2026-07-19, so it could
# never publish. whoami is the honest probe: npm answers a bad PUBLISH token
# with 404 (it hides package existence), which reads as "package missing".
if npm whoami >/dev/null 2>&1; then
  NPM_OK=1; ok "npm — authenticated as $(npm whoami 2>/dev/null)"
else
  bad "npm — no valid token in ~/.npmrc"
  note "fix: echo '//registry.npmjs.org/:_authToken=<token>' >> ~/.npmrc && chmod 600 ~/.npmrc"
fi

# Cloudflare — wrangler's OAuth session expires and CANNOT be refreshed in a
# non-interactive shell. Do NOT copy the MacBook's wrangler config here:
# Cloudflare rotates refresh tokens on use, so this box refreshing would
# invalidate the MacBook's session and break the machine that works.
if [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then
  CF_OK=1; ok "cloudflare — CLOUDFLARE_API_TOKEN set"
elif [ -f "$HOME/.cloudflare/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.cloudflare/yaver.env"; set +a
  if [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then CF_OK=1; ok "cloudflare — token from ~/.cloudflare/yaver.env"; fi
fi
if [ "$CF_OK" != "1" ]; then
  bad "cloudflare — no API token, and wrangler's OAuth session cannot refresh headlessly"
  note "fix (once): create a token with 'Edit Cloudflare Workers' at"
  note "  https://dash.cloudflare.com/profile/api-tokens   then on this box:"
  note "  mkdir -p ~/.cloudflare && chmod 700 ~/.cloudflare"
  note "  printf 'export CLOUDFLARE_API_TOKEN=%s\\nexport CLOUDFLARE_ACCOUNT_ID=%s\\n' <token> <account-id> > ~/.cloudflare/yaver.env"
  note "  chmod 600 ~/.cloudflare/yaver.env"
fi

# TestFlight — the historical failure is NOT a missing certificate. It is a
# signing key in a LOCKED keychain, which a non-GUI session cannot open, and
# it surfaces as the maximally unhelpful `errSecInternalComponent`. Probe by
# actually signing something.
if [ -f "$HOME/.appstoreconnect/yaver.env" ] && [ -f "$HOME/.yaver/local-secrets.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.yaver/local-secrets.env"; set +a
  KC="${YAVER_CI_KEYCHAIN_PATH:-$HOME/Library/Keychains/yaver-ci.keychain-db}"
  if [ -n "${YAVER_CI_KEYCHAIN_PASSWORD:-}" ] \
     && security unlock-keychain -p "$YAVER_CI_KEYCHAIN_PASSWORD" "$KC" >/dev/null 2>&1; then
    security set-keychain-settings "$KC" >/dev/null 2>&1 || true
    security set-key-partition-list -S apple-tool:,apple: -s \
      -k "$YAVER_CI_KEYCHAIN_PASSWORD" "$KC" >/dev/null 2>&1 || true
    # BOTH keychains, or the archive dies at CodeSign .../*.appex.
    #
    # The signing identity spans two keychains: Apple DISTRIBUTION lives in
    # yaver-ci.keychain, but Apple DEVELOPMENT private keys live in
    # login.keychain — and during an App Store archive Xcode signs the
    # app-extension / watch intermediates with the DEVELOPMENT identity before
    # re-signing with distribution at export. Unlocking only the CI keychain
    # gets you all the way to `CodeSign .../YaverActivity.appex` and then a
    # bare "(2 failures)" with no cause named. Observed exactly that on
    # 2026-07-19. See CLAUDE.md "Headless codesign".
    LKC="${YAVER_LOGIN_KEYCHAIN_PATH:-$HOME/Library/Keychains/login.keychain-db}"
    if [ -n "${YAVER_LOGIN_PASSWORD:-}" ] && [ -f "$LKC" ]; then
      security unlock-keychain -p "$YAVER_LOGIN_PASSWORD" "$LKC" >/dev/null 2>&1 || true
      # No flags = never auto-lock. A ~20 min archive will otherwise relock
      # the keychain mid-build and fail at a random extension.
      security set-keychain-settings "$LKC" >/dev/null 2>&1 || true
      security set-key-partition-list -S apple-tool:,apple: -s \
        -k "$YAVER_LOGIN_PASSWORD" "$LKC" >/dev/null 2>&1 || true
    fi
    SHA="$(security find-identity -v -p codesigning "$KC" 2>/dev/null \
           | grep 'Apple Distribution' | head -1 | awk '{print $2}')"
    if [ -n "$SHA" ]; then
      cp /bin/echo /tmp/.yaver-cs-probe 2>/dev/null || true
      if codesign --force --keychain "$KC" -s "$SHA" /tmp/.yaver-cs-probe >/dev/null 2>&1; then
        # Distribution signs. Now prove DEVELOPMENT signs too — that is the
        # identity the archive uses for app-extension intermediates, and it is
        # the half that fails 20 minutes in with an unexplained "(2 failures)".
        DSHA="$(security find-identity -v -p codesigning "$LKC" 2>/dev/null \
                | grep 'Apple Development' | head -1 | awk '{print $2}')"
        if [ -z "$DSHA" ]; then
          bad "testflight — no Apple Development identity in $LKC (appex signing would fail)"
        elif codesign --force --keychain "$LKC" -s "$DSHA" /tmp/.yaver-cs-probe >/dev/null 2>&1; then
          ASC_OK=1; ok "testflight — real codesign OK with BOTH Distribution and Development"
        else
          bad "testflight — Distribution signs but DEVELOPMENT cannot; archive dies at CodeSign .../*.appex"
          note "login.keychain is locked or has no partition list; set YAVER_LOGIN_PASSWORD in ~/.yaver/local-secrets.env"
        fi
      else
        bad "testflight — identity present but CANNOT SIGN (the errSecInternalComponent case)"
        note "the cert is not the problem; the private key's keychain is locked or has no partition list"
      fi
      rm -f /tmp/.yaver-cs-probe
    else
      bad "testflight — no Apple Distribution identity in $KC"
    fi
  else
    bad "testflight — cannot unlock $KC (YAVER_CI_KEYCHAIN_PASSWORD missing/wrong)"
  fi
else
  bad "testflight — need ~/.appstoreconnect/yaver.env and ~/.yaver/local-secrets.env"
fi

# Play — keys being present is not the probe. On 2026-07-19 every file was in
# place and uploads would still have died: google-auth was not installed.
#
# Then it got worse in an instructive way. This box has THREE python3s —
# /usr/local/bin, /opt/homebrew/bin, and Xcode's — and "install google-auth"
# was true for one of them at a time. Which one answers `python3` depends on
# PATH order, so the library kept appearing and disappearing as the PATH block
# above changed. Do not chase interpreters: find one that can actually import
# the library and pin it for the deploy step too, so probe and run can never
# disagree about which python they meant.
PY=""
if [ -f keys/google-play-service-account.json ] && [ -f keys/yaver-upload.keystore ]; then
  for cand in python3 /usr/local/bin/python3 /opt/homebrew/bin/python3 /usr/bin/python3; do
    if command -v "$cand" >/dev/null 2>&1 &&
       "$cand" -c 'import google.oauth2.service_account' >/dev/null 2>&1; then
      PY="$cand"; break
    fi
  done
  if [ -n "$PY" ]; then
    PLAY_OK=1; ok "playstore — keys + google-auth present (using $("$PY" -c 'import sys;print(sys.executable)'))"
  else
    bad "playstore — no python3 on this box can import google-auth"
    note "the interpreter that answers 'python3' here is: $(command -v python3 || echo none)"
    note "fix: $(command -v python3 || echo python3) -m pip install --break-system-packages google-auth google-auth-httplib2 google-api-python-client"
  fi
else
  bad "playstore — missing keys/google-play-service-account.json or keys/yaver-upload.keystore"
fi
export YAVER_PYTHON="${PY:-python3}"

# Convex — deliberately does NOT run `convex env list`: that prints SECRET
# VALUES to stdout.
#
# Credentials are only half the question. The first version of this check
# passed on CLI+token alone and the deploy then died with "No CONVEX_DEPLOYMENT
# set" — a false green in the very script written to stop false greens. A fresh
# clone has no backend/.env.local (gitignored), so it is authenticated to
# Convex and pointed at nothing. Both halves, or it is not ready.
CONVEX_DEPLOYMENT_SET=0
if [ -n "${CONVEX_DEPLOYMENT:-}" ]; then
  CONVEX_DEPLOYMENT_SET=1
elif [ -f backend/.env.local ] && grep -q '^CONVEX_DEPLOYMENT=' backend/.env.local; then
  CONVEX_DEPLOYMENT_SET=1
fi
if [ ! -f "$HOME/.convex/config.json" ]; then
  bad "convex — not logged in (run: npx convex login)"
elif ! (cd backend && npx --no-install convex --version >/dev/null 2>&1); then
  bad "convex — CLI unavailable in backend/ (run: npm install)"
elif [ "$CONVEX_DEPLOYMENT_SET" != "1" ]; then
  bad "convex — authenticated but NO DEPLOYMENT configured; a fresh clone has no backend/.env.local"
  note "fix: copy it from a working checkout on this box —"
  note "  cp ~/Workspace/yaver.io/backend/.env.local $CLONE/backend/.env.local && chmod 600 $CLONE/backend/.env.local"
else
  CONVEX_OK=1; ok "convex — logged in and deployment configured"
fi

if [ "$CHECK_ONLY" = "1" ]; then
  say "Check only — nothing deployed"
  exit 0
fi

# ---------------------------------------------------------------- deploy
#
# One at a time. Never background a step, never run two builds at once.

wants() {
  [ ${#TARGETS[@]} -eq 0 ] && return 0
  for t in "${TARGETS[@]}"; do [ "$t" = "$1" ] && return 0; done
  return 1
}

FAILED=()
run_step() { # name, guard, command...
  local name="$1" guard="$2"; shift 2
  wants "$name" || return 0
  if [ "$guard" != "1" ]; then
    printf '\n  \033[33mSKIP\033[0m  %s — preflight blocked (see above)\n' "$name"
    return 0
  fi
  say "Deploy: $name"
  if "$@"; then ok "$name deployed"; else bad "$name FAILED"; FAILED+=("$name"); fi
}

# A pulled clone has source but no node_modules, and `git pull` never brings
# them. Left implicit, convex dies with a baffling `Could not resolve
# "convex/server"` (esbuild, not Convex, and nothing about the message says
# "run npm install"). Each step installs its own deps first — idempotent and
# nearly free once warm.
# npm — publish the CLI. This step existed as a PREFLIGHT with no deploy, so
# the script would cheerfully report "npm OK" and then never publish anything.
#
# ORDER IS LOAD-BEARING: cli/src/postinstall.js downloads the platform tarballs
# from the GitHub Release for this exact version, with NO retry. Publishing to
# npm before that release exists hard-fails every install in the window. So we
# refuse unless the release is already there with assets — CI builds them
# (signed + notarized for darwin, which this box cannot do for linux/windows
# anyway). npm last, always.
run_step npm "$NPM_OK" bash -c '
  set -e
  VERSION=$(python3 -c "import json;print(json.load(open(\"versions.json\"))[\"cli\"])")
  PKG=$(python3 -c "import json;print(json.load(open(\"cli/package.json\"))[\"version\"])")
  if [ "$VERSION" != "$PKG" ]; then
    echo "versions.json ($VERSION) != cli/package.json ($PKG) — refusing to publish a mismatched version"; exit 1
  fi
  LIVE=$(npm view yaver-cli version 2>/dev/null || echo none)
  if [ "$LIVE" = "$VERSION" ]; then
    echo "npm already serves $VERSION — nothing to publish"; exit 0
  fi
  ASSETS=$(gh release view "v$VERSION" --json assets -q ".assets|length" 2>/dev/null || echo 0)
  if [ "${ASSETS:-0}" -lt 5 ]; then
    echo "GitHub release v$VERSION has ${ASSETS:-0} asset(s); postinstall needs the platform tarballs."
    echo "Run the build+release first:  gh workflow run release-cli.yml --ref main"
    exit 1
  fi
  cd cli && npm ci --silent && npm publish --access public
  cd .. && sleep 5
  GOT=$(npm view yaver-cli version 2>/dev/null || echo none)
  [ "$GOT" = "$VERSION" ] || { echo "npm still serves $GOT after publish"; exit 1; }
  echo "npm serves $GOT"'

run_step convex "$CONVEX_OK" bash -c 'cd backend && npm install --silent && npx convex deploy --yes'
run_step web    "$CF_OK"     bash -c 'cd web && npm install --silent && cd .. && ./scripts/deploy-web.sh'
# mobile/ios/ is gitignored apart from a few force-added overlays, so a FRESH
# CLONE has Yaver.xcodeproj and the Swift files but NO Podfile — `pod install`
# dies with "No `Podfile' found in the project directory" and the archive never
# starts. The Podfile is generated by expo prebuild. Generate it, then restore
# the force-tracked overlays prebuild just clobbered (CLAUDE.md "Cold-start
# mobile rebuild"), then pods. Skipped entirely once Pods/ exists.
run_step testflight "$ASC_OK" bash -c '
  set -e
  if [ ! -d mobile/ios/Pods ]; then
    ( cd mobile && npm install --legacy-peer-deps )
    # --clean is REQUIRED, not optional. A clone has a PARTIAL mobile/ios/
    # (only the force-added overlays), so without it prebuild takes its
    # "modify an existing project" path and dies on the first generated file
    # the clone does not carry:
    #   [ios.expoPlist]: ENOENT ... mobile/ios/Yaver/Supporting/Expo.plist
    # --clean regenerates ios/ whole; the checkout below then puts the
    # force-tracked overlays back over it. Safe here because this clone is
    # disposable and mobile/ios/ is gitignored anyway.
    ( cd mobile && npx expo prebuild --platform ios --clean --no-install )
    git checkout -- mobile/ios/
    cp mobile/sdk-manifest.json mobile/ios/Yaver/sdk-manifest.json
    ( cd mobile/ios && pod install )
  fi
  ./scripts/deploy-testflight.sh'
run_step playstore  "$PLAY_OK" bash -c 'JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json "$YAVER_PYTHON" scripts/upload-playstore.py'

say "Done"
if [ ${#FAILED[@]} -gt 0 ]; then
  echo "  failed: ${FAILED[*]}"
  exit 1
fi
echo "  all requested targets deployed"
