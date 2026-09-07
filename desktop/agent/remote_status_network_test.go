package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestFirstDialableHostFallsThroughIPv4ToIPv6(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
		close(accepted)
	}()

	got := firstDialableHost([]string{"192.0.2.1", "::1"}, fmt.Sprint(port), 100*time.Millisecond)
	if got != "::1" {
		t.Fatalf("firstDialableHost = %q, want IPv6 fallback", got)
	}
	<-accepted
}

func TestClassifyRemoteStatusErrorKeepsLocalNetworkFailureLocal(t *testing.T) {
	err := errors.New(`/info failed: http://192.0.2.10:18080: dial tcp: network is unreachable | https://public.yaver.io/d/device: dial tcp: network is unreachable`)
	target := &DeviceInfo{IsOnline: true}

	cause, hint := classifyRemoteStatusError(err, target)
	if !strings.Contains(cause, "this machine") {
		t.Fatalf("cause = %q, want an explicit local-machine diagnosis", cause)
	}
	if strings.Contains(strings.ToLower(hint), "auth") {
		t.Fatalf("hint = %q, must not prescribe remote auth when no packet left the caller", hint)
	}
	if !strings.Contains(hint, "yaver ssh primary") {
		t.Fatalf("hint = %q, want retry route after local connectivity is restored", hint)
	}
}

func TestTerminalDialFallsThroughDeadDirectCandidateToRelay(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer relay.Close()

	candidates := []RemoteAgentCandidate{
		{DeviceID: "device-1", Kind: "direct", BaseURL: "http://127.0.0.1:1"},
		{DeviceID: "device-1", Kind: "relay", BaseURL: relay.URL},
	}
	conn, label, err := dialFirstTerminalCandidate(candidates, "test-token", "")
	if err != nil {
		t.Fatalf("dialFirstTerminalCandidate: %v", err)
	}
	defer conn.Close()
	if !strings.Contains(label, "via relay") {
		t.Fatalf("label = %q, want relay candidate after direct failure", label)
	}
}

func TestLocalNetworkFailureOutranksStaleRemoteHeartbeat(t *testing.T) {
	err := errors.New("dial tcp 192.0.2.10:22: connect: no route to host")
	cause, _ := classifyRemoteStatusError(err, &DeviceInfo{IsOnline: false})
	if !strings.Contains(cause, "this machine") {
		t.Fatalf("cause = %q, local operation failure must outrank remote inventory", cause)
	}
}

func TestSSHRelayFallbackPreservesCause(t *testing.T) {
	got := sshRelayFallbackFailureMessage(errors.New("no reachable transport: network is unreachable"))
	if !strings.Contains(got, "network is unreachable") {
		t.Fatalf("message = %q, relay failure cause was swallowed", got)
	}
}
