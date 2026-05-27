package main

// imap_summary.go — GET /imap/inbox
//
// Glass HUD surface: returns a compact list of {from, subject, ts}
// from the configured email provider, sized for HUD rendering (≤ 4
// items, ≤ 60 chars per field). Reuses the existing EmailManager
// (Office 365 / Gmail providers) so creds + sync logic stay in one
// place. Cache is the manager's own local SQLite — no extra layer.
//
// HUD wall flow:
//   miniapp polls /imap/inbox every 30s OR
//   the agent fans new arrivals out via /glass/hud POST email_subjects
//
// Read-only. Composing emails goes through the spatial browser quad
// (Gmail/Fastmail tab) — not the HUD.

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type imapInboxItem struct {
	From    string `json:"from"`
	Subject string `json:"subject"`
	TS      string `json:"ts,omitempty"`
}

func (s *HTTPServer) handleIMAPInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if s.emailMgr == nil {
		jsonReply(w, http.StatusOK, map[string]any{
			"folder": "inbox",
			"items":  []imapInboxItem{},
			"note":   "email manager not configured — set up via /email/setup or auth_oauth_setup",
		})
		return
	}
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" {
		folder = "inbox"
	}
	limit := 4
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			limit = n
		}
	}
	summaries, err := s.emailMgr.ListInbox(folder, "", limit)
	if err != nil {
		jsonError(w, http.StatusBadGateway, "list inbox: "+err.Error())
		return
	}
	items := make([]imapInboxItem, 0, len(summaries))
	for _, e := range summaries {
		from := strings.TrimSpace(e.SenderName)
		if from == "" {
			from = strings.TrimSpace(e.SenderEmail)
		}
		items = append(items, imapInboxItem{
			From:    from,
			Subject: e.Subject,
			TS:      e.ReceivedAt,
		})
	}
	provider := ""
	if s.emailMgr.cfg != nil {
		provider = s.emailMgr.cfg.Provider
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"folder":   folder,
		"items":    items,
		"asOf":     time.Now().UTC().Format(time.RFC3339),
		"provider": provider,
	})
}
