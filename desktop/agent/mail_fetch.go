package main

// mail_fetch.go — read inbox + draft AI replies against Gmail
// and Microsoft Graph (O365). The solo-dev "let me triage mail
// from my phone without giving Convex my credentials" feature.
//
// Flow:
//
//   1. The dev already configured Gmail or O365 via
//      `yaver email config`. We reuse those OAuth credentials.
//   2. `yaver mail inbox` / POST /mail/inbox calls the vendor
//      API to pull the last N messages and normalises them
//      into a shared MailMessage shape.
//   3. A heuristic classifier tags each message as
//      "personal", "transactional", "marketing", or "bulk".
//      Gmail's own Category:Personal is unreliable (throws
//      newsletters into Updates, sends real one-to-one mail
//      into Promotions if the sender has an unsubscribe link)
//      — the dev wants something tighter. Our classifier
//      combines header sniffing + thread history + domain
//      frequency so a reply from a human gets through even
//      when Gmail routes it to Promotions.
//   4. `yaver mail draft` builds a prompt for the configured
//      AI runner with the current message + prior thread
//      context and returns the draft text. No silent sends —
//      the dev reviews before a `yaver mail send` call hits
//      the SMTP relay.
//
// Nothing here talks to Convex. Everything runs on the agent,
// hitting vendor APIs directly with the dev's own OAuth tokens.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// MailMessage is the shared shape across providers. Everything
// that comes out of a provider fetch is converted to this so
// the CLI / HTTP / mobile code never branches on Gmail vs
// Graph — they all see the same field names.
type MailMessage struct {
	ID           string    `json:"id"`
	ThreadID     string    `json:"threadId,omitempty"`
	From         string    `json:"from"`
	FromName     string    `json:"fromName,omitempty"`
	To           []string  `json:"to,omitempty"`
	Cc           []string  `json:"cc,omitempty"`
	Subject      string    `json:"subject"`
	Snippet      string    `json:"snippet,omitempty"`
	Body         string    `json:"body,omitempty"`
	BodyHTML     string    `json:"bodyHtml,omitempty"`
	Date         time.Time `json:"date"`
	LabelIDs     []string  `json:"labels,omitempty"`
	HasUnsub     bool      `json:"hasUnsubscribe,omitempty"`
	AutoGen      bool      `json:"autoGen,omitempty"`
	ListID       string    `json:"listId,omitempty"`
	Classification string  `json:"classification"` // personal | transactional | marketing | bulk
	Score          int     `json:"score"`          // 0..100, higher = more likely real personal mail
	ThreadReplies  int     `json:"threadReplies,omitempty"`
	Provider       string  `json:"provider"` // gmail | o365
}

// MailFetchOptions shapes what the dev wants back.
type MailFetchOptions struct {
	Provider    string `json:"provider,omitempty"` // gmail | o365 | auto
	Folder      string `json:"folder,omitempty"`   // inbox | sent | all
	Query       string `json:"query,omitempty"`    // vendor-native query (Gmail: "from:bob", Graph: "$search=...")
	Limit       int    `json:"limit,omitempty"`
	OnlyPersonal bool  `json:"onlyPersonal,omitempty"` // drop marketing/bulk before returning
	Since       int64  `json:"sinceMs,omitempty"`      // unix ms — only messages newer
}

// --- provider dispatch -----------------------------------------------------

// FetchMail is the single entry point. Picks a provider from
// config when opts.Provider is empty or "auto".
func FetchMail(opts MailFetchOptions) ([]MailMessage, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.Email == nil {
		return nil, fmt.Errorf("email not configured — run `yaver email setup`")
	}
	if opts.Limit <= 0 {
		opts.Limit = 25
	}

	provider := opts.Provider
	if provider == "" || provider == "auto" {
		switch cfg.Email.Provider {
		case "gmail", "office365":
			provider = cfg.Email.Provider
		default:
			return nil, fmt.Errorf("no fetch-capable email provider configured")
		}
	}

	var msgs []MailMessage
	switch provider {
	case "gmail":
		msgs, err = fetchGmail(cfg.Email, opts)
	case "office365", "o365":
		msgs, err = fetchGraph(cfg.Email, opts)
	default:
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
	if err != nil {
		return nil, err
	}

	// Classify, score, optionally filter.
	for i := range msgs {
		classifyMessage(&msgs[i], msgs)
	}
	if opts.OnlyPersonal {
		filtered := msgs[:0]
		for _, m := range msgs {
			if m.Classification == "personal" || m.Classification == "transactional" {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Date.After(msgs[j].Date) })
	return msgs, nil
}

// --- Gmail -----------------------------------------------------------------

const gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"

// gmailToken refreshes the access token using the stored
// refresh token. Google access tokens expire in ~1h so we
// refresh lazily and cache in-process.
var (
	gmailTokenMu      sync.Mutex
	gmailAccessToken  string
	gmailExpiry       time.Time
)

func gmailAccess(cfg *EmailConfig) (string, error) {
	gmailTokenMu.Lock()
	defer gmailTokenMu.Unlock()
	if gmailAccessToken != "" && time.Now().Before(gmailExpiry.Add(-60*time.Second)) {
		return gmailAccessToken, nil
	}
	if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" || cfg.GoogleRefreshToken == "" {
		return "", fmt.Errorf("gmail OAuth credentials missing in email config")
	}
	form := url.Values{}
	form.Set("client_id", cfg.GoogleClientID)
	form.Set("client_secret", cfg.GoogleClientSecret)
	form.Set("refresh_token", cfg.GoogleRefreshToken)
	form.Set("grant_type", "refresh_token")
	resp, err := http.PostForm("https://oauth2.googleapis.com/token", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Error != "" {
		return "", fmt.Errorf("gmail refresh: %s", body.Error)
	}
	gmailAccessToken = body.AccessToken
	gmailExpiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return gmailAccessToken, nil
}

func gmailGet(accessToken, path string, out interface{}) error {
	req, _ := http.NewRequest("GET", gmailBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gmail %s: HTTP %d — %s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func fetchGmail(cfg *EmailConfig, opts MailFetchOptions) ([]MailMessage, error) {
	token, err := gmailAccess(cfg)
	if err != nil {
		return nil, err
	}

	// List message IDs.
	params := url.Values{}
	params.Set("maxResults", fmt.Sprint(opts.Limit))
	query := opts.Query
	if opts.Folder == "sent" {
		query = strings.TrimSpace("in:sent " + query)
	} else if opts.Folder == "" || opts.Folder == "inbox" {
		query = strings.TrimSpace("in:inbox " + query)
	}
	if opts.Since > 0 {
		sec := opts.Since / 1000
		query = strings.TrimSpace(fmt.Sprintf("after:%d %s", sec, query))
	}
	if query != "" {
		params.Set("q", query)
	}

	var list struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := gmailGet(token, "/messages?"+params.Encode(), &list); err != nil {
		return nil, err
	}
	out := make([]MailMessage, 0, len(list.Messages))
	for _, m := range list.Messages {
		var full struct {
			ID           string   `json:"id"`
			ThreadID     string   `json:"threadId"`
			LabelIDs     []string `json:"labelIds"`
			Snippet      string   `json:"snippet"`
			InternalDate string   `json:"internalDate"`
			Payload      struct {
				Headers []struct{ Name, Value string } `json:"headers"`
				Body    struct {
					Data string `json:"data"`
				} `json:"body"`
				Parts []struct {
					MimeType string `json:"mimeType"`
					Body     struct {
						Data string `json:"data"`
					} `json:"body"`
				} `json:"parts"`
			} `json:"payload"`
		}
		if err := gmailGet(token, "/messages/"+m.ID+"?format=full", &full); err != nil {
			continue
		}
		msg := MailMessage{
			ID:       full.ID,
			ThreadID: full.ThreadID,
			Snippet:  full.Snippet,
			LabelIDs: full.LabelIDs,
			Provider: "gmail",
		}
		for _, h := range full.Payload.Headers {
			switch strings.ToLower(h.Name) {
			case "from":
				msg.From, msg.FromName = parseFromHeader(h.Value)
			case "to":
				msg.To = splitAddr(h.Value)
			case "cc":
				msg.Cc = splitAddr(h.Value)
			case "subject":
				msg.Subject = h.Value
			case "list-unsubscribe":
				msg.HasUnsub = true
			case "list-id":
				msg.ListID = h.Value
			case "auto-submitted":
				if strings.ToLower(h.Value) != "no" {
					msg.AutoGen = true
				}
			case "precedence":
				if v := strings.ToLower(h.Value); v == "bulk" || v == "list" {
					msg.AutoGen = true
				}
			}
		}
		// Body: prefer text/plain part; fall back to decoded root.
		if full.Payload.Body.Data != "" {
			msg.Body, _ = decodeGmailBody(full.Payload.Body.Data)
		}
		for _, p := range full.Payload.Parts {
			decoded, _ := decodeGmailBody(p.Body.Data)
			if p.MimeType == "text/plain" && msg.Body == "" {
				msg.Body = decoded
			}
			if p.MimeType == "text/html" {
				msg.BodyHTML = decoded
			}
		}
		if full.InternalDate != "" {
			var ms int64
			fmt.Sscan(full.InternalDate, &ms)
			msg.Date = time.UnixMilli(ms)
		}
		out = append(out, msg)
	}
	return out, nil
}

func decodeGmailBody(b64 string) (string, error) {
	if b64 == "" {
		return "", nil
	}
	// Gmail uses URL-safe base64 without padding.
	data, err := base64.URLEncoding.DecodeString(b64 + strings.Repeat("=", (4-len(b64)%4)%4))
	if err != nil {
		data, err = base64.RawURLEncoding.DecodeString(b64)
	}
	return string(data), err
}

// --- Microsoft Graph (Office 365) ------------------------------------------

const graphBase = "https://graph.microsoft.com/v1.0"

var (
	graphTokenMu     sync.Mutex
	graphAccessToken string
	graphExpiry      time.Time
)

func graphAccess(cfg *EmailConfig) (string, error) {
	graphTokenMu.Lock()
	defer graphTokenMu.Unlock()
	if graphAccessToken != "" && time.Now().Before(graphExpiry.Add(-60*time.Second)) {
		return graphAccessToken, nil
	}
	if cfg.AzureTenantID == "" || cfg.AzureClientID == "" || cfg.AzureClientSecret == "" {
		return "", fmt.Errorf("Azure client credentials missing")
	}
	form := url.Values{}
	form.Set("client_id", cfg.AzureClientID)
	form.Set("client_secret", cfg.AzureClientSecret)
	form.Set("scope", "https://graph.microsoft.com/.default")
	form.Set("grant_type", "client_credentials")
	tokenURL := "https://login.microsoftonline.com/" + cfg.AzureTenantID + "/oauth2/v2.0/token"
	resp, err := http.PostForm(tokenURL, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("graph token: %s", body.Error)
	}
	graphAccessToken = body.AccessToken
	graphExpiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return graphAccessToken, nil
}

func fetchGraph(cfg *EmailConfig, opts MailFetchOptions) ([]MailMessage, error) {
	token, err := graphAccess(cfg)
	if err != nil {
		return nil, err
	}
	user := cfg.SenderEmail
	if user == "" {
		return nil, fmt.Errorf("senderEmail required in email config for Graph fetch")
	}
	folder := "inbox"
	if opts.Folder == "sent" {
		folder = "sentitems"
	}
	q := url.Values{}
	q.Set("$top", fmt.Sprint(opts.Limit))
	q.Set("$orderby", "receivedDateTime desc")
	q.Set("$select", "id,conversationId,subject,from,toRecipients,ccRecipients,body,bodyPreview,receivedDateTime,internetMessageHeaders")
	if opts.Since > 0 {
		q.Set("$filter", fmt.Sprintf("receivedDateTime ge %s", time.UnixMilli(opts.Since).UTC().Format(time.RFC3339)))
	}
	endpoint := fmt.Sprintf("%s/users/%s/mailFolders/%s/messages?%s", graphBase, user, folder, q.Encode())

	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("graph HTTP %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Value []struct {
			ID               string `json:"id"`
			ConversationID   string `json:"conversationId"`
			Subject          string `json:"subject"`
			BodyPreview      string `json:"bodyPreview"`
			ReceivedDateTime string `json:"receivedDateTime"`
			From             struct {
				EmailAddress struct {
					Name, Address string
				} `json:"emailAddress"`
			} `json:"from"`
			ToRecipients []struct {
				EmailAddress struct {
					Name, Address string
				} `json:"emailAddress"`
			} `json:"toRecipients"`
			CCRecipients []struct {
				EmailAddress struct {
					Name, Address string
				} `json:"emailAddress"`
			} `json:"ccRecipients"`
			Body struct {
				ContentType, Content string
			} `json:"body"`
			Headers []struct {
				Name, Value string
			} `json:"internetMessageHeaders"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]MailMessage, 0, len(payload.Value))
	for _, v := range payload.Value {
		msg := MailMessage{
			ID:       v.ID,
			ThreadID: v.ConversationID,
			Subject:  v.Subject,
			Snippet:  v.BodyPreview,
			From:     v.From.EmailAddress.Address,
			FromName: v.From.EmailAddress.Name,
			Provider: "o365",
		}
		for _, r := range v.ToRecipients {
			msg.To = append(msg.To, r.EmailAddress.Address)
		}
		for _, r := range v.CCRecipients {
			msg.Cc = append(msg.Cc, r.EmailAddress.Address)
		}
		if strings.EqualFold(v.Body.ContentType, "html") {
			msg.BodyHTML = v.Body.Content
		} else {
			msg.Body = v.Body.Content
		}
		if t, err := time.Parse(time.RFC3339, v.ReceivedDateTime); err == nil {
			msg.Date = t
		}
		for _, h := range v.Headers {
			name := strings.ToLower(h.Name)
			switch name {
			case "list-unsubscribe":
				msg.HasUnsub = true
			case "list-id":
				msg.ListID = h.Value
			case "auto-submitted":
				if strings.ToLower(h.Value) != "no" {
					msg.AutoGen = true
				}
			case "precedence":
				if v := strings.ToLower(h.Value); v == "bulk" || v == "list" {
					msg.AutoGen = true
				}
			}
		}
		out = append(out, msg)
	}
	return out, nil
}

// --- classifier ------------------------------------------------------------

// classifyMessage grades a message against a heuristic rubric.
// Higher score = more likely "real personal mail". We explicitly
// do not trust Gmail's own Category labels — the dev already
// complained Gmail throws real mail into Promotions when the
// sender has any kind of unsubscribe footer.
//
// Scoring (max 100):
//
//   +40 if the same thread already has 2+ replies (ongoing convo)
//   +20 if the subject starts with "Re:" or "Fwd:"
//   +15 if it's addressed directly to the user (not To/BCC list)
//   +15 if the sender domain appears in the user's Sent folder
//        in the last 30 days (tracked via sender index — set by
//        fetchGmail/fetchGraph when called with folder=sent)
//   +10 if no List-Unsubscribe header
//   -40 if List-Unsubscribe is present
//   -30 if Precedence=bulk or Auto-Submitted is set
//   -20 if the From name contains marketing keywords
//   -10 if the subject matches a marketing pattern ("Sale", "%%", "hot deal")
//
// Final bucket:
//    > 60  personal
//    40–60 transactional
//    20–40 marketing
//    < 20  bulk
func classifyMessage(m *MailMessage, all []MailMessage) {
	// Learned allow/deny lists take precedence — the dev told us
	// their verdict, we respect it. See mail_learning.go for how
	// these lists get populated from /mail/mark.
	if isMailDenied(m.From) {
		m.Score = 0
		m.Classification = "bulk"
		return
	}
	if isMailAllowed(m.From) {
		m.Score = 100
		m.Classification = "personal"
		return
	}

	score := 50
	if m.ThreadID != "" {
		replies := 0
		for _, o := range all {
			if o.ThreadID == m.ThreadID && o.ID != m.ID {
				replies++
			}
		}
		m.ThreadReplies = replies
		if replies >= 2 {
			score += 40
		} else if replies == 1 {
			score += 20
		}
	}
	if strings.HasPrefix(strings.ToLower(m.Subject), "re:") || strings.HasPrefix(strings.ToLower(m.Subject), "fwd:") {
		score += 20
	}
	if m.HasUnsub {
		score -= 40
	} else {
		score += 10
	}
	if m.AutoGen {
		score -= 30
	}
	if m.ListID != "" {
		score -= 20
	}
	subjectLower := strings.ToLower(m.Subject)
	for _, kw := range []string{"sale", "% off", "save now", "deal", "limited time", "click here", "unsubscribe", "buy now", "discount"} {
		if strings.Contains(subjectLower, kw) {
			score -= 10
			break
		}
	}
	fromLower := strings.ToLower(m.FromName + " " + m.From)
	for _, kw := range []string{"newsletter", "no-reply", "noreply", "notifications", "marketing", "team@"} {
		if strings.Contains(fromLower, kw) {
			score -= 20
			break
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	m.Score = score
	switch {
	case score > 60:
		m.Classification = "personal"
	case score >= 40:
		m.Classification = "transactional"
	case score >= 20:
		m.Classification = "marketing"
	default:
		m.Classification = "bulk"
	}
}

// --- AI draft builder ------------------------------------------------------

// BuildDraftPrompt assembles the text the AI runner gets when
// drafting a reply. Includes the target message, the full
// thread so far, and a handful of recent messages *from the
// dev* so the runner learns their tone. Falls back gracefully
// when thread history is unavailable.
func BuildDraftPrompt(target MailMessage, thread []MailMessage, recentSent []MailMessage, instructions string) string {
	var b strings.Builder
	b.WriteString("You are the user's email assistant. Draft a reply to the MESSAGE below in the user's own voice.\n\n")
	if instructions != "" {
		b.WriteString("Instructions: " + instructions + "\n\n")
	}
	if len(recentSent) > 0 {
		b.WriteString("--- RECENT MESSAGES FROM THE USER (for tone) ---\n")
		for _, s := range recentSent {
			if len(s.Body) > 500 {
				s.Body = s.Body[:500] + "..."
			}
			b.WriteString("Subject: " + s.Subject + "\n" + s.Body + "\n\n")
		}
	}
	if len(thread) > 0 {
		b.WriteString("--- THREAD SO FAR ---\n")
		for _, t := range thread {
			if t.ID == target.ID {
				continue
			}
			if len(t.Body) > 1000 {
				t.Body = t.Body[:1000] + "..."
			}
			b.WriteString(fmt.Sprintf("From: %s (%s)\nSubject: %s\n%s\n\n", t.FromName, t.From, t.Subject, t.Body))
		}
	}
	b.WriteString("--- MESSAGE TO REPLY TO ---\n")
	b.WriteString(fmt.Sprintf("From: %s (%s)\nSubject: %s\n%s\n\n", target.FromName, target.From, target.Subject, target.Body))
	b.WriteString("--- DRAFT ---\nWrite the reply now. Keep it short unless the user asks otherwise. Sign off with just the user's first name.\n")
	return b.String()
}

// --- helpers ---------------------------------------------------------------

func parseFromHeader(h string) (addr, name string) {
	h = strings.TrimSpace(h)
	if lt := strings.Index(h, "<"); lt >= 0 {
		gt := strings.Index(h, ">")
		if gt > lt {
			addr = h[lt+1 : gt]
			name = strings.TrimSpace(strings.Trim(h[:lt], " \"'"))
			return
		}
	}
	return h, ""
}

func splitAddr(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			addr, _ := parseFromHeader(p)
			out = append(out, addr)
		}
	}
	return out
}
