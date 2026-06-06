package main

// screenlog_policy.go — consent, permission, and audit for the screen
// black box. Screen recording is sensitive enough that "you're signed in
// as the same account" isn't sufficient justification to silently start
// it on someone's machine; this layer gives the RECORDED machine's owner
// an explicit, local kill-switch + a remote-control gate + an audit trail.
//
// How it composes with the rest of Yaver's permission spine:
//
//   - Same yaver account (your own devices): the owner token clears
//     s.auth and reaches /screenlog/* by default. This policy adds the
//     extra gate — Enabled (master kill-switch) and AllowRemoteControl
//     (may a NON-loopback caller start/stop). Default: enabled, remote
//     allowed — your machines, your call — but every remote start is
//     audited and (optionally) notified.
//
//   - Delegated / guest (someone you invited): gated at the existing
//     guest-scope middleware (guest_scope.go). /screenlog/* is on the
//     FULL-scope allow-list only — feedback-only and read-only support
//     guests physically cannot reach it.
//
//   - Yaver mesh peer (a different account you peered with): the ACL
//     layer (acl.go CallPeerTool) delivers the tools/call; this policy
//     requires the peer id to be in AllowedPeers, so a mesh peering does
//     NOT implicitly grant screen access — it must be granted per peer.
//
// Stored at ~/.yaver/screenlog/policy.json. LOCAL ONLY (it lists peer
// ids); never synced to Convex.

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ScreenlogPolicy struct {
	Enabled            bool     `json:"enabled"`            // master switch; false = refuse all starts
	AllowRemoteControl bool     `json:"allowRemoteControl"` // may a non-loopback caller start/stop
	RequireMeshGrant   bool     `json:"requireMeshGrant"`   // mesh peers must be in AllowedPeers
	AllowedPeers       []string `json:"allowedPeers,omitempty"`
	NotifyOnStart      bool     `json:"notifyOnStart"`
	// AllowInputCapture gates the keystroke/mouse companion stream — a
	// separate, stronger consent than screen capture (keylogging can
	// catch passwords). OFF by default; ingest is refused unless true.
	AllowInputCapture bool  `json:"allowInputCapture"`
	UpdatedAt         int64 `json:"updatedAt"`
}

func defaultScreenlogPolicy() ScreenlogPolicy {
	return ScreenlogPolicy{
		Enabled:            true,
		AllowRemoteControl: true,
		RequireMeshGrant:   true,
		NotifyOnStart:      true,
	}
}

func screenlogPolicyPath() (string, error) {
	base, err := screenlogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "policy.json"), nil
}

func loadScreenlogPolicy() ScreenlogPolicy {
	p, err := screenlogPolicyPath()
	if err != nil {
		return defaultScreenlogPolicy()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return defaultScreenlogPolicy()
	}
	pol := defaultScreenlogPolicy()
	if err := json.Unmarshal(data, &pol); err != nil {
		return defaultScreenlogPolicy()
	}
	return pol
}

func saveScreenlogPolicy(pol ScreenlogPolicy) error {
	p, err := screenlogPolicyPath()
	if err != nil {
		return err
	}
	pol.UpdatedAt = time.Now().UnixMilli()
	data, _ := json.MarshalIndent(pol, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// screenlogCaller describes who is asking to control screenlog.
type screenlogCaller struct {
	Remote bool   // non-loopback request
	Mesh   bool   // arrived via a mesh peer tool-call
	PeerID string // mesh/guest peer identity, if any
}

// screenlogEnforce decides whether a control request is allowed. Returns
// (allowed, reason-if-denied). Pure — unit-tested.
func screenlogEnforce(pol ScreenlogPolicy, c screenlogCaller) (bool, string) {
	if !pol.Enabled {
		return false, "screenlog is disabled on this machine (owner ran `yaver screenlog disable`)"
	}
	if c.Remote && !pol.AllowRemoteControl {
		return false, "remote screenlog control is disabled on this machine (owner must run `yaver screenlog allow-remote`)"
	}
	if c.Mesh && pol.RequireMeshGrant && !peerAllowed(pol, c.PeerID) {
		return false, "this mesh peer is not granted screen access (owner must run `yaver screenlog allow-peer " + c.PeerID + "`)"
	}
	return true, ""
}

func peerAllowed(pol ScreenlogPolicy, peerID string) bool {
	if peerID == "" {
		return false
	}
	for _, p := range pol.AllowedPeers {
		if p == peerID {
			return true
		}
	}
	return false
}

// isLoopbackAddr reports whether an HTTP RemoteAddr is local. Used to
// classify a control request as local vs remote.
func isLoopbackAddr(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// --- audit trail (local) ---------------------------------------------------

type screenlogAuditEntry struct {
	At      int64  `json:"at"`
	Action  string `json:"action"` // "start" | "stop" | "policy" | "deny"
	Session string `json:"session,omitempty"`
	Remote  bool   `json:"remote"`
	Mesh    bool   `json:"mesh,omitempty"`
	PeerID  string `json:"peerId,omitempty"`
	Note    string `json:"note,omitempty"`
}

func appendScreenlogAudit(e screenlogAuditEntry) {
	base, err := screenlogDir()
	if err != nil {
		return
	}
	e.At = time.Now().UnixMilli()
	f, err := os.OpenFile(filepath.Join(base, "audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(e)
	f.Write(append(data, '\n'))
}

func readScreenlogAudit(limit int) []screenlogAuditEntry {
	base, err := screenlogDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(base, "audit.jsonl"))
	if err != nil {
		return nil
	}
	var out []screenlogAuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e screenlogAuditEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
