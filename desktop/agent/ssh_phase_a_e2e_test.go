package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestPhaseA_ReverseSSHOverRelay_EndToEnd proves the WHOLE reverse-SSH-over-relay
// chain in-process, with a REAL ssh.Client, the REAL embedded control server, and
// the REAL splice glue — the two hops that were only unit-tested separately now
// carry a real SSH session end to end:
//
//   phone ssh.Client ──▶ [relay: write envelope, then splice] ──▶
//     [agent: decode envelope, sentinel? splice to local SSH] ──▶ box SSH server
//
// This is the "spin up a relay and prove Phase A" test, minus two OS processes:
// the relay and agent halves are the exact code paths (envelope framing + the
// WS-style splice + spliceStreamToSSH), wired through net.Pipe the way the QUIC
// tunnel wires them in prod.
func TestPhaseA_ReverseSSHOverRelay_EndToEnd(t *testing.T) {
	// --- The box: the real embedded SSH control server on a loopback port. ---
	hostSigner, _ := newTestSigner(t)
	clientSigner, clientPub := newTestSigner(t)
	dispatch := func(method, path string, body []byte) ([]byte, int) {
		return []byte("E2E " + method + " " + path), 0
	}
	isAuth := func(key ssh.PublicKey) bool {
		return string(ssh.MarshalAuthorizedKey(key)) == string(ssh.MarshalAuthorizedKey(clientPub))
	}
	boxLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer boxLn.Close()
	go newSSHControlServer(hostSigner, isAuth, dispatch).Serve(boxLn)

	// --- The tunnel: two pipes standing in for the QUIC relay tunnel. ---
	phoneEnd, relayPhoneEnd := net.Pipe() // phone ⇄ relay
	relayAgentEnd, agentEnd := net.Pipe() // relay ⇄ agent (the "tunnel stream")

	// --- The relay half: write the JSON envelope for the sentinel path, then
	// splice the phone connection ⇄ the tunnel stream (exactly proxyWebSocket). ---
	go func() {
		env, _ := json.Marshal(relayTunnelRequest{ID: "1", Method: "GET", Path: relaySSHControlSentinelPath})
		relayAgentEnd.Write(env)
		go io.Copy(relayAgentEnd, relayPhoneEnd)
		io.Copy(relayPhoneEnd, relayAgentEnd)
	}()

	// --- The agent half: decode the envelope off the tunnel stream; on the SSH
	// sentinel, splice the rest (incl. decoder over-read) to the box SSH port.
	// This mirrors relayHandleProxiedRequest's new branch exactly. ---
	go func() {
		dec := json.NewDecoder(io.LimitReader(agentEnd, 64<<20))
		var req relayTunnelRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		if req.Path == relaySSHControlSentinelPath {
			_ = spliceStreamToSSH(context.Background(), agentEnd, dec.Buffered(), boxLn.Addr().String())
		}
	}()

	// --- The phone: a REAL ssh.Client over the phone end of the tunnel. ---
	cfg := &ssh.ClientConfig{
		User:            "yaver",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(phoneEnd, "box", cfg)
	if err != nil {
		t.Fatalf("SSH handshake over the relay tunnel failed: %v", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	out, err := sess.Output(`{"verb":"health"}`)
	if err != nil {
		t.Fatalf("health over reverse-SSH-over-relay failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "E2E GET /health" {
		t.Fatalf("end-to-end verb wrong: got %q, want %q", got, "E2E GET /health")
	}
}
