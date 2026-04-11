package main

// mail_learning.go — classifier feedback loop. Every time the
// dev taps "this is marketing" or "this is personal" on a mail
// item, the sender gets added to a local allow/deny list that
// the classifier consults first. Over a few weeks the inbox
// gets dramatically more accurate — and it stays private: the
// lists never leave the Mac mini.
//
// Two tiny JSON files:
//
//   ~/.yaver/mail-allow.json  — senders always classified personal
//   ~/.yaver/mail-deny.json   — senders always classified bulk
//
// classifyMessage (mail_fetch.go) checks these before running
// the heuristic. Allow → score = 100 → personal. Deny → score = 0
// → bulk.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	learnMu        sync.RWMutex
	learnAllow     map[string]bool
	learnDeny      map[string]bool
	learnLoaded    bool
)

func mailAllowFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mail-allow.json"), nil
}

func mailDenyFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mail-deny.json"), nil
}

// loadMailLearning populates the allow/deny lists from disk.
// Safe to call from any goroutine; subsequent callers read from
// the in-memory maps directly.
func loadMailLearning() {
	learnMu.Lock()
	defer learnMu.Unlock()
	if learnLoaded {
		return
	}
	learnAllow = map[string]bool{}
	learnDeny = map[string]bool{}
	if p, err := mailAllowFile(); err == nil {
		if data, err := os.ReadFile(p); err == nil {
			var list []string
			if json.Unmarshal(data, &list) == nil {
				for _, v := range list {
					learnAllow[strings.ToLower(v)] = true
				}
			}
		}
	}
	if p, err := mailDenyFile(); err == nil {
		if data, err := os.ReadFile(p); err == nil {
			var list []string
			if json.Unmarshal(data, &list) == nil {
				for _, v := range list {
					learnDeny[strings.ToLower(v)] = true
				}
			}
		}
	}
	learnLoaded = true
}

func saveMailLearning() {
	learnMu.RLock()
	defer learnMu.RUnlock()
	allow := []string{}
	for k := range learnAllow {
		allow = append(allow, k)
	}
	deny := []string{}
	for k := range learnDeny {
		deny = append(deny, k)
	}
	if p, err := mailAllowFile(); err == nil {
		data, _ := json.MarshalIndent(allow, "", "  ")
		_ = os.WriteFile(p, data, 0o600)
	}
	if p, err := mailDenyFile(); err == nil {
		data, _ := json.MarshalIndent(deny, "", "  ")
		_ = os.WriteFile(p, data, 0o600)
	}
}

// mailSenderKey is the canonical key for a sender (domain when
// present, falls back to full address). Domain-level keys mean a
// single "mark bulk" on one newsletter covers every sender at
// the same domain — which is usually what the dev wants.
func mailSenderKey(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if at := strings.LastIndex(addr, "@"); at > 0 {
		return addr[at+1:]
	}
	return addr
}

// isMailAllowed / isMailDenied are the classifier hooks.
func isMailAllowed(addr string) bool {
	loadMailLearning()
	learnMu.RLock()
	defer learnMu.RUnlock()
	return learnAllow[mailSenderKey(addr)] || learnAllow[strings.ToLower(addr)]
}

func isMailDenied(addr string) bool {
	loadMailLearning()
	learnMu.RLock()
	defer learnMu.RUnlock()
	return learnDeny[mailSenderKey(addr)] || learnDeny[strings.ToLower(addr)]
}

// markMailSender records a user verdict. `verdict` must be one
// of "personal" or "bulk" / "marketing". Calling with "reset"
// clears both lists for that sender.
func markMailSender(addr, verdict string) {
	loadMailLearning()
	learnMu.Lock()
	defer learnMu.Unlock()
	key := mailSenderKey(addr)
	lower := strings.ToLower(addr)
	delete(learnAllow, key)
	delete(learnAllow, lower)
	delete(learnDeny, key)
	delete(learnDeny, lower)
	switch verdict {
	case "personal":
		learnAllow[key] = true
	case "bulk", "marketing":
		learnDeny[key] = true
	}
	saveMailLearning()
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleMailMark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		From    string `json:"from"`
		Verdict string `json:"verdict"` // "personal" | "bulk" | "marketing" | "reset"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.From == "" {
		jsonError(w, http.StatusBadRequest, "from required")
		return
	}
	markMailSender(body.From, body.Verdict)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleMailLearning(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	loadMailLearning()
	learnMu.RLock()
	defer learnMu.RUnlock()
	allow := []string{}
	for k := range learnAllow {
		allow = append(allow, k)
	}
	deny := []string{}
	for k := range learnDeny {
		deny = append(deny, k)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "allow": allow, "deny": deny})
}
