package main

// mail_fetch_http.go — HTTP + CLI surface for mail triage.
//
// Endpoints:
//
//   GET  /mail/inbox?provider=&limit=&onlyPersonal=...  list messages
//   GET  /mail/message?id=&provider=...                  single message
//   POST /mail/draft                                     {id, instructions} → AI prompt
//   POST /mail/send                                      {to, subject, body}
//   POST /mail/classify                                  reclassify a message
//
//   POST /mail/onboard/start                             owner-only: start OAuth
//   GET  /mail/onboard/status                            poll
//   GET  /mail/onboard/callback                          OAuth redirect landing
//
// Onboarding uses the existing Gmail / Azure OAuth apps. The
// dev taps "Connect Gmail" in the mobile app → mobile calls
// /mail/onboard/start → agent returns a browser URL → mobile
// app opens the URL in a SafariViewController → user signs in
// → Google redirects back to the agent (via the configured
// tunnel / relay URL) → agent captures the code, exchanges it
// for refresh+access tokens, writes them into config.json.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// contextTimeout is a tiny helper so the mail draft call can
// cap runner execution without pulling context into every
// handler signature.
func contextTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// execCmdCtx returns an *exec.Cmd bound to the given context
// so a runaway runner is killed when the timeout fires.
func execCmdCtx(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}

// --- fetch endpoints -------------------------------------------------------

func (s *HTTPServer) handleMailInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	since, _ := strconv.ParseInt(q.Get("sinceMs"), 10, 64)
	opts := MailFetchOptions{
		Provider:     q.Get("provider"),
		Folder:       q.Get("folder"),
		Query:        q.Get("q"),
		Limit:        limit,
		OnlyPersonal: q.Get("onlyPersonal") == "true",
		Since:        since,
	}
	msgs, err := FetchMail(opts)
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"messages": msgs,
		"counts":   countByClassification(msgs),
	})
}

func countByClassification(msgs []MailMessage) map[string]int {
	out := map[string]int{"personal": 0, "transactional": 0, "marketing": 0, "bulk": 0}
	for _, m := range msgs {
		out[m.Classification]++
	}
	return out
}

func (s *HTTPServer) handleMailDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		ID           string `json:"id"`
		Provider     string `json:"provider"`
		Instructions string `json:"instructions"`
		Execute      bool   `json:"execute"` // run the runner inline
		Runner       string `json:"runner,omitempty"` // override default
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.ID == "" {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}

	// Pull the target + its thread + some recent sent messages.
	all, err := FetchMail(MailFetchOptions{Provider: body.Provider, Folder: "inbox", Limit: 50})
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	var target MailMessage
	thread := make([]MailMessage, 0, 8)
	for _, m := range all {
		if m.ID == body.ID {
			target = m
		}
	}
	if target.ID == "" {
		jsonError(w, http.StatusNotFound, "message not found in recent inbox window")
		return
	}
	for _, m := range all {
		if m.ThreadID == target.ThreadID {
			thread = append(thread, m)
		}
	}
	sent, _ := FetchMail(MailFetchOptions{Provider: body.Provider, Folder: "sent", Limit: 10})

	prompt := BuildDraftPrompt(target, thread, sent, body.Instructions)
	out := map[string]interface{}{
		"ok":     true,
		"target": target,
		"prompt": prompt,
	}
	if body.Execute {
		reply, runErr := runMailDraftInline(body.Runner, prompt)
		if runErr != nil {
			out["draft"] = ""
			out["error"] = runErr.Error()
		} else {
			out["draft"] = reply
		}
	}
	jsonReply(w, http.StatusOK, out)
}

func (s *HTTPServer) handleMailSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req SendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	res, err := SendTransactionalEmail(req)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "result": res})
}

// runMailDraftInline pipes the prompt into the configured
// runner (Claude, Codex, Ollama, …) and returns the draft
// text. Synchronous, capped at 120s so a misbehaving runner
// can't hang the mobile app.
//
// For stream-json runners (Claude) we still collect into a
// flat string and extract the assistant text — mobile isn't
// the place for token-streaming today, and the caller already
// has the raw prompt if they want an attach-session experience.
func runMailDraftInline(runnerID, prompt string) (string, error) {
	if runnerID == "" {
		runnerID = "claude"
	}
	cfg := GetRunnerConfig(runnerID)
	if cfg.Command == "" {
		return "", fmt.Errorf("unknown runner %q", runnerID)
	}
	// Substitute {prompt} in each argument. We do NOT shell out
	// through sh -c, so the prompt is passed as an argv element
	// and no escaping is required.
	args := make([]string, 0, len(cfg.Args))
	for _, a := range cfg.Args {
		args = append(args, strings.ReplaceAll(a, "{prompt}", prompt))
	}
	ctx, cancel := contextTimeout(120 * time.Second)
	defer cancel()
	cmd := execCmdCtx(ctx, cfg.Command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %v — %s", runnerID, err, strings.TrimSpace(string(out)))
	}
	return extractRunnerReply(cfg.OutputMode, string(out)), nil
}

// extractRunnerReply pulls the assistant text out of whatever
// the runner wrote to stdout. Claude's stream-json format is
// newline-delimited JSON with `type: "assistant"` messages;
// everything else is assumed to be raw.
func extractRunnerReply(mode, output string) string {
	if mode != "stream-json" {
		return strings.TrimSpace(output)
	}
	var b strings.Builder
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var evt struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Type == "assistant" {
			for _, c := range evt.Message.Content {
				if c.Type == "text" {
					b.WriteString(c.Text)
				}
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// --- OAuth onboarding (mobile-friendly) ------------------------------------

// OnboardSession tracks one in-flight OAuth handshake.
type OnboardSession struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"` // gmail | o365
	Status    string    `json:"status"`   // pending | done | failed
	StartedAt time.Time `json:"startedAt"`
	Error     string    `json:"error,omitempty"`
	Email     string    `json:"email,omitempty"`
}

var (
	onboardMu       sync.Mutex
	onboardSessions = map[string]*OnboardSession{}
)

// handleMailConfig saves email OAuth credentials from the mobile app.
// POST /mail/config { provider: "gmail", clientId: "...", clientSecret: "..." }
// Returns { ok: true, redirectUri: "https://public.yaver.io/mail/onboard/callback" }
// so the user can paste the redirect URI into Google Cloud Console.
func (s *HTTPServer) handleMailConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Return current config state (without secrets) + redirect URI to paste
		cfg, _ := LoadConfig()
		resp := map[string]interface{}{
			"redirectUri":    publicOauthBase(r) + "/mail/onboard/callback",
			"gmailConfigured": cfg != nil && cfg.Email != nil && cfg.Email.GoogleClientID != "",
			"o365Configured":  cfg != nil && cfg.Email != nil && cfg.Email.AzureClientID != "",
		}
		jsonReply(w, http.StatusOK, resp)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Provider     string `json:"provider"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		TenantID     string `json:"tenantId,omitempty"` // o365 only
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.ClientID == "" || body.ClientSecret == "" {
		jsonError(w, http.StatusBadRequest, "clientId and clientSecret required")
		return
	}
	cfg, _ := LoadConfig()
	if cfg == nil {
		jsonError(w, http.StatusInternalServerError, "no config loaded")
		return
	}
	if cfg.Email == nil {
		cfg.Email = &EmailConfig{}
	}
	switch body.Provider {
	case "gmail":
		cfg.Email.Provider = "gmail"
		cfg.Email.GoogleClientID = body.ClientID
		cfg.Email.GoogleClientSecret = body.ClientSecret
	case "o365", "office365":
		cfg.Email.Provider = "office365"
		cfg.Email.AzureClientID = body.ClientID
		cfg.Email.AzureClientSecret = body.ClientSecret
		if body.TenantID != "" {
			cfg.Email.AzureTenantID = body.TenantID
		}
	default:
		jsonError(w, http.StatusBadRequest, "provider must be 'gmail' or 'o365'")
		return
	}
	if err := SaveConfig(cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "save failed: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"redirectUri": publicOauthBase(r) + "/mail/onboard/callback",
	})
}

func (s *HTTPServer) handleMailOnboardStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Provider    string `json:"provider"` // gmail | o365
		RedirectURL string `json:"redirectUrl,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.Email == nil {
		jsonError(w, http.StatusPreconditionFailed, "run `yaver email setup` once to install OAuth app credentials first")
		return
	}
	id := randomFormID()
	sess := &OnboardSession{
		ID:        id,
		Provider:  body.Provider,
		Status:    "pending",
		StartedAt: time.Now(),
	}
	onboardMu.Lock()
	onboardSessions[id] = sess
	onboardMu.Unlock()

	redirect := body.RedirectURL
	if redirect == "" {
		redirect = publicOauthBase(r) + "/mail/onboard/callback"
	}
	var authURL string
	switch body.Provider {
	case "gmail":
		if cfg.Email.GoogleClientID == "" {
			jsonError(w, http.StatusPreconditionFailed, "Gmail OAuth client ID not configured")
			return
		}
		q := url.Values{}
		q.Set("client_id", cfg.Email.GoogleClientID)
		q.Set("redirect_uri", redirect)
		q.Set("response_type", "code")
		q.Set("scope", "https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.send")
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
		q.Set("state", id)
		authURL = "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode()
	case "o365", "office365":
		if cfg.Email.AzureTenantID == "" || cfg.Email.AzureClientID == "" {
			jsonError(w, http.StatusPreconditionFailed, "Azure tenant/client not configured")
			return
		}
		q := url.Values{}
		q.Set("client_id", cfg.Email.AzureClientID)
		q.Set("redirect_uri", redirect)
		q.Set("response_type", "code")
		q.Set("scope", "offline_access https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Mail.Send")
		q.Set("state", id)
		authURL = fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize?%s", cfg.Email.AzureTenantID, q.Encode())
	default:
		jsonError(w, http.StatusBadRequest, "provider must be gmail or o365")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"sessionId":  id,
		"authUrl":    authURL,
		"redirectTo": redirect,
	})
}

func (s *HTTPServer) handleMailOnboardCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code/state", http.StatusBadRequest)
		return
	}
	onboardMu.Lock()
	sess := onboardSessions[state]
	onboardMu.Unlock()
	if sess == nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.Email == nil {
		http.Error(w, "email config missing", http.StatusPreconditionFailed)
		return
	}
	redirect := publicOauthBase(r) + "/mail/onboard/callback"

	switch sess.Provider {
	case "gmail":
		form := url.Values{}
		form.Set("code", code)
		form.Set("client_id", cfg.Email.GoogleClientID)
		form.Set("client_secret", cfg.Email.GoogleClientSecret)
		form.Set("redirect_uri", redirect)
		form.Set("grant_type", "authorization_code")
		resp, err := http.PostForm("https://oauth2.googleapis.com/token", form)
		if err != nil {
			sess.Status = "failed"
			sess.Error = err.Error()
			writeCallbackHTML(w, sess)
			return
		}
		defer resp.Body.Close()
		var body struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
			ErrorMsg     string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body.RefreshToken == "" {
			sess.Status = "failed"
			sess.Error = "no refresh token: " + body.ErrorMsg
			writeCallbackHTML(w, sess)
			return
		}
		cfg.Email.Provider = "gmail"
		cfg.Email.GoogleRefreshToken = body.RefreshToken
		_ = SaveConfig(cfg)
		sess.Status = "done"
	case "o365", "office365":
		form := url.Values{}
		form.Set("code", code)
		form.Set("client_id", cfg.Email.AzureClientID)
		form.Set("client_secret", cfg.Email.AzureClientSecret)
		form.Set("redirect_uri", redirect)
		form.Set("grant_type", "authorization_code")
		tokenURL := "https://login.microsoftonline.com/" + cfg.Email.AzureTenantID + "/oauth2/v2.0/token"
		resp, err := http.PostForm(tokenURL, form)
		if err != nil {
			sess.Status = "failed"
			sess.Error = err.Error()
			writeCallbackHTML(w, sess)
			return
		}
		defer resp.Body.Close()
		cfg.Email.Provider = "office365"
		_ = SaveConfig(cfg)
		sess.Status = "done"
	}
	writeCallbackHTML(w, sess)
}

func writeCallbackHTML(w http.ResponseWriter, sess *OnboardSession) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if sess.Status == "done" {
		fmt.Fprintf(w, "<html><body style='font-family:system-ui;padding:40px;text-align:center'><h1>Connected!</h1><p>You can close this tab and return to Yaver.</p><script>setTimeout(()=>window.close(),1500)</script></body></html>")
	} else {
		fmt.Fprintf(w, "<html><body style='font-family:system-ui;padding:40px;text-align:center'><h1>Connection failed</h1><p>%s</p></body></html>", sess.Error)
	}
}

func (s *HTTPServer) handleMailOnboardStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	onboardMu.Lock()
	sess := onboardSessions[id]
	onboardMu.Unlock()
	if sess == nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}
	// Also surface whether the config now has usable credentials.
	cfg, _ := LoadConfig()
	ready := false
	if cfg != nil && cfg.Email != nil {
		switch sess.Provider {
		case "gmail":
			ready = cfg.Email.GoogleRefreshToken != ""
		case "o365", "office365":
			ready = cfg.Email.Provider == "office365"
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"session": sess,
		"ready":   ready,
	})
}

// --- CLI -------------------------------------------------------------------

// runMail dispatches the `yaver mail …` subcommands. Kept in
// this file so all mail plumbing is in one place.
func runMail(args []string) {
	if len(args) == 0 {
		fmt.Println("usage:")
		fmt.Println("  yaver mail inbox [--limit 25] [--only-personal] [--provider gmail|o365]")
		fmt.Println("  yaver mail draft <id> [--instructions '...'] [--provider gmail|o365]")
		fmt.Println("  yaver mail send --to user@example.com --subject '...' --body '...'")
		fmt.Println("  yaver mail connect <gmail|o365>   start OAuth onboarding")
		return
	}
	switch args[0] {
	case "inbox":
		runMailInbox(args[1:])
	case "draft":
		runMailDraft(args[1:])
	case "send":
		runMailSend(args[1:])
	case "connect":
		runMailConnect(args[1:])
	default:
		fmt.Println("unknown subcommand:", args[0])
	}
}

func runMailInbox(args []string) {
	opts := MailFetchOptions{Limit: 25}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 < len(args) {
				opts.Limit, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--only-personal":
			opts.OnlyPersonal = true
		case "--provider":
			if i+1 < len(args) {
				opts.Provider = args[i+1]
				i++
			}
		case "--folder":
			if i+1 < len(args) {
				opts.Folder = args[i+1]
				i++
			}
		}
	}
	msgs, err := FetchMail(opts)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, m := range msgs {
		snippet := strings.TrimSpace(m.Snippet)
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		fmt.Printf("%-10s  %-10s  %-28s  %s\n", m.Classification, m.Date.Format("Jan 02 15:04"), truncateMail(m.FromName+" <"+m.From+">", 28), m.Subject)
	}
}

func runMailDraft(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: yaver mail draft <id> [--instructions '...']")
		return
	}
	id := args[0]
	instructions := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--instructions" && i+1 < len(args) {
			instructions = args[i+1]
			i++
		}
	}
	all, err := FetchMail(MailFetchOptions{Folder: "inbox", Limit: 50})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	var target MailMessage
	var thread []MailMessage
	for _, m := range all {
		if m.ID == id {
			target = m
		}
	}
	if target.ID == "" {
		fmt.Println("message not found in recent inbox window")
		return
	}
	for _, m := range all {
		if m.ThreadID == target.ThreadID {
			thread = append(thread, m)
		}
	}
	sent, _ := FetchMail(MailFetchOptions{Folder: "sent", Limit: 10})
	prompt := BuildDraftPrompt(target, thread, sent, instructions)
	fmt.Println(prompt)
}

func runMailSend(args []string) {
	req := SendEmailRequest{}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--to":
			req.To = append(req.To, args[i+1])
			i++
		case "--subject":
			req.Subject = args[i+1]
			i++
		case "--body":
			req.Body = args[i+1]
			i++
		case "--from":
			req.From = args[i+1]
			i++
		}
	}
	if len(req.To) == 0 || req.Subject == "" {
		fmt.Println("usage: yaver mail send --to ... --subject ... --body ...")
		return
	}
	res, err := SendTransactionalEmail(req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("sent", res.MessageID)
}

func runMailConnect(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: yaver mail connect <gmail|o365>")
		return
	}
	provider := args[0]
	fmt.Println("POST /mail/onboard/start from the mobile app or web dashboard to start the OAuth flow.")
	fmt.Println("Provider:", provider)
	fmt.Println("(CLI-only fallback: paste your Google refresh token into ~/.yaver/config.json under email.google_refresh_token)")
}

// truncateMail is the inbox-specific length cap; renamed from
// truncate to avoid clashing with tasks.go.
func truncateMail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
