package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsPrivateLanIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		// RFC1918 — true.
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.111.25", true},
		// Outside RFC1918 — false. 100.x is Tailscale CGNAT, not LAN.
		{"100.64.0.1", false},
		{"100.89.155.25", false},
		{"100.127.255.255", false},
		// Public.
		{"8.8.8.8", false},
		// Just-outside-172/12.
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		// Loopback / link-local.
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		// Empty / garbage.
		{"", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		got := isPrivateLanIPv4(net.ParseIP(tc.ip))
		if got != tc.want {
			t.Errorf("isPrivateLanIPv4(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIsCGNATTailscaleIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.0", true},
		{"100.89.155.25", true},
		{"100.127.255.255", true},
		// Just outside 100.64/10.
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		// RFC1918.
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		// Public.
		{"100.0.0.1", false},
		// Garbage.
		{"", false},
		{"100.64", false},
	}
	for _, tc := range cases {
		got := isCGNATTailscaleIP(tc.ip)
		if got != tc.want {
			t.Errorf("isCGNATTailscaleIP(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestSameIPv4Slash24(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"192.168.111.25", "192.168.111.8", true},
		{"192.168.111.25", "192.168.112.8", false},
		{"10.0.0.1", "10.0.0.99", true},
		{"10.0.0.1", "10.0.1.1", false},
		{"172.16.0.5", "172.16.0.99", true},
		// IPv6 — always false (we only compare /24 of v4).
		{"::1", "192.168.1.1", false},
	}
	for _, tc := range cases {
		got := sameIPv4Slash24(net.ParseIP(tc.a), net.ParseIP(tc.b))
		if got != tc.want {
			t.Errorf("sameIPv4Slash24(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestPickReachableLanIP_NoLocalsReturnsEmpty shoves an empty locals
// list at the picker by stubbing the interface scan would be ideal,
// but we can't easily — so instead verify the candidate-side filters
// rule out non-RFC1918 IPs even when a local match would exist. The
// no-locals case is exercised implicitly: if the test host has no
// RFC1918 interfaces (CI inside Docker), pickReachableLanIP returns
// "" regardless of candidate quality.
func TestPickReachableLanIP_RejectsNonLanCandidates(t *testing.T) {
	// All of these should be rejected even on a host with active LAN.
	for _, bad := range []string{
		"100.64.0.5",    // Tailscale CGNAT
		"100.89.155.25", // Tailscale CGNAT (the regression IP)
		"8.8.8.8",       // public
		"127.0.0.1",     // loopback
		"::1",           // ipv6 loopback
		"169.254.1.1",   // link-local
		"",              // empty
		"garbage",       // unparseable
		"172.17.0.1",    // docker bridge default
	} {
		if got := pickReachableLanIP([]string{bad}); got != "" {
			t.Errorf("pickReachableLanIP(%q) = %q, want \"\"", bad, got)
		}
	}
}

// TestPickReachableLanIP_PicksFirstSubnetMatch verifies preference
// ordering when multiple LAN candidates are on different subnets:
// the first one whose /24 matches a local interface wins. We discover
// the host's actual local /24 first, then craft candidates around it.
func TestPickReachableLanIP_PicksFirstSubnetMatch(t *testing.T) {
	locals, err := localInterfacePrivateIPv4s()
	if err != nil || len(locals) == 0 {
		t.Skip("no RFC1918 local interface — can't test subnet matching")
	}
	local := locals[0].To4()
	// Off-subnet first, on-subnet second — picker must skip the
	// off-subnet one and return the on-subnet one.
	off := net.IPv4(10, 99, 99, 99).String()
	on := net.IPv4(local[0], local[1], local[2], 200).String()
	got := pickReachableLanIP([]string{off, on})
	if got != on {
		t.Errorf("pickReachableLanIP([%q,%q]) = %q, want %q", off, on, got, on)
	}
}

// TestLocalTailscaleUp_ConsistentWithInterfaces is a sanity check
// that the helper agrees with what net.Interfaces actually shows us.
// We can't assert true/false because it depends on the host running
// the test, but we can confirm the function doesn't panic and
// returns the same answer twice in a row.
func TestLocalTailscaleUp_StableAcrossCalls(t *testing.T) {
	a := localTailscaleUp()
	b := localTailscaleUp()
	if a != b {
		t.Fatalf("localTailscaleUp returned different values across calls: %v then %v", a, b)
	}
}

func TestTailscaleStateLabel(t *testing.T) {
	if got := tailscaleStateLabel(true); got == "" {
		t.Fatalf("tailscaleStateLabel(true) returned empty label")
	}
	if got := tailscaleStateLabel(false); got == "" {
		t.Fatalf("tailscaleStateLabel(false) returned empty label")
	}
}

func TestFirstDialablePrivateIP_FiltersAndUnreachable(t *testing.T) {
	// Non-private and bogus candidates are skipped; an unreachable
	// private IP returns "" within the timeout budget rather than
	// blocking. 192.0.2.1 is TEST-NET-1 (RFC5737) but not RFC1918, so
	// it's filtered before any dial — keeps the test fast and offline.
	got := firstDialablePrivateIP(
		[]string{"", "not-an-ip", "8.8.8.8", "192.0.2.1", "172.17.0.1"},
		"22", 200*time.Millisecond,
	)
	if got != "" {
		t.Fatalf("firstDialablePrivateIP returned %q; want \"\" (all candidates non-private/docker/bogus)", got)
	}
}

func TestFirstDialablePrivateIP_ReachableWins(t *testing.T) {
	// Bind a real listener on a private interface IP (no mocks). When
	// the machine has no RFC1918 interface (some CI runners), skip.
	locals, err := localInterfacePrivateIPv4s()
	if err != nil || len(locals) == 0 {
		t.Skip("no RFC1918 interface available to bind a listener")
	}
	ip := locals[0].String()
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("cannot bind listener on %s: %v", ip, err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// A dead private IP first, then the live one — proves we keep
	// probing past an unreachable candidate and return the reachable one.
	got := firstDialablePrivateIP([]string{"10.255.255.1", ip}, port, 300*time.Millisecond)
	if got != ip {
		t.Fatalf("firstDialablePrivateIP = %q; want reachable %q", got, ip)
	}
}

func TestFirstDialableSameSubnetLanIPRequiresOpenPort(t *testing.T) {
	locals, err := localInterfacePrivateIPv4s()
	if err != nil || len(locals) == 0 {
		t.Skip("no RFC1918 interface available to bind a listener")
	}
	ip := locals[0].String()
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("cannot bind listener on %s: %v", ip, err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	if got := firstDialableSameSubnetLanIP([]string{ip}, port, 300*time.Millisecond); got != ip {
		t.Fatalf("firstDialableSameSubnetLanIP = %q; want reachable same-subnet %q", got, ip)
	}

	_ = ln.Close()
	if got := firstDialableSameSubnetLanIP([]string{ip}, port, 100*time.Millisecond); got != "" {
		t.Fatalf("firstDialableSameSubnetLanIP returned %q after listener closed; want empty", got)
	}
}

func TestTCPPortDialable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	if !tcpPortDialable(host, port, 300*time.Millisecond) {
		t.Fatalf("tcpPortDialable(%s,%s) = false; want true", host, port)
	}
	_ = ln.Close()
	if tcpPortDialable(host, port, 100*time.Millisecond) {
		t.Fatalf("tcpPortDialable(%s,%s) = true after close; want false", host, port)
	}
}

func TestFirstDialableHostConcurrentKeepsPreferenceWithinOneBudget(t *testing.T) {
	first, err := listenWithSSHBanner("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := listenWithSSHBanner("127.0.0.2:0")
	if err != nil {
		t.Skipf("cannot bind second loopback address: %v", err)
	}
	defer second.Close()
	firstHost, firstPort, _ := net.SplitHostPort(first.Addr().String())
	secondHost, secondPort, _ := net.SplitHostPort(second.Addr().String())
	if firstPort != secondPort {
		// Rebind the second listener on the same port so candidates share the
		// operation's port, just like SSH routes do.
		second.Close()
		second, err = listenWithSSHBanner(net.JoinHostPort(secondHost, firstPort))
		if err != nil {
			t.Skipf("cannot bind second loopback address on shared port: %v", err)
		}
		defer second.Close()
	}
	started := time.Now()
	got := firstDialableHostConcurrent([]string{"192.0.2.1", firstHost, secondHost}, firstPort, 250*time.Millisecond)
	if got != firstHost {
		t.Fatalf("got %q, want highest-preference live host %q", got, firstHost)
	}
	if elapsed := time.Since(started); elapsed >= 600*time.Millisecond {
		t.Fatalf("route probes multiplied their timeout instead of sharing it: %s", elapsed)
	}
}

func listenWithSSHBanner(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte("SSH-2.0-yaver-test\r\n"))
			}()
		}
	}()
	return listener, nil
}

func TestSSHHandshakeDialableRejectsAcceptWithoutBanner(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	if sshHandshakeDialable(host, port, 100*time.Millisecond) {
		t.Fatal("TCP accept without an SSH banner must not be selected")
	}
	select {
	case conn := <-accepted:
		conn.Close()
	default:
	}
}

func TestOrderedSSHRouteCandidatesSkipsDeadOverlayEligibility(t *testing.T) {
	dev := &DeviceInfo{
		LocalIps:        []string{"100.75.123.78", "10.254.253.252"},
		PublicEndpoints: []string{"https://public.yaver.io/d/device-test", "198.51.100.7:18080"},
		QuicHost:        "100.75.123.78",
	}
	got := orderedSSHRouteCandidates(dev, false, false)
	joined := strings.Join(got, ",")
	if strings.Contains(joined, "100.75.123.78") {
		t.Fatalf("down Tailscale address leaked into candidates: %v", got)
	}
	if strings.Contains(joined, "public.yaver.io") {
		t.Fatalf("HTTP relay host leaked into SSH candidates: %v", got)
	}
	if !strings.Contains(joined, "198.51.100.7") {
		t.Fatalf("public SSH endpoint missing from candidates: %v", got)
	}
}

func TestSplitDestUserHost(t *testing.T) {
	cases := []struct {
		dest     string
		wantUser string
		wantHost string
	}{
		{"pokayoke@192.168.111.25", "pokayoke", "192.168.111.25"},
		{"root@example.com", "root", "example.com"},
		{"192.168.111.25", "", "192.168.111.25"},
		{"", "", ""},
		// Edge: leading `@` (no user). LastIndex returns 0 → not >0
		// → treated as bare host.
		{"@example.com", "", "@example.com"},
		// Edge: multiple `@` (e.g., `user@host@something` would be
		// odd but we take the last one as the separator).
		{"user@host.example@oddtag", "user@host.example", "oddtag"},
	}
	for _, tc := range cases {
		gotUser, gotHost := splitDestUserHost(tc.dest)
		if gotUser != tc.wantUser || gotHost != tc.wantHost {
			t.Errorf("splitDestUserHost(%q) = (%q,%q); want (%q,%q)",
				tc.dest, gotUser, gotHost, tc.wantUser, tc.wantHost)
		}
	}
}
