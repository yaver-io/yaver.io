#!/usr/bin/env bash
# Yaver Mesh DATA-PLANE end-to-end test — runs INSIDE a privileged Linux
# container (real kernel via Docker Desktop's LinuxKit VM on macOS, or native on
# Linux/CI). Two network namespaces joined by a veth act as two NAT'd machines;
# each brings up a real wireguard-go TUN through the mesh package (the prebuilt
# /meshtest binary) and peers the other. PASS = ICMP over the 100.96.x overlay.
#
# No Convex, no OAuth, no cloud cost. Driven by scripts/test-mesh-e2e.sh, which
# cross-compiles /meshtest (cmd/meshtest) and mounts it + this script.
set -euo pipefail

A_IP=100.96.0.2
B_IP=100.96.0.3
UNDERLAY1=10.10.0.1
UNDERLAY2=10.10.0.2
PORT=51820

echo "== Yaver Mesh e2e: kernel $(uname -sr), arch $(uname -m) =="
mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200

A=$(/meshtest keygen); APRIV=${A% *}; APUB=${A#* }
B=$(/meshtest keygen); BPRIV=${B% *}; BPUB=${B#* }
echo "keys: A.pub=${APUB:0:12}…  B.pub=${BPUB:0:12}…"

cleanup() {
  kill $(jobs -p) 2>/dev/null || true
  ip netns del ns1 2>/dev/null || true
  ip netns del ns2 2>/dev/null || true
}
trap cleanup EXIT

# Two namespaces + a veth "physical LAN" between them.
ip netns add ns1; ip netns add ns2
ip link add veth1 type veth peer name veth2
ip link set veth1 netns ns1; ip link set veth2 netns ns2
ip netns exec ns1 ip addr add ${UNDERLAY1}/24 dev veth1
ip netns exec ns2 ip addr add ${UNDERLAY2}/24 dev veth2
for ns in ns1 ns2; do ip netns exec $ns ip link set lo up; done
ip netns exec ns1 ip link set veth1 up
ip netns exec ns2 ip link set veth2 up
ip netns exec ns1 ping -c1 -W2 ${UNDERLAY2} >/dev/null && echo "underlay reachable ✓"

# Bring up the two mesh nodes (real TUN + wireguard-go) in each namespace.
ip netns exec ns1 /meshtest run yaver-wg0 ${A_IP} ${PORT} "$APRIV" "$BPUB" ${UNDERLAY2}:${PORT} ${B_IP} &
ip netns exec ns2 /meshtest run yaver-wg0 ${B_IP} ${PORT} "$BPRIV" "$APUB" ${UNDERLAY1}:${PORT} ${A_IP} &
sleep 4

echo "== ns1 interfaces =="
ip netns exec ns1 ip -br addr show | grep -E "yaver-wg0|veth"

echo "== PING over the overlay (ns1 ${A_IP} -> ns2 ${B_IP}) =="
if ip netns exec ns1 ping -c 4 -W 3 ${B_IP}; then
  ip netns exec ns2 ping -c 2 -W 3 ${A_IP} >/dev/null && echo "reverse direction ✓"
  echo
  echo "RESULT: PASS — WireGuard overlay carries traffic end-to-end ✓"
  exit 0
fi
echo
echo "RESULT: FAIL — no ICMP over the overlay"
exit 1
