package main

// shortener.go — self-hosted URL shortener with click analytics.
// Replaces Bitly / Rebrandly / Dub.co for the solo dev who wants
// yourname.com/s/abc → real URL without the $29/mo subscription.
//
// Public endpoints (no auth — that's the point of a public link):
//
//   GET  /s/:code              302 → long URL, logs a click
//   GET  /s/:code/json         → the long URL as JSON (for SDKs)
//
// Owner endpoints:
//
//   GET  /shortener            list links + click counts
//   POST /shortener            create { code?, url, label? }
//   DELETE /shortener?code=... delete a link
//   GET  /shortener/clicks?code=... → last 500 click rows
//
// Storage: links in ~/.yaver/shortener.json, clicks in
// ~/.yaver/shortener-clicks.jsonl (append-only, rotatable by mv).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ShortLink is one entry in the links table.
type ShortLink struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Clicks    int       `json:"clicks"`
}

// ShortClick is one row in the append-only click log.
type ShortClick struct {
	Code      string    `json:"code"`
	At        time.Time `json:"at"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
	Referer   string    `json:"referer,omitempty"`
}

var (
	shortMu    sync.Mutex
	shortLinks []ShortLink
)

func shortFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shortener.json"), nil
}

func shortClicksFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shortener-clicks.jsonl"), nil
}

func loadShortLinks() []ShortLink {
	shortMu.Lock()
	defer shortMu.Unlock()
	if shortLinks != nil {
		return shortLinks
	}
	p, _ := shortFile()
	data, err := os.ReadFile(p)
	if err != nil {
		shortLinks = []ShortLink{}
		return shortLinks
	}
	_ = json.Unmarshal(data, &shortLinks)
	return shortLinks
}

func saveShortLinks() error {
	p, _ := shortFile()
	data, _ := json.MarshalIndent(shortLinks, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func recordClick(c ShortClick) {
	p, _ := shortClicksFile()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(c)
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleShortRedirect(w http.ResponseWriter, r *http.Request) {
	// Path: /s/:code or /s/:code/json
	code := strings.TrimPrefix(r.URL.Path, "/s/")
	asJSON := false
	if strings.HasSuffix(code, "/json") {
		asJSON = true
		code = strings.TrimSuffix(code, "/json")
	}
	if code == "" {
		jsonError(w, http.StatusNotFound, "no code")
		return
	}
	links := loadShortLinks()
	var target *ShortLink
	for i := range links {
		if links[i].Code == code {
			target = &links[i]
			break
		}
	}
	if target == nil {
		jsonError(w, http.StatusNotFound, "code not found")
		return
	}
	// Count + log (best-effort).
	shortMu.Lock()
	target.Clicks++
	_ = saveShortLinks()
	shortMu.Unlock()
	go recordClick(ShortClick{
		Code:      code,
		At:        time.Now().UTC(),
		IP:        clientIP(r),
		UserAgent: r.Header.Get("User-Agent"),
		Referer:   r.Header.Get("Referer"),
	})
	if asJSON {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"code": code, "url": target.URL, "label": target.Label, "clicks": target.Clicks,
		})
		return
	}
	http.Redirect(w, r, target.URL, http.StatusFound)
}

func (s *HTTPServer) handleShortener(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "links": loadShortLinks()})
	case http.MethodPost:
		var body struct {
			Code  string `json:"code"`
			URL   string `json:"url"`
			Label string `json:"label,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.URL == "" || !strings.HasPrefix(body.URL, "http") {
			jsonError(w, http.StatusBadRequest, "url must be http(s)://...")
			return
		}
		if body.Code == "" {
			body.Code = randomShortCode()
		}
		links := loadShortLinks()
		for _, l := range links {
			if l.Code == body.Code {
				jsonError(w, http.StatusConflict, "code taken")
				return
			}
		}
		link := ShortLink{
			Code:      body.Code,
			URL:       body.URL,
			Label:     body.Label,
			CreatedAt: time.Now().UTC(),
		}
		shortMu.Lock()
		shortLinks = append(links, link)
		_ = saveShortLinks()
		shortMu.Unlock()
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "link": link})
	case http.MethodDelete:
		code := r.URL.Query().Get("code")
		if code == "" {
			jsonError(w, http.StatusBadRequest, "code required")
			return
		}
		shortMu.Lock()
		links := loadShortLinks()
		out := links[:0]
		for _, l := range links {
			if l.Code != code {
				out = append(out, l)
			}
		}
		shortLinks = out
		_ = saveShortLinks()
		shortMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST/DELETE")
	}
}

func (s *HTTPServer) handleShortClicks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	code := r.URL.Query().Get("code")
	p, _ := shortClicksFile()
	data, err := os.ReadFile(p)
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "clicks": []ShortClick{}})
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// Tail last 500 so we don't ship megabytes back to mobile.
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}
	out := make([]ShortClick, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var c ShortClick
		if err := json.Unmarshal([]byte(line), &c); err == nil {
			if code == "" || c.Code == code {
				out = append(out, c)
			}
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "clicks": out})
}

// randomShortCode returns a 6-char URL-safe code. Letters/digits
// avoiding lookalikes (0/O, 1/l) so SMSed codes don't get lost.
func randomShortCode() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	_, _ = randomRead(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return fmt.Sprintf("%s", string(b))
}
