package main

// --- Tunnel Protocol ---
//
// The relay and agent communicate over QUIC streams using JSON messages.
//
// Flow:
//   1. Agent dials relay via QUIC (outbound — works behind any NAT)
//   2. Agent opens stream 0, sends RegisterMsg, reads RegisterResp
//   3. Connection stays alive via QUIC keepalive
//   4. When relay receives an HTTP request for this device:
//      - Relay opens a new QUIC stream on the agent's connection
//      - Writes a TunnelRequest (serialized HTTP request)
//      - Agent proxies to its local HTTP server
//      - Agent writes TunnelResponse back on the same stream
//   5. Relay returns the response to the HTTP client (mobile app)
//
// For hole punching (optional upgrade):
//   - After both peers are connected, relay sends PeerInfo to each
//   - Both attempt direct QUIC to the other's observed public addr
//   - If direct works, traffic bypasses relay

// RegisterMsg is sent by the agent on the first QUIC stream after connecting.
type RegisterMsg struct {
	Type     string `json:"type"`               // "register"
	DeviceID string `json:"deviceId"`           // agent's device ID from config
	Token    string `json:"token"`              // auth token for validation
	Password string `json:"password,omitempty"` // shared relay password
}

// RegisterResp is sent by the relay back to the agent.
type RegisterResp struct {
	Type    string `json:"type"`              // "registered" or "error"
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// TunnelRequest is sent from relay to agent over a QUIC stream.
// It represents an HTTP request from a mobile client.
type TunnelRequest struct {
	ID         string            `json:"id"`                   // unique request ID
	Method     string            `json:"method"`               // HTTP method
	Path       string            `json:"path"`                 // URL path (e.g. /tasks)
	Query      string            `json:"query"`                // URL query string
	Headers    map[string]string `json:"headers"`              // HTTP headers
	Body       []byte            `json:"body"`                 // request body
	TargetPort int               `json:"targetPort,omitempty"` // non-zero = forward to this port instead of agent HTTP
}

// TunnelResponse is sent from agent back to relay over the same QUIC stream.
type TunnelResponse struct {
	ID         string            `json:"id"`         // matches request ID
	StatusCode int               `json:"statusCode"` // HTTP status code
	Headers    map[string]string `json:"headers"`    // response headers
	Body       []byte            `json:"body"`       // response body
}

// PeerInfo is sent by relay to both peers for hole punch coordination.
type PeerInfo struct {
	Type       string `json:"type"`       // "peer_info"
	PeerAddr   string `json:"peerAddr"`   // observed public IP:port of the other peer
	PeerID     string `json:"peerId"`     // device ID of the other peer
	DirectPort int    `json:"directPort"` // port the peer is listening on for direct connections
}

// ExposeRegisterMsg is sent by the agent over a control stream to register a public subdomain.
type ExposeRegisterMsg struct {
	Type      string `json:"type"`      // "expose_register"
	DeviceID  string `json:"deviceId"`
	Subdomain string `json:"subdomain"` // e.g. "myapp" → myapp.yaver.io
	Port      int    `json:"port"`      // local port to forward to (e.g. 3000)
}

// ExposeRegisterResp is the relay's reply.
type ExposeRegisterResp struct {
	Type      string `json:"type"`                // "expose_registered" or "error"
	OK        bool   `json:"ok"`
	PublicURL string `json:"publicUrl,omitempty"` // https://myapp.yaver.io
	Message   string `json:"message,omitempty"`
}

// ExposeUnregisterMsg removes a subdomain binding.
type ExposeUnregisterMsg struct {
	Type      string `json:"type"`      // "expose_unregister"
	DeviceID  string `json:"deviceId"`
	Subdomain string `json:"subdomain"`
}
