package main

// gateway_audit.go — the append-only ACT audit ledger (M-G7).
//
// Every gateway ACT (a write that changes state in one of your credentialed
// services) appends one line to a LOCAL JSON-lines ledger at
// ~/.yaver/gateway-audit.jsonl. The ledger is the record of "what Yaver did as
// me, when, and how it ended" — the reversibility/forensics backbone of the ACT
// consent model (docs §16).
//
// PRIVACY (CLAUDE.md privacy contract): this ledger is LOCAL ONLY and MUST NEVER
// be synced to Convex. Convex may carry an action+target+outcome+timestamp
// SUMMARY at most; the detailed local ledger below intentionally records the
// endpoint, risk tier and outcome (useful to the user on their own box) but
// NEVER a token, a client secret, or a raw request body value. The execution
// path redacts those before calling appendGatewayAudit.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GatewayAuditEntry is one ledger line. Times are RFC3339 UTC.
type GatewayAuditEntry struct {
	ID         string `json:"id"`
	Time       string `json:"time"`
	Connector  string `json:"connector"`
	Capability string `json:"capability"`
	Verb       string `json:"verb"`
	Risk       string `json:"risk"`
	Engine     string `json:"engine"`
	// Target is a redacted human-readable description of what was acted on
	// (e.g. "POST https://api.example/orders" or "redroid:com.carrier.app").
	// NEVER a body value or a secret.
	Target string `json:"target,omitempty"`
	// Decision records the Policy Guard verdict (allow/warn/block).
	Decision string `json:"decision,omitempty"`
	// Confirmed records how the second key was obtained: "voice", "phone_tap",
	// "explicit", or "" (none required / not reached).
	Confirmed string `json:"confirmed,omitempty"`
	// Outcome is the terminal state: "executed" | "declined" | "blocked_policy" |
	// "blocked_rate" | "blocked_remote" | "error".
	Outcome    string `json:"outcome"`
	StatusCode int    `json:"statusCode,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// Idempotency is the key sent with the request (safe to record — it is a
	// hash, not a secret) so a duplicate replay is recognizable.
	Idempotency string `json:"idempotency,omitempty"`
}

// gatewayAuditPathFn resolves the ledger path. Overridable in tests so the suite
// never touches the real ~/.yaver dir.
var gatewayAuditPathFn = func() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gateway-audit.jsonl"), nil
}

// gatewayAuditMu serializes appends so concurrent acts don't interleave lines.
var gatewayAuditMu sync.Mutex

// appendGatewayAudit writes one entry to the ledger. A failure to write is
// returned (the caller logs it) but never blocks the act outcome it records —
// an act that already happened must still be reported to the user.
func appendGatewayAudit(e GatewayAuditEntry) error {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if e.ID == "" {
		e.ID = fmt.Sprintf("act_%d", time.Now().UnixNano())
	}
	path, err := gatewayAuditPathFn()
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("gateway audit marshal: %w", err)
	}
	gatewayAuditMu.Lock()
	defer gatewayAuditMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("gateway audit mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("gateway audit open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("gateway audit write: %w", err)
	}
	return nil
}

// listGatewayAudit returns the most recent entries (newest first), up to limit
// (<=0 ⇒ all). Used by the gateway_audit MCP tool / a "what did you do" view.
func listGatewayAudit(limit int) ([]GatewayAuditEntry, error) {
	path, err := gatewayAuditPathFn()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no acts yet
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := make([]GatewayAuditEntry, 0, len(lines))
	// Walk newest-first.
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		var e GatewayAuditEntry
		if json.Unmarshal([]byte(ln), &e) != nil {
			continue // skip a corrupt line rather than fail the whole list
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// gatewayActCountSince counts executed acts for a connector at/after the given
// time. Used by the velocity cap (max acts/hour) so a runaway loop can't drain
// an account — reuses the ledger as the source of truth rather than a separate
// in-memory counter that resets on restart.
func gatewayActCountSince(connector string, since time.Time) (int, error) {
	entries, err := listGatewayAudit(0)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Connector != connector || e.Outcome != "executed" {
			continue
		}
		t, perr := time.Parse(time.RFC3339, e.Time)
		if perr != nil {
			continue
		}
		if !t.Before(since) {
			n++
		}
	}
	return n, nil
}
