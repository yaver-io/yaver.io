#!/usr/bin/env bash
# Runs on the remote box after sync-repo. Smoke-tests the installed
# toolchain + a few Yaver primitives; exits non-zero on any failure.
#
# Intentionally short — integration tests that take minutes belong
# in their own verify-*.sh scripts so failures are easy to localise.
set -euo pipefail

REPO=/opt/yaver
mkdir -p /var/log/yaver-ci
LOG=/var/log/yaver-ci/verify.log
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }

banner "system"
uname -a
free -h
df -h /

banner "toolchain versions"
docker --version
node --version
/usr/local/go/bin/go version
python3 --version
ollama --version 2>&1 | head -1
aider --version 2>&1 | head -1
opencode --version 2>&1 | head -1 || echo "opencode missing from PATH"
yaver --version 2>&1 | head -1 || echo "yaver missing"

banner "docker smoke test"
docker run --rm hello-world | tail -5

banner "ollama smoke test"
if ! ollama list | grep -q '^qwen2.5-coder:1.5b'; then
  echo "qwen2.5-coder:1.5b not pulled — pulling now"
  ollama pull qwen2.5-coder:1.5b
fi
ollama run qwen2.5-coder:1.5b 'print hello in python one line' </dev/null | head -5

banner "yaver help"
yaver --help 2>&1 | head -20 || true

banner "yaver go test (desktop/agent)"
cd "$REPO/desktop/agent"
export PATH=/usr/local/go/bin:$PATH
go test -count=1 -timeout 5m ./... 2>&1 | tail -40

banner "verify done"
