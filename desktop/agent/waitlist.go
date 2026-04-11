package main

// waitlist.go — public waitlist with referral codes + a
// "who brought who" leaderboard. Replaces Prefinery / Earlybird
// / Viral Loops for solo devs running a pre-launch landing page.
//
// Flow:
//
//   1. Visitor hits yourname.com/waitlist-signup?ref=ABCD
//   2. Client POSTs { email, ref } to /waitlist/join
//   3. Agent assigns them a slot number + a fresh referral code
//      and stores them in waitlist.json. The referrer's count
//      goes up. An entry that comes in without a ref gets its
//      own code but no parent.
//   4. GET /waitlist/leaderboard returns the top referrers
//      (public — this is typically shown on the landing page).
//   5. GET /waitlist (owner only) returns the full list so the
//      dev can export / remove / blast the waitlist via the
//      existing newsletter broadcast.
//
// Nothing in this file touches Convex. The waitlist data lives
// entirely on the agent's local disk.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// WaitlistEntry is one row.
type WaitlistEntry struct {
	Slot       int       `json:"slot"`
	Email      string    `json:"email"`
	Name       string    `json:"name,omitempty"`
	Code       string    `json:"code"`           // their own referral code
	Referrer   string    `json:"referrer,omitempty"` // code of whoever brought them
	JoinedAt   time.Time `json:"joinedAt"`
	Invited    int       `json:"invited"`        // count of signups they referred
	Source     string    `json:"source,omitempty"`
}

var (
	waitlistMu    sync.Mutex
	waitlistCache []WaitlistEntry
)

func waitlistFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "waitlist.json"), nil
}

func loadWaitlist() []WaitlistEntry {
	waitlistMu.Lock()
	defer waitlistMu.Unlock()
	if waitlistCache != nil {
		return waitlistCache
	}
	p, _ := waitlistFile()
	data, err := os.ReadFile(p)
	if err != nil {
		waitlistCache = []WaitlistEntry{}
		return waitlistCache
	}
	_ = json.Unmarshal(data, &waitlistCache)
	return waitlistCache
}

func saveWaitlist() error {
	p, _ := waitlistFile()
	data, _ := json.MarshalIndent(waitlistCache, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func findWaitlistByCode(code string) *WaitlistEntry {
	list := loadWaitlist()
	for i := range list {
		if list[i].Code == code {
			return &waitlistCache[i]
		}
	}
	return nil
}

// --- HTTP ------------------------------------------------------------------

// handleWaitlistJoin is the public signup endpoint. Accepts
// JSON or form-encoded bodies so landing-page <form action=...>
// tags work without JS.
func (s *HTTPServer) handleWaitlistJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Email  string `json:"email"`
		Name   string `json:"name,omitempty"`
		Ref    string `json:"ref,omitempty"`
		Source string `json:"source,omitempty"`
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&body)
	} else {
		_ = r.ParseForm()
		body.Email = r.PostForm.Get("email")
		body.Name = r.PostForm.Get("name")
		body.Ref = r.PostForm.Get("ref")
		body.Source = r.PostForm.Get("source")
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		jsonError(w, http.StatusBadRequest, "email required")
		return
	}

	waitlistMu.Lock()
	list := loadWaitlist()
	// Dedup — if the email already exists, just return its slot.
	for i := range list {
		if list[i].Email == email {
			entry := list[i]
			waitlistMu.Unlock()
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok": true, "entry": entry, "existing": true,
			})
			return
		}
	}
	entry := WaitlistEntry{
		Slot:     len(list) + 1,
		Email:    email,
		Name:     body.Name,
		Code:     randomShortCode(),
		Referrer: body.Ref,
		JoinedAt: time.Now().UTC(),
		Source:   body.Source,
	}
	list = append(list, entry)
	// Credit the referrer if present.
	if body.Ref != "" {
		for i := range list {
			if list[i].Code == body.Ref {
				list[i].Invited++
				break
			}
		}
	}
	waitlistCache = list
	_ = saveWaitlist()
	waitlistMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"entry":    entry,
		"shareUrl": fmt.Sprintf("?ref=%s", entry.Code),
	})
}

// handleWaitlistLeaderboard is public — used by landing pages
// to show "top inviters".
func (s *HTTPServer) handleWaitlistLeaderboard(w http.ResponseWriter, r *http.Request) {
	list := loadWaitlist()
	top := make([]WaitlistEntry, 0, 10)
	for _, e := range list {
		if e.Invited > 0 {
			top = append(top, e)
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Invited > top[j].Invited })
	if len(top) > 10 {
		top = top[:10]
	}
	// Strip email so the public leaderboard doesn't leak PII.
	redacted := make([]map[string]interface{}, 0, len(top))
	for _, e := range top {
		redacted = append(redacted, map[string]interface{}{
			"slot":    e.Slot,
			"code":    e.Code,
			"name":    e.Name,
			"invited": e.Invited,
		})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"total":      len(list),
		"leaderboard": redacted,
	})
}

// handleWaitlist is the owner-only management endpoint.
func (s *HTTPServer) handleWaitlist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list := loadWaitlist()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"entries": list,
			"total":   len(list),
		})
	case http.MethodDelete:
		email := r.URL.Query().Get("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email required")
			return
		}
		waitlistMu.Lock()
		list := loadWaitlist()
		out := list[:0]
		for _, e := range list {
			if e.Email != email {
				out = append(out, e)
			}
		}
		waitlistCache = out
		_ = saveWaitlist()
		waitlistMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/DELETE")
	}
}
