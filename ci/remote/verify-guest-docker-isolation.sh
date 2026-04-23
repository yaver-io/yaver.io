#!/usr/bin/env bash
# Focused guest Docker-isolation verification on a machine that has Docker.
# This proves the positive enforcement path:
#   guest task + isolation required -> task runs inside Docker,
#   sees only /workspace, and does not inherit host API-key env.
set -euo pipefail

REPO=/opt/yaver
mkdir -p /var/log/yaver-ci
LOG=/var/log/yaver-ci/verify-guest-docker-isolation.log
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }

banner "guest docker isolation remote verify"
uname -a
docker --version
/usr/local/go/bin/go version

banner "focused guest docker isolation tests"
cd "$REPO/desktop/agent"
export PATH=/usr/local/go/bin:$PATH
go test -count=1 -run 'Test(GuestTaskRunsInDockerIsolation|GuestShareLinuxStack_TaskAPIFailsClosedWhenIsolationRequiredWithoutDocker|ApplySandboxQuickstartConfiguresGuestIsolation)' ./...

banner "verify done"
