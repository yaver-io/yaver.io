#!/bin/sh
# lease-contention-check.sh — prove the fleet lease tier ACROSS REAL MACHINES.
#
# Why this exists as a script rather than a Go test:
#
# The git-ref CAS tier had full unit-test coverage and had never once been run
# against two machines. Unit tests cannot exercise the thing the design actually
# claims — that the REMOTE arbitrates, so there is no leader to elect and none to
# die. A single-process test proves the Go logic; it says nothing about whether
# two hosts pushing the same ref at the same instant produce exactly one winner.
#
# What it verifies, in order:
#   1. Two machines contending at the same wall-clock second -> exactly ONE wins.
#   2. The loser retrying while the lease is held -> still loses (exclusion holds
#      over time, not just at the instant of the race).
#   3. After the holder releases -> the loser CAN take it (no starvation, and no
#      lease wedged forever if a run ends).
#
# Faithful to desktop/agent/autorun_leases_git.go in the ways that matter: the
# refs/yaver/lease/ namespace, CAS via push with no --force anywhere (forcing
# would defeat the mutual exclusion this exists to provide), and a record that
# carries only holder/machine/ttl — never a path, prompt, or diff, per the
# Convex privacy contract.
#
# Usage:
#   scripts/lease-contention-check.sh <ssh-target>
#   e.g. scripts/lease-contention-check.sh user@host
#
# The remote needs git and SSH access. It does NOT need Go — the primitive under
# test is plain git, which is the point. Everything lives in /tmp on both ends
# and is removed on exit, including on failure.
set -u

PEER="${1:-}"
if [ -z "$PEER" ]; then
  echo "usage: $0 <ssh-target>   (a second machine you can ssh to)" >&2
  exit 2
fi

ARENA=/tmp/lease-arena-$$.git
REF=refs/yaver/lease/build/testflight-ios
LOCAL_WORK=/tmp/lease-local-$$
REMOTE_SH=/tmp/lease-contend-$$.sh
FAILED=0

cleanup() {
  ssh -o BatchMode=yes "$PEER" "rm -rf $ARENA /tmp/lease-remote-$$ $REMOTE_SH" >/dev/null 2>&1
  rm -rf "$LOCAL_WORK" "$LOCAL_WORK-2" "$LOCAL_WORK-3"
}
trap cleanup EXIT INT TERM

contender_body() {
  cat <<'CONTENDER'
set -u
REMOTE="$1"; HOLDER="$2"; MACHINE="$3"; WORK="$4"; START="$5"
REF=refs/yaver/lease/build/testflight-ios
rm -rf "$WORK"; mkdir -p "$WORK"; cd "$WORK" || exit 9
git init -q .
git remote add origin "$REMOTE"
BLOB=$(printf '{"key":"build/testflight-ios","holder":"%s","machine":"%s","ttlSeconds":2700}\n' \
  "$HOLDER" "$MACHINE" | git hash-object -w --stdin)
# Spin to a shared instant so the race is real, not serialized by ssh latency.
while [ "$(date +%s)" -lt "$START" ]; do :; done
if git push -q origin "$BLOB:$REF" 2>/dev/null; then
  echo "WON $HOLDER"
else
  echo "LOST $HOLDER"
fi
CONTENDER
}

echo "peer: $PEER"
ssh -o BatchMode=yes -o ConnectTimeout=20 "$PEER" "git init -q --bare $ARENA" || {
  echo "FAIL: could not create arena on $PEER" >&2; exit 1; }
contender_body | ssh -o BatchMode=yes "$PEER" "cat > $REMOTE_SH && chmod +x $REMOTE_SH"
contender_body > "$REMOTE_SH.local" && chmod +x "$REMOTE_SH.local"

START=$(( $(date +%s) + 10 ))
echo
echo "1. simultaneous contention (both spin to epoch $START)"
ssh -o BatchMode=yes "$PEER" "$REMOTE_SH $ARENA run-peer peer /tmp/lease-remote-$$ $START" > /tmp/peer-$$.out 2>&1 &
P=$!
"$REMOTE_SH.local" "$PEER:$ARENA" run-local local "$LOCAL_WORK" "$START" > /tmp/local-$$.out 2>&1 &
L=$!
wait $P $L
PEER_R=$(cat /tmp/peer-$$.out); LOCAL_R=$(cat /tmp/local-$$.out)
echo "   peer : $PEER_R"
echo "   local: $LOCAL_R"
WINS=0
case "$PEER_R" in WON*) WINS=$((WINS+1));; esac
case "$LOCAL_R" in WON*) WINS=$((WINS+1));; esac
if [ "$WINS" -eq 1 ]; then
  echo "   PASS exactly one winner"
else
  echo "   FAIL expected exactly 1 winner, got $WINS"; FAILED=1
fi

echo
echo "2. retry while held -> must lose"
R2=$("$REMOTE_SH.local" "$PEER:$ARENA" run-local-retry local "$LOCAL_WORK-2" "$(date +%s)" 2>&1 | tail -1)
case "$R2" in
  LOST*) echo "   PASS $R2" ;;
  *)     echo "   FAIL a held lease was acquired twice: $R2"; FAILED=1 ;;
esac

echo
echo "3. release -> the other machine can take it"
ssh -o BatchMode=yes "$PEER" "cd $ARENA && git update-ref -d $REF"
R3=$("$REMOTE_SH.local" "$PEER:$ARENA" run-local-after local "$LOCAL_WORK-3" "$(date +%s)" 2>&1 | tail -1)
case "$R3" in
  WON*) echo "   PASS $R3" ;;
  *)    echo "   FAIL lease not reacquirable after release (starvation): $R3"; FAILED=1 ;;
esac

rm -f "$REMOTE_SH.local" /tmp/peer-$$.out /tmp/local-$$.out
echo
if [ "$FAILED" -eq 0 ]; then echo "ALL PASS — remote-arbitrated mutual exclusion holds across machines"; exit 0; fi
echo "FAILURES — see above"; exit 1
