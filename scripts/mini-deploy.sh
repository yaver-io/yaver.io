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
# --ff-only on purpose: this clone must never carry local commits. If this
# fails, someone edited the deploy clone, and that is the bug to fix — not
# something to force past.
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
    SHA="$(security find-identity -v -p codesigning "$KC" 2>/dev/null \
           | grep 'Apple Distribution' | head -1 | awk '{print $2}')"
    if [ -n "$SHA" ]; then
      cp /bin/echo /tmp/.yaver-cs-probe 2>/dev/null || true
      if codesign --force --keychain "$KC" -s "$SHA" /tmp/.yaver-cs-probe >/dev/null 2>&1; then
        ASC_OK=1; ok "testflight — real codesign with Apple Distribution succeeded headlessly"
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
if [ -f keys/google-play-service-account.json ] && [ -f keys/yaver-upload.keystore ]; then
  if python3 -c 'import google.oauth2.service_account' >/dev/null 2>&1; then
    PLAY_OK=1; ok "playstore — service account + keystore + google-auth present"
  else
    bad "playstore — google-auth NOT installed; upload-playstore.py would die on import"
    note "fix: python3 -m pip install --user google-auth google-auth-httplib2 google-api-python-client"
  fi
else
  bad "playstore — missing keys/google-play-service-account.json or keys/yaver-upload.keystore"
fi

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

run_step convex "$CONVEX_OK" bash -c 'cd backend && npx convex deploy --yes'
run_step web    "$CF_OK"     ./scripts/deploy-web.sh
run_step testflight "$ASC_OK" ./scripts/deploy-testflight.sh
run_step playstore  "$PLAY_OK" bash -c 'JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py'

say "Done"
if [ ${#FAILED[@]} -gt 0 ]; then
  echo "  failed: ${FAILED[*]}"
  exit 1
fi
echo "  all requested targets deployed"
