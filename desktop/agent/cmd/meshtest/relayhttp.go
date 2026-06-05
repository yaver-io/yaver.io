package main

// relayhttp.go — meshtest "relay-http" mode: a minimal agent that registers with
// the relay and answers HTTP-over-QUIC proxied requests. Used to prove the core
// relay product path end-to-end: external HTTP client -> relay /d/<id>/... ->
// QUIC tunnel -> this agent -> TunnelResponse -> back to the client. Mirrors the
// real agent's relayHandleProxiedRequest (main.go) wire format: the relay opens
// a stream per request, writes a TunnelRequest JSON, reads a TunnelResponse JSON.
//
//   meshtest relay-http <deviceID> <relayAddr> <password>

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/quic-go/quic-go"
)

type tunnelRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}
type tunnelResponse struct {
	ID         string            `json:"id"`
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

func runRelayHTTPMode(args []string) {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "relay-http needs: <deviceID> <relayAddr> <password>")
		os.Exit(2)
	}
	deviceID, relayAddr, password := args[0], args[1], args[2]

	ctx := context.Background()
	conn, err := quic.DialAddr(ctx, relayAddr,
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"yaver-relay"}},
		&quic.Config{MaxIdleTimeout: 120 * time.Second, KeepAlivePeriod: 20 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial relay:", err)
		os.Exit(1)
	}
	reg, _ := conn.OpenStreamSync(ctx)
	data, _ := json.Marshal(relayRegisterMsg{Type: "register", DeviceID: deviceID, Token: "meshtest-token", Password: password})
	reg.Write(data)
	reg.Close()
	respData, _ := io.ReadAll(io.LimitReader(reg, 1<<16))
	var rr relayRegisterResp
	if err := json.Unmarshal(respData, &rr); err != nil || !rr.OK {
		fmt.Fprintln(os.Stderr, "register rejected:", rr.Message)
		os.Exit(1)
	}
	fmt.Printf("[%s] registered with relay %s — serving proxied HTTP\n", deviceID, relayAddr)

	// Answer each proxied request the relay forwards.
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go handleProxiedRequest(stream, deviceID)
	}
}

func handleProxiedRequest(stream quic.Stream, deviceID string) {
	defer stream.Close()
	var req tunnelRequest
	if err := json.NewDecoder(io.LimitReader(stream, 8<<20)).Decode(&req); err != nil {
		return
	}
	// Minimal router: /health and an echo of the path so the test can assert
	// the request actually traversed relay -> this agent.
	body := fmt.Sprintf(`{"ok":true,"servedBy":%q,"path":%q}`, deviceID, req.Path)
	resp := tunnelResponse{
		ID:         req.ID,
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       []byte(body),
	}
	out, _ := json.Marshal(resp)
	stream.Write(out)
}
