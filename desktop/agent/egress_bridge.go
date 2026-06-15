package main

// egress_bridge.go — the A side of peer-egress: a LOCAL forward proxy that the
// browser collector points at (browser_open proxy_url=http://127.0.0.1:PORT),
// which tunnels each connection to a peer's auth-gated /egress/proxy so the
// source sees the PEER's egress IP. Together with egress_proxy.go (the B side)
// this makes "collect from my US box's IP while I sit on my EU box" work.
//
// The bridge speaks just enough HTTP-proxy protocol for a browser: it handles
// CONNECT (used for every https:// target — i.e. effectively all real
// collection). Plain-http absolute-URI requests are refused with a clear
// message; point egress at https endpoints. The bridge holds the peer's auth
// token in memory only and never logs it.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// egressBridge is a running local CONNECT proxy bound to loopback that forwards
// to a single peer's /egress/proxy.
type egressBridge struct {
	id      string
	ln      net.Listener
	baseURL string            // peer agent base URL, e.g. http://10.0.0.2:18080
	token   string            // peer auth token (in-memory only)
	headers map[string]string // transport headers (relay password, CF Access), in-memory only
	stop    chan struct{}
	once    sync.Once
}

func (b *egressBridge) addr() string { return b.ln.Addr().String() }
func (b *egressBridge) proxyURL() string {
	return "http://" + b.ln.Addr().String()
}

func (b *egressBridge) close() {
	b.once.Do(func() {
		close(b.stop)
		_ = b.ln.Close()
	})
}

// startEgressBridge opens a loopback listener and serves the CONNECT proxy. The
// browser is then opened with proxy_url = bridge.proxyURL(). baseURL/token point
// at the peer that will provide the egress.
func startEgressBridge(id, baseURL, token string, headers map[string]string) (*egressBridge, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("egress bridge: peer baseURL required")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("egress bridge: listen: %w", err)
	}
	b := &egressBridge{
		id:      id,
		ln:      ln,
		baseURL: baseURL,
		token:   token,
		headers: headers,
		stop:    make(chan struct{}),
	}
	go b.serve()
	return b, nil
}

func (b *egressBridge) serve() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			select {
			case <-b.stop:
				return
			default:
				continue
			}
		}
		go b.handle(conn)
	}
}

func (b *egressBridge) handle(client net.Conn) {
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		_ = client.Close()
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = client.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\nConnection: close\r\n\r\nonly CONNECT (https targets) supported by the egress bridge\n"))
		_ = client.Close()
		return
	}

	// For CONNECT, req.Host carries "host:port".
	target := req.Host
	upstream, err := dialEgressUpstream(b.baseURL, b.token, b.headers, target)
	if err != nil {
		_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n" + err.Error() + "\n"))
		_ = client.Close()
		return
	}

	// Tell the browser the tunnel is established.
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = upstream.Close()
		_ = client.Close()
		return
	}

	// Any bytes the browser already pipelined after CONNECT (the TLS
	// ClientHello) sit in br's buffer — forward them before piping.
	if n := br.Buffered(); n > 0 {
		if pre, perr := br.Peek(n); perr == nil {
			if _, werr := upstream.Write(pre); werr != nil {
				_ = upstream.Close()
				_ = client.Close()
				return
			}
		}
	}

	pipeBoth(client, upstream)
}

// dialEgressUpstream opens an authenticated HTTP-Upgrade tunnel to a peer's
// /egress/proxy for the given target and returns the raw tunneled conn. Any
// bytes the peer buffered past the 101 headers are preserved via prefixConn.
func dialEgressUpstream(baseURL, token string, headers map[string]string, target string) (net.Conn, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("bad peer baseURL %q", baseURL)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 8*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial peer: %w", err)
	}

	var hdr strings.Builder
	hdr.WriteString("GET /egress/proxy?target=" + url.QueryEscape(target) + " HTTP/1.1\r\n")
	hdr.WriteString("Host: " + u.Host + "\r\n")
	hdr.WriteString("Authorization: Bearer " + token + "\r\n")
	for k, v := range headers {
		// Skip anything that would collide with the lines we set explicitly.
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Authorization") ||
			strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Upgrade") {
			continue
		}
		hdr.WriteString(k + ": " + v + "\r\n")
	}
	hdr.WriteString("Connection: Upgrade\r\nUpgrade: yaver-egress\r\n\r\n")
	if _, err := conn.Write([]byte(hdr.String())); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write peer request: %w", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read peer status: %w", err)
	}
	if !strings.Contains(statusLine, " 101") {
		// Drain a little of the body so the owner sees WHY (disabled, refused
		// target, etc.) without leaking much.
		var body strings.Builder
		for i := 0; i < 16; i++ {
			line, e := br.ReadString('\n')
			body.WriteString(line)
			if e != nil || line == "\r\n" || line == "\n" {
				break
			}
		}
		_ = conn.Close()
		return nil, fmt.Errorf("peer egress refused: %s", strings.TrimSpace(statusLine))
	}
	// Consume the rest of the response headers up to the blank line.
	for {
		line, e := br.ReadString('\n')
		if e != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("read peer headers: %w", e)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	if n := br.Buffered(); n > 0 {
		pre, _ := br.Peek(n)
		buf := make([]byte, n)
		copy(buf, pre)
		return &prefixConn{Conn: conn, prefix: buf}, nil
	}
	return conn, nil
}

// prefixConn replays a small buffer of already-read bytes before continuing to
// read from the underlying conn. Needed because the HTTP header reader may
// over-read into the tunneled stream.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (p *prefixConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// --- bridge registry + ops verbs (A side) -----------------------------------

var (
	egressBridgeMu  sync.Mutex
	egressBridges   = map[string]*egressBridge{}
	egressBridgeSeq int
)

func registerEgressBridge(b *egressBridge) {
	egressBridgeMu.Lock()
	egressBridges[b.id] = b
	egressBridgeMu.Unlock()
}

func stopEgressBridge(id string) bool {
	egressBridgeMu.Lock()
	b, ok := egressBridges[id]
	if ok {
		delete(egressBridges, id)
	}
	egressBridgeMu.Unlock()
	if ok {
		b.close()
	}
	return ok
}

func nextEgressBridgeID() string {
	egressBridgeMu.Lock()
	egressBridgeSeq++
	id := fmt.Sprintf("egress-bridge-%d", egressBridgeSeq)
	egressBridgeMu.Unlock()
	return id
}

func listEgressBridges() []map[string]interface{} {
	egressBridgeMu.Lock()
	defer egressBridgeMu.Unlock()
	out := make([]map[string]interface{}, 0, len(egressBridges))
	for _, b := range egressBridges {
		out = append(out, map[string]interface{}{
			"bridge_id":  b.id,
			"proxy_url":  b.proxyURL(),
			"peer_base":  b.baseURL,
			"local_addr": b.addr(),
		})
	}
	return out
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "egress_via_peer_start",
		Description: "Open a LOCAL proxy that routes a browser collector's traffic out through " +
			"the named peer device's egress, so the source sees that peer's IP/geo (multi-vantage " +
			"collection). Returns a proxy_url to pass to browser_open. The peer must have egress lending " +
			"enabled (egress_proxy_set enabled=true) and be the SAME user's device. This lends your own " +
			"egress between your own machines — not a tool to defeat a source's geo/IP block.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "Peer device id or alias whose egress to use."},
		}, "device"),
		Handler:    egressViaPeerStartHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "egress_via_peer_stop",
		Description: "Stop a local peer-egress bridge started by egress_via_peer_start and release its listener.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"bridge_id": map[string]interface{}{"type": "string", "description": "Bridge id returned by egress_via_peer_start."},
		}, "bridge_id"),
		Handler:    egressViaPeerStopHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "egress_via_peer_list",
		Description: "List active local peer-egress bridges (their proxy_url and peer).",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     egressViaPeerListHandler,
		AllowGuest:  false,
	})
}

func egressViaPeerStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Device string `json:"device"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(args.Device) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "device required"}
	}

	candidates, token, err := resolveRemoteAgentCandidates(args.Device)
	if err != nil || len(candidates) == 0 {
		msg := "could not resolve a reachable agent for that device"
		if err != nil {
			msg = err.Error()
		}
		return OpsResult{OK: false, Code: "not_found", Error: msg}
	}
	cand := candidates[0]

	id := nextEgressBridgeID()
	bridge, err := startEgressBridge(id, cand.BaseURL, token, cand.Headers)
	if err != nil {
		return OpsResult{OK: false, Code: "internal", Error: err.Error()}
	}
	registerEgressBridge(bridge)

	out := map[string]interface{}{
		"bridge_id":   id,
		"proxy_url":   bridge.proxyURL(),
		"peer_device": cand.DeviceID,
		"peer_label":  cand.Label,
		"hint":        "pass proxy_url to browser_open; the source will see the peer's egress IP",
	}
	// Best-effort: report the egress the source will actually see.
	if peerEgress := peerEgressBestEffort(c.Ctx, args.Device); peerEgress != nil {
		out["peer_egress"] = peerEgress
	}
	return OpsResult{OK: true, Initial: out}
}

func egressViaPeerStopHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		BridgeID string `json:"bridge_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if !stopEgressBridge(strings.TrimSpace(args.BridgeID)) {
		return OpsResult{OK: false, Code: "not_found", Error: "no such bridge"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"stopped": args.BridgeID}}
}

func egressViaPeerListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	return OpsResult{OK: true, Initial: map[string]interface{}{"bridges": listEgressBridges()}}
}

// peerEgressBestEffort asks the peer for its runtime_egress so the caller can
// show "the source will see IP X". Returns nil on any failure — purely
// informational, never blocks bridge creation.
func peerEgressBestEffort(ctx context.Context, device string) interface{} {
	if ctx == nil {
		ctx = context.Background()
	}
	body, _ := json.Marshal(OpsRequest{Machine: "local", Verb: "runtime_egress"})
	status, resp, err := proxyToDevice(ctx, "egress_via_peer_start", device, "POST", "/ops", body)
	if err != nil || status != 200 {
		return nil
	}
	var parsed struct {
		Initial struct {
			Egress json.RawMessage `json:"egress"`
		} `json:"initial"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil || len(parsed.Initial.Egress) == 0 {
		return nil
	}
	var egress map[string]interface{}
	if err := json.Unmarshal(parsed.Initial.Egress, &egress); err != nil {
		return nil
	}
	return egress
}
