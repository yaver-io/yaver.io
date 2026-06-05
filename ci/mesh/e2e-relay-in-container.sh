#!/usr/bin/env bash
# Yaver Mesh RELAY-as-DERP end-to-end test — proves the symmetric-NAT fallback:
# two agents that CANNOT reach each other directly still form a WireGuard tunnel
# by relaying encrypted frames through the relay's mesh_relay stream.
#
# Topology (one privileged Linux container):
#   root netns: /relay serve (QUIC :4433)
#   ns1 (agentA) --veth-- root (10.1.0.0/24)   } different /24s, NO route between
#   ns2 (agentB) --veth-- root (10.2.0.0/24)   } ns1 and ns2 => no direct path
# Both namespaces reach the relay; neither reaches the other. PASS = overlay ping.
set -euo pipefail

PASSWORD=meshtest-relay-secret
AID=agent-aaa-001   # >= 8 chars (relay deviceId shape)
BID=agent-bbb-002

echo "== Yaver Mesh relay-DERP e2e: kernel $(uname -sr) =="
mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200

cleanup() {
  kill $(jobs -p) 2>/dev/null || true
  ip netns del ns1 2>/dev/null || true
  ip netns del ns2 2>/dev/null || true
}
trap cleanup EXIT

# Relay in the root namespace.
RELAY_PASSWORD="$PASSWORD" /relay serve --quic-port 4433 --http-port 8443 >/tmp/relay.log 2>&1 &
sleep 2
echo "relay started (root netns :4433)"

A=$(/meshtest keygen); APRIV=${A% *}; APUB=${A#* }
B=$(/meshtest keygen); BPRIV=${B% *}; BPUB=${B#* }

# Two namespaces, each on its own /24 to root. No inter-ns route ⇒ no direct path.
ip netns add ns1; ip netns add ns2
ip link add veth-a type veth peer name veth-a-ns
ip link add veth-b type veth peer name veth-b-ns
ip link set veth-a-ns netns ns1
ip link set veth-b-ns netns ns2
ip addr add 10.1.0.1/24 dev veth-a; ip link set veth-a up
ip addr add 10.2.0.1/24 dev veth-b; ip link set veth-b up
ip netns exec ns1 ip addr add 10.1.0.2/24 dev veth-a-ns; ip netns exec ns1 ip link set veth-a-ns up; ip netns exec ns1 ip link set lo up
ip netns exec ns2 ip addr add 10.2.0.2/24 dev veth-b-ns; ip netns exec ns2 ip link set veth-b-ns up; ip netns exec ns2 ip link set lo up

ip netns exec ns1 ping -c1 -W2 10.1.0.1 >/dev/null && echo "ns1 -> relay ok"
ip netns exec ns2 ping -c1 -W2 10.2.0.1 >/dev/null && echo "ns2 -> relay ok"
if ip netns exec ns1 ping -c1 -W2 10.2.0.2 >/dev/null 2>&1; then
  echo "WARNING: ns1 reaches ns2 directly — test would not exercise the relay"; exit 1
fi
echo "no direct ns1<->ns2 path ✓ (forces relay-DERP)"

# agentA peers agentB through the relay (reached at 10.1.0.1 from ns1); vice-versa.
ip netns exec ns1 /meshtest derp yaver-wg0 100.96.0.2 "$APRIV" "$AID" "$BPUB" "$BID" 100.96.0.3 10.1.0.1:4433 "$PASSWORD" &
ip netns exec ns2 /meshtest derp yaver-wg0 100.96.0.3 "$BPRIV" "$BID" "$APUB" "$AID" 100.96.0.2 10.2.0.1:4433 "$PASSWORD" &
sleep 6

echo "== PING over the overlay via relay-DERP (ns1 100.96.0.2 -> ns2 100.96.0.3) =="
if ip netns exec ns1 ping -c 5 -W 4 100.96.0.3; then
  ip netns exec ns2 ping -c 2 -W 4 100.96.0.2 >/dev/null && echo "reverse direction ✓"
  echo
  echo "RESULT: PASS — WireGuard rides the relay (DERP) with NO direct path ✓"
  exit 0
fi
echo
echo "RESULT: FAIL — no ICMP over the relay-DERP overlay"
echo "--- relay log ---"; tail -20 /tmp/relay.log || true
exit 1
