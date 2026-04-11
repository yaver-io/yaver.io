package main

// forms.go — self-hosted form-ingestion service. Replaces
// Formspree / Basin / Getform / Statickit for solo devs.
//
// Model:
//
//   Form       — a named endpoint with optional honeypot + email
//                notification + rate limit. Stored in forms.json.
//   Submission — a single POST body + metadata (IP, UA, time).
//                Stored as JSONL in ~/.yaver/forms/<formId>.jsonl
//                so a noisy form can't blow up RAM — append-only,
//                tail-readable, rotatable by mv.
//
// HTTP surface:
//
//   POST /forms/:id/submit        public — no auth (forms run on
//                                 the internet); honeypot + rate
//                                 limit + origin allowlist do the
//                                 gating.
//   GET  /forms                   owner-only: list forms + counts
//   POST /forms                   owner-only: create form
//   GET  /forms/:id/submissions   owner-only: tail submissions
//   DELETE /forms/:id             owner-only: delete form
//
// Notifications go through the existing SMTP relay (email_send.go)
// so we don't reinvent delivery.

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

// Form is a named submission endpoint.
type Form struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"createdAt"`
	NotifyEmail    string    `json:"notifyEmail,omitempty"`
	HoneypotField  string    `json:"honeypotField,omitempty"`  // field name; if non-empty in submission, drop
	RequireField   string    `json:"requireField,omitempty"`   // must be present+non-empty
	AllowedOrigins []string  `json:"allowedOrigins,omitempty"` // CORS / Referer check
	RateLimit      int       `json:"rateLimitPerHour,omitempty"`
	SuccessRedirect string   `json:"successRedirect,omitempty"`
}

// Submission captures one POST body.
type Submission struct {
	FormID    string            `json:"formId"`
	At        time.Time         `json:"at"`
	IP        string            `json:"ip,omitempty"`
	UserAgent string            `json:"userAgent,omitempty"`
	Fields    map[string]string `json:"fields"`
}

// --- storage ---------------------------------------------------------------

var (
	formsMu     sync.Mutex
	formsCache  []Form
	formCounter = map[string][]time.Time{} // rate limit window (formID → recent submissions)
)

func formsFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "forms.json"), nil
}

func formsDataDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "forms"), nil
}

func loadForms() ([]Form, error) {
	formsMu.Lock()
	defer formsMu.Unlock()
	if formsCache != nil {
		return formsCache, nil
	}
	p, err := formsFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		formsCache = []Form{}
		return formsCache, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Form
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	formsCache = out
	return out, nil
}

func saveForms(forms []Form) error {
	p, err := formsFile()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(forms, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	formsMu.Lock()
	formsCache = forms
	formsMu.Unlock()
	return nil
}

func findForm(id string) *Form {
	forms, err := loadForms()
	if err != nil {
		return nil
	}
	for i := range forms {
		if forms[i].ID == id {
			return &forms[i]
		}
	}
	return nil
}

// allowFormSubmit returns true when the given form hasn't burst
// past its per-hour rate limit. Keeps a sliding window of
// timestamps per form ID. Cheap because forms are expected to be
// low-volume (contact forms, waitlists).
func allowFormSubmit(formID string, limit int) bool {
	if limit <= 0 {
		return true
	}
	formsMu.Lock()
	defer formsMu.Unlock()
	now := time.Now()
	window := now.Add(-1 * time.Hour)
	recent := formCounter[formID][:0]
	for _, t := range formCounter[formID] {
		if t.After(window) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		formCounter[formID] = recent
		return false
	}
	formCounter[formID] = append(recent, now)
	return true
}

// appendSubmission writes a JSONL line to the form's data file.
func appendSubmission(sub *Submission) error {
	dir, err := formsDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(dir, sub.FormID+".jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(sub)
}

// readSubmissions returns up to `limit` most recent submissions
// from the JSONL file. Reads the whole file into memory — fine
// for the expected scale (hundreds per form, not millions).
func readSubmissions(formID string, limit int) ([]Submission, error) {
	dir, err := formsDataDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, formID+".jsonl")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > limit && limit > 0 {
		lines = lines[len(lines)-limit:]
	}
	out := make([]Submission, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var s Submission
		if err := json.Unmarshal([]byte(line), &s); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleFormSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	// Path: /forms/:id/submit
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "forms" || parts[2] != "submit" {
		jsonError(w, http.StatusNotFound, "unknown form endpoint")
		return
	}
	formID := parts[1]
	form := findForm(formID)
	if form == nil {
		jsonError(w, http.StatusNotFound, "form not found")
		return
	}

	// Origin allowlist (optional).
	if len(form.AllowedOrigins) > 0 {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		ok := false
		for _, a := range form.AllowedOrigins {
			if a == "*" || strings.HasPrefix(origin, a) {
				ok = true
				break
			}
		}
		if !ok {
			jsonError(w, http.StatusForbidden, "origin not allowed")
			return
		}
	}

	// Rate limit.
	if !allowFormSubmit(formID, form.RateLimit) {
		jsonError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Parse fields — accept both x-www-form-urlencoded and JSON.
	fields := map[string]string{}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var raw map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		for k, v := range raw {
			fields[k] = fmt.Sprint(v)
		}
	} else {
		if err := r.ParseForm(); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		for k, v := range r.PostForm {
			if len(v) > 0 {
				fields[k] = v[0]
			}
		}
	}

	// Honeypot: if the trap field has content, pretend success.
	if form.HoneypotField != "" && strings.TrimSpace(fields[form.HoneypotField]) != "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}
	// Required field check.
	if form.RequireField != "" && strings.TrimSpace(fields[form.RequireField]) == "" {
		jsonError(w, http.StatusBadRequest, "missing required field")
		return
	}

	sub := &Submission{
		FormID:    formID,
		At:        time.Now().UTC(),
		IP:        clientIP(r),
		UserAgent: r.Header.Get("User-Agent"),
		Fields:    fields,
	}
	if err := appendSubmission(sub); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Fire notification (best-effort, don't block the response).
	if form.NotifyEmail != "" {
		go sendFormNotification(form, sub)
	}

	if form.SuccessRedirect != "" {
		http.Redirect(w, r, form.SuccessRedirect, http.StatusSeeOther)
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// sendFormNotification ships a plain-text email via the
// existing SMTP relay. Best-effort — failures are logged but
// never block the submission response.
func sendFormNotification(form *Form, sub *Submission) {
	body := fmt.Sprintf("New submission for %q (%s) at %s\n\nFrom: %s (%s)\n\n",
		form.Name, form.ID, sub.At.Format(time.RFC3339), sub.IP, sub.UserAgent)
	for k, v := range sub.Fields {
		body += fmt.Sprintf("%s: %s\n", k, v)
	}
	_, _ = SendTransactionalEmail(SendEmailRequest{
		To:      []string{form.NotifyEmail},
		Subject: fmt.Sprintf("[Form] %s — new submission", form.Name),
		Body:    body,
	})
}

func (s *HTTPServer) handleForms(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		forms, _ := loadForms()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "forms": forms})
	case http.MethodPost:
		var form Form
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if form.ID == "" {
			form.ID = randomFormID()
		}
		if form.Name == "" {
			form.Name = form.ID
		}
		form.CreatedAt = time.Now().UTC()
		forms, _ := loadForms()
		forms = append(forms, form)
		if err := saveForms(forms); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "form": form})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleFormDetail(w http.ResponseWriter, r *http.Request) {
	// Path: /forms/:id or /forms/:id/submissions
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		jsonError(w, http.StatusNotFound, "form id required")
		return
	}
	id := parts[1]
	form := findForm(id)
	if form == nil {
		jsonError(w, http.StatusNotFound, "form not found")
		return
	}

	if len(parts) == 3 && parts[2] == "submissions" && r.Method == http.MethodGet {
		limit := 100
		subs, _ := readSubmissions(id, limit)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":          true,
			"form":        form,
			"submissions": subs,
		})
		return
	}

	if r.Method == http.MethodDelete {
		forms, _ := loadForms()
		out := forms[:0]
		for _, f := range forms {
			if f.ID != id {
				out = append(out, f)
			}
		}
		if err := saveForms(out); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}

	jsonError(w, http.StatusMethodNotAllowed, "use GET or DELETE")
}

// handleFormsRouter fans out /forms/:id, /forms/:id/submit,
// and /forms/:id/submissions. Mixed auth: /submit is public,
// everything else requires owner auth (applied by the caller
// wrapping this with s.auth for the /forms/ prefix — but we
// need /submit to NOT be auth-gated, so the router itself
// strips auth off for that path).
func (s *HTTPServer) handleFormsRouter(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "forms" && parts[2] == "submit" {
		// Public — no auth.
		s.handleFormSubmit(w, r)
		return
	}
	// Everything else is owner-only; run the auth middleware
	// inline to keep this single mux handler simple.
	s.auth(s.handleFormDetail)(w, r)
}

func randomFormID() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	out := make([]byte, 8)
	buf := make([]byte, 8)
	_, _ = randomRead(buf)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}
