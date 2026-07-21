package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestRelayStreamTagIsSSH(t *testing.T) {
	if !relayStreamTagIsSSH(relayStreamTagSSH) {
		t.Error("the SSH tag must route to SSH")
	}
	for _, tag := range []byte{0x00, 0x01, 0x03, 0xff} {
		if relayStreamTagIsSSH(tag) {
			t.Errorf("tag %#x must NOT route to SSH (only the JSON control path)", tag)
		}
	}
}

// bridgeToLocalSSH must faithfully splice bytes both ways between the relay
// stream and the local SSH server — a real loopback "SSH" server that echoes,
// standing in for ssh_control_server. No mocks.
func TestBridgeToLocalSSH_SplicesBothWays(t *testing.T) {
	// Fake local "SSH" server: read a line, echo it back uppercased-ish (just
	// prefix), proving bytes flow server→client too.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		c.Write(append([]byte("ok:"), buf[:n]...))
	}()

	// The "relay stream" is one end of a socket pair; the client writes/reads the
	// other end.
	clientSide, bridgeSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- bridgeToLocalSSH(context.Background(), bridgeSide, ln.Addr().String())
	}()

	// Client → server.
	go func() { clientSide.Write([]byte("hello")) }()
	// Server → client (through the bridge).
	clientSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 64)
	n, err := clientSide.Read(got)
	if err != nil {
		t.Fatalf("reading bridged reply: %v", err)
	}
	if string(got[:n]) != "ok:hello" {
		t.Fatalf("bridge did not splice both ways: got %q, want %q", string(got[:n]), "ok:hello")
	}
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("bridge did not tear down after the client closed")
	}
}

// Dialing a dead SSH addr returns an error (the box's server isn't up) — the
// caller surfaces "agent SSH server down", not a hang.
func TestBridgeToLocalSSH_DeadServerErrors(t *testing.T) {
	client, bridgeSide := net.Pipe()
	defer client.Close()
	err := bridgeToLocalSSH(context.Background(), bridgeSide, "127.0.0.1:1") // nothing listens on :1
	if err == nil {
		t.Fatal("bridging to a dead SSH server must return an error")
	}
}

var _ io.ReadWriteCloser = net.Conn(nil)
