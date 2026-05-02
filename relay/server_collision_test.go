package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// TestRegister_RefusesDuplicateDeviceID is the C-1 regression test.
//
// Before this fix the relay swapped tunnel ownership last-writer-wins,
// letting anyone with the relay password steal another device's
// /d/<id>/... routing. After the fix, the second concurrent register
// for the same DeviceID must be rejected with Type:"error".
func TestRegister_RefusesDuplicateDeviceID(t *testing.T) {
	srv, addr, cleanup := startTestRelayQUIC(t, "test-pw")
	defer cleanup()
	_ = srv

	// First client registers — should succeed.
	conn1, resp1, err := dialAndRegister(t, addr, "device-collision-1", "tok1", "test-pw")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	defer conn1.CloseWithError(0, "test done")
	if !resp1.OK || resp1.Type != "registered" {
		t.Fatalf("first register: expected registered, got %+v", resp1)
	}

	// Second client tries with the same deviceId — should be refused.
	conn2, resp2, err := dialAndRegister(t, addr, "device-collision-1", "tok2", "test-pw")
	if err == nil {
		defer conn2.CloseWithError(0, "test done")
	}
	if resp2.OK {
		t.Fatalf("expected second register to fail, got OK=true response %+v", resp2)
	}
	if resp2.Type != "error" {
		t.Fatalf("expected Type=\"error\", got %q (msg=%q)", resp2.Type, resp2.Message)
	}
	if resp2.Message != "deviceId already registered" {
		t.Fatalf("expected message \"deviceId already registered\", got %q", resp2.Message)
	}

	// Verify that the FIRST tunnel is still the one in the map (not replaced).
	// quic.Connection is an interface so pointer equality across goroutines
	// isn't a reliable identity check; compare ephemeral source ports
	// instead. conn1.LocalAddr() yields "[::]:<port>" (the client UDP
	// socket) and tun.peerAddr is "127.0.0.1:<port>" (server-side observed
	// peer) — same port, different format.
	srv.mu.RLock()
	tun, ok := srv.tunnels["device-collision-1"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatalf("expected first tunnel to still be registered after collision attempt")
	}
	_, conn1Port, _ := net.SplitHostPort(conn1.LocalAddr().String())
	_, tunPort, _ := net.SplitHostPort(tun.peerAddr)
	if conn1Port == "" || tunPort == "" || conn1Port != tunPort {
		t.Fatalf("expected first tunnel's source port (%q) to match conn1's local port (%q); collision swapped tunnel",
			tunPort, conn1Port)
	}
}

// TestRegister_AllowsAfterDisconnect confirms a legitimate reconnect
// path still works once the previous tunnel actually drops.
func TestRegister_AllowsAfterDisconnect(t *testing.T) {
	srv, addr, cleanup := startTestRelayQUIC(t, "test-pw")
	defer cleanup()

	conn1, resp1, err := dialAndRegister(t, addr, "device-reconnect-1", "tok1", "test-pw")
	if err != nil || !resp1.OK {
		t.Fatalf("first register: err=%v resp=%+v", err, resp1)
	}

	// Simulate the agent process dying.
	conn1.CloseWithError(0, "agent crash")

	// The relay's <-conn.Context().Done() handler runs in a goroutine;
	// give it a moment to clean up the tunnel map. Even if it races,
	// the new code path also checks conn.Context().Done() inline.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		_, stillThere := srv.tunnels["device-reconnect-1"]
		srv.mu.RUnlock()
		if !stillThere {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// New registration should succeed.
	conn2, resp2, err := dialAndRegister(t, addr, "device-reconnect-1", "tok2", "test-pw")
	if err != nil {
		t.Fatalf("reconnect register: %v", err)
	}
	defer conn2.CloseWithError(0, "test done")
	if !resp2.OK || resp2.Type != "registered" {
		t.Fatalf("reconnect register expected ok, got %+v", resp2)
	}
}

// startTestRelayQUIC binds the relay's QUIC listener to a random port
// so each test gets an isolated server.
func startTestRelayQUIC(t *testing.T, password string) (*RelayServer, string, func()) {
	t.Helper()
	srv := NewRelayServer(0, 0, password, "", "")

	tlsCfg, err := generateRelayTLS()
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	tr := &quic.Transport{Conn: udpConn}
	listener, err := tr.Listen(tlsCfg, &quic.Config{
		MaxIdleTimeout:  30 * time.Second,
		KeepAlivePeriod: 5 * time.Second,
	})
	if err != nil {
		udpConn.Close()
		t.Fatalf("quic listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			session, err := listener.Accept(ctx)
			if err != nil {
				return
			}
			go srv.handleAgentConnection(ctx, session)
		}
	}()

	addr := udpConn.LocalAddr().String()
	cleanup := func() {
		cancel()
		listener.Close()
		udpConn.Close()
	}
	return srv, addr, cleanup
}

// dialAndRegister opens a QUIC connection, sends RegisterMsg, and
// reads the RegisterResp.
func dialAndRegister(t *testing.T, addr, deviceID, token, password string) (quic.Connection, RegisterResp, error) {
	t.Helper()
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"yaver-relay"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := quic.DialAddr(ctx, addr, tlsCfg, &quic.Config{
		MaxIdleTimeout:  30 * time.Second,
		KeepAlivePeriod: 5 * time.Second,
	})
	if err != nil {
		return nil, RegisterResp{}, err
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(0, "test cleanup")
		return nil, RegisterResp{}, err
	}

	msg := RegisterMsg{
		Type:     "register",
		DeviceID: deviceID,
		Token:    token,
		Password: password,
	}
	data, _ := json.Marshal(msg)
	if _, err := stream.Write(data); err != nil {
		conn.CloseWithError(0, "test cleanup")
		return nil, RegisterResp{}, err
	}
	stream.Close()

	respData, err := io.ReadAll(io.LimitReader(stream, 1<<16))
	if err != nil {
		conn.CloseWithError(0, "test cleanup")
		return nil, RegisterResp{}, err
	}

	var resp RegisterResp
	_ = json.Unmarshal(respData, &resp)

	// Give the relay a moment to register the tunnel in its map
	// before tests query srv.tunnels — handleAgentConnection enters
	// the select-on-Done loop after writing the response.
	time.Sleep(50 * time.Millisecond)
	return conn, resp, nil
}
