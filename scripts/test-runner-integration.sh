#!/usr/bin/env bash
set -euo pipefail

RUNNER="${1:-}"
if [[ -z "$RUNNER" ]]; then
  echo "usage: $0 <runner-spec>" >&2
  exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
YAVER_BIN="${YAVER_BIN:-}"
WORK_DIR="$(mktemp -d -t yaver-runner-XXXXXX)"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

if [[ -z "$YAVER_BIN" ]]; then
  pushd "$REPO_ROOT/desktop/agent" >/dev/null
  go build -o "$WORK_DIR/yaver" .
  popd >/dev/null
  YAVER_BIN="$WORK_DIR/yaver"
fi

mkdir -p "$WORK_DIR/fixture/src"
cd "$WORK_DIR/fixture"
git init -q .
git config user.email "ci@yaver.test"
git config user.name "yaver-ci"
cat > README.md <<'EOF'
# Fixture
Tiny repo for Yaver runner integration tests.
EOF
cat > package.json <<'EOF'
{"name":"fixture","version":"0.0.1","main":"src/index.js","scripts":{"typecheck":"echo ok","test":"echo ok"}}
EOF
cat > src/index.js <<'EOF'
export function greet(name) {
  return `hello ${name}`;
}
EOF
git add . && git commit -q -m "init"

echo "[runner-test] autoinit via ${RUNNER}"
YAVER_AUTODEV_DETACHED=1 "$YAVER_BIN" autoinit fixture --runner "$RUNNER"

test -f init.md || { echo "init.md not created" >&2; exit 1; }
grep -q "## What is this" init.md || { echo "init.md missing What is this" >&2; exit 1; }
grep -q "## Tech stack" init.md || { echo "init.md missing Tech stack" >&2; exit 1; }
grep -q "yaver:autoinit:generated:start" init.md || { echo "init.md missing markers" >&2; exit 1; }

echo "[runner-test] autoideas via ${RUNNER}"
YAVER_AUTODEV_DETACHED=1 "$YAVER_BIN" autoideas fixture --runner "$RUNNER" --max-batches 1 --tick 1

test -f ideas.md || { echo "ideas.md not created" >&2; exit 1; }
grep -q -- "- \\[ \\]" ideas.md || { echo "ideas.md missing checklist items" >&2; exit 1; }

echo "[runner-test] PASS ${RUNNER}"
