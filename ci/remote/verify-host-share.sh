#!/usr/bin/env bash
# Runs a focused host-share / borrowed-session verification slice on the
# remote Hetzner box. Keep this targeted so local iteration stays fast.
set -euo pipefail

REPO=/opt/yaver
mkdir -p /var/log/yaver-ci
LOG=/var/log/yaver-ci/verify-host-share.log
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }

banner "host-share remote verify"
uname -a
/usr/local/go/bin/go version

banner "focused host-share tests"
cd "$REPO/desktop/agent"
export PATH=/usr/local/go/bin:$PATH
go test -count=1 -run 'Test(AuthAllowsHostShareInfo|AuthRejectsHostShareExec|HandleHostShareFSWriteSupportsBase64AndRootPath|HandleHostShareFSDeleteSupportsRootPath|HostShareWorkspaceBootstrapFromDir|ResolveLocalHostShareRootFallsBackToAnyDirectory|GuestShareLinuxStack_.*)' ./...

banner "verify done"
