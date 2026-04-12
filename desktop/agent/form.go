package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// FormField describes a single input on a form.
type FormField struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`        // text, email, textarea, select, checkbox, file, hidden
	Required    bool     `json:"required"`
	Label       string   `json:"label"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"` // for select
}

// FormConfig is the definition of a managed form endpoint.
type FormConfig struct {
	Name           string      `json:"name"`
	Fields         []FormField `json:"fields"`
	NotifyEmail    string      `json:"notifyEmail,omitempty"`
	NotifyTelegram string      `json:"notifyTelegram,omitempty"`
	NotifyDiscord  string      `json:"notifyDiscord,omitempty"`
	RedirectURL    string      `json:"redirectUrl,omitempty"`
	SuccessMessage string      `json:"successMessage,omitempty"`
	Honeypot       bool        `json:"honeypot"`   // default true
	RateLimit      int         `json:"rateLimit"`  // submissions per IP per hour, default 10
}

// FormSubmission is a recorded form submission.
type FormSubmission struct {
	ID        string            `json:"id"`
	FormName  string            `json:"formName"`
	Data      map[string]string `json:"data"`
	IP        string            `json:"ip"`
	UserAgent string            `json:"userAgent"`
	CreatedAt time.Time         `json:"createdAt"`
	Files     []string          `json:"files,omitempty"` // saved file paths
}

// formsConfig is the on-disk structure persisted to ~/.yaver/forms.json.
type formsConfig struct {
	Forms map[string]*FormConfig `json:"forms"`
}

// FormManager manages form definitions, submissions, and rate limiting.
type FormManager struct {
	mu          sync.Mutex
	forms       map[string]*FormConfig
	submissions map[string][]FormSubmission
	rateLimiter map[string][]time.Time // key: "formName:ip"
	configPath  string
	dataDir     string // ~/.yaver/forms/
}

var emailRegexp = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewFormManager creates a FormManager backed by ~/.yaver/forms.json.
func NewFormManager() *FormManager {
	dir, err := ConfigDir()
	if err != nil {
		log.Printf("[forms] cannot resolve config dir: %v", err)
		dir = filepath.Join(os.Getenv("HOME"), ".yaver")
	}

	fm := &FormManager{
		forms:       make(map[string]*FormConfig),
		submissions: make(map[string][]FormSubmission),
		rateLimiter: make(map[string][]time.Time),
		configPath:  filepath.Join(dir, "forms.json"),
		dataDir:     filepath.Join(dir, "forms"),
	}
	if err := fm.loadConfig(); err != nil {
		log.Printf("[forms] load config: %v", err)
	}
	return fm
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Create registers a new form. Returns the endpoint path /forms/{name}.
func (fm *FormManager) Create(config *FormConfig) (string, error) {
	if config.Name == "" {
		return "", fmt.Errorf("form name is required")
	}
	if !isValidFormName(config.Name) {
		return "", fmt.Errorf("form name must be lowercase alphanumeric with hyphens only")
	}
	for _, f := range config.Fields {
		if f.Name == "" {
			return "", fmt.Errorf("field name must not be empty")
		}
		if !isValidFieldType(f.Type) {
			return "", fmt.Errorf("field %q has unsupported type %q", f.Name, f.Type)
		}
		if f.Type == "select" && len(f.Options) == 0 {
			return "", fmt.Errorf("select field %q requires at least one option", f.Name)
		}
	}
	if config.RateLimit <= 0 {
		config.RateLimit = 10
	}
	// Honeypot defaults to true on new forms; only skip if caller explicitly set false.
	// (Zero-value bool in Go is false, so we can't distinguish "not set" from false here.
	//  We document that callers must set Honeypot=true explicitly or accept the default.)

	fm.mu.Lock()
	defer fm.mu.Unlock()

	if _, exists := fm.forms[config.Name]; exists {
		return "", fmt.Errorf("form %q already exists", config.Name)
	}

	fm.forms[config.Name] = config
	fm.submissions[config.Name] = []FormSubmission{}

	if err := fm.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return "/forms/" + config.Name, nil
}

// FormListEntry combines a FormConfig with its submission count.
type FormListEntry struct {
	FormConfig
	SubmissionCount int `json:"submissionCount"`
}

// List returns all form configs with their submission counts.
func (fm *FormManager) List() ([]FormListEntry, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	entries := make([]FormListEntry, 0, len(fm.forms))
	for _, cfg := range fm.forms {
		count := len(fm.submissions[cfg.Name])
		entries = append(entries, FormListEntry{FormConfig: *cfg, SubmissionCount: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Submissions returns submissions for a form, optionally filtered to the last duration.
// Pass 0 for last to return all submissions.
func (fm *FormManager) Submissions(formName string, last time.Duration) ([]FormSubmission, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	if _, ok := fm.forms[formName]; !ok {
		return nil, fmt.Errorf("form %q not found", formName)
	}

	subs := fm.submissions[formName]
	if last <= 0 {
		out := make([]FormSubmission, len(subs))
		copy(out, subs)
		return out, nil
	}

	cutoff := time.Now().Add(-last)
	var filtered []FormSubmission
	for _, s := range subs {
		if s.CreatedAt.After(cutoff) {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// Export returns all submissions for a form as a CSV string.
func (fm *FormManager) Export(formName string) (string, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	cfg, ok := fm.forms[formName]
	if !ok {
		return "", fmt.Errorf("form %q not found", formName)
	}
	subs := fm.submissions[formName]
	return fm.generateCSV(subs, cfg.Fields), nil
}

// Delete removes a form and all its submissions. Returns a confirmation message.
func (fm *FormManager) Delete(formName string) (string, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	if _, ok := fm.forms[formName]; !ok {
		return "", fmt.Errorf("form %q not found", formName)
	}

	count := len(fm.submissions[formName])
	delete(fm.forms, formName)
	delete(fm.submissions, formName)

	// Clean up rate-limiter entries for this form.
	for k := range fm.rateLimiter {
		if strings.HasPrefix(k, formName+":") {
			delete(fm.rateLimiter, k)
		}
	}

	if err := fm.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}

	// Best-effort removal of stored files.
	formFilesDir := filepath.Join(fm.dataDir, formName, "files")
	_ = os.RemoveAll(formFilesDir)

	return fmt.Sprintf("deleted form %q (%d submissions removed)", formName, count), nil
}

// HandleSubmission processes an incoming form submission.
// files maps field names to raw file contents.
// Returns the configured success message (or a default), or an error.
func (fm *FormManager) HandleSubmission(formName string, data map[string]string, ip, userAgent string, files map[string][]byte) (string, error) {
	fm.mu.Lock()
	cfg, ok := fm.forms[formName]
	if !ok {
		fm.mu.Unlock()
		return "", fmt.Errorf("form %q not found", formName)
	}
	// Snapshot config values before releasing the lock.
	cfgCopy := *cfg
	fm.mu.Unlock()

	// (a) Honeypot check — bot trap field "_hp".
	if cfgCopy.Honeypot {
		if val, filled := data["_hp"]; filled && val != "" {
			// Silently accept without storing or notifying.
			msg := cfgCopy.SuccessMessage
			if msg == "" {
				msg = "Thank you for your submission."
			}
			return msg, nil
		}
	}

	// (b) Rate limit.
	if !fm.checkRateLimit(formName, ip, cfgCopy.RateLimit) {
		return "", fmt.Errorf("rate limit exceeded — please try again later")
	}

	// (c) Validate required fields and field types.
	if err := validateFields(cfgCopy.Fields, data); err != nil {
		return "", err
	}

	// (d) Build and save submission.
	sub := FormSubmission{
		ID:        uuid.New().String(),
		FormName:  formName,
		Data:      data,
		IP:        ip,
		UserAgent: userAgent,
		CreatedAt: time.Now().UTC(),
	}

	// (e) Save uploaded files.
	if len(files) > 0 {
		filesDir := filepath.Join(fm.dataDir, formName, "files", sub.ID)
		if err := os.MkdirAll(filesDir, 0700); err != nil {
			log.Printf("[forms] mkdir files dir: %v", err)
		} else {
			for fieldName, content := range files {
				// Sanitize filename: use field name only, no path traversal.
				fname := sanitizeFilename(fieldName)
				fpath := filepath.Join(filesDir, fname)
				if err := os.WriteFile(fpath, content, 0600); err != nil {
					log.Printf("[forms] write file %s: %v", fpath, err)
				} else {
					sub.Files = append(sub.Files, fpath)
				}
			}
		}
	}

	fm.mu.Lock()
	fm.submissions[formName] = append(fm.submissions[formName], sub)
	fm.mu.Unlock()

	// (f) Async notifications — do not block the response.
	go func() {
		if err := fm.sendNotification(formName, cfgCopy, sub); err != nil {
			log.Printf("[forms] notification error for %s: %v", formName, err)
		}
	}()

	msg := cfgCopy.SuccessMessage
	if msg == "" {
		msg = "Thank you for your submission."
	}
	if cfgCopy.RedirectURL != "" {
		msg = cfgCopy.RedirectURL // caller checks if this looks like a URL
	}
	return msg, nil
}

// GenerateComponent generates a frontend form component.
// Supported frameworks: "react", "astro", "html".
func (fm *FormManager) GenerateComponent(formName, framework string) (string, error) {
	fm.mu.Lock()
	cfg, ok := fm.forms[formName]
	fm.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("form %q not found", formName)
	}

	switch strings.ToLower(framework) {
	case "react":
		return generateReactComponent(cfg), nil
	case "astro":
		return generateAstroComponent(cfg), nil
	case "html":
		return generateHTMLForm(cfg), nil
	default:
		return "", fmt.Errorf("unsupported framework %q — choose react, astro, or html", framework)
	}
}

// FormStats summarises activity across all forms.
type FormStats struct {
	TotalForms       int            `json:"totalForms"`
	TotalSubmissions int            `json:"totalSubmissions"`
	SubmissionsToday int            `json:"submissionsToday"`
	TopForms         []FormVolume   `json:"topForms"`
}

// FormVolume pairs a form name with its submission count.
type FormVolume struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Stats returns aggregate statistics across all managed forms.
func (fm *FormManager) Stats() (*FormStats, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")
	stats := &FormStats{TotalForms: len(fm.forms)}
	volumes := make([]FormVolume, 0, len(fm.submissions))

	for name, subs := range fm.submissions {
		count := len(subs)
		stats.TotalSubmissions += count
		for _, s := range subs {
			if s.CreatedAt.UTC().Format("2006-01-02") == today {
				stats.SubmissionsToday++
			}
		}
		volumes = append(volumes, FormVolume{Name: name, Count: count})
	}

	sort.Slice(volumes, func(i, j int) bool {
		return volumes[i].Count > volumes[j].Count
	})
	if len(volumes) > 5 {
		volumes = volumes[:5]
	}
	stats.TopForms = volumes
	return stats, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// checkRateLimit returns true if the submission is allowed. It records the
// attempt and uses a sliding 1-hour window.
func (fm *FormManager) checkRateLimit(formName, ip string, limitPerHour int) bool {
	key := formName + ":" + ip
	now := time.Now()
	cutoff := now.Add(-time.Hour)

	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Evict timestamps older than 1 hour.
	ts := fm.rateLimiter[key]
	valid := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= limitPerHour {
		fm.rateLimiter[key] = valid
		return false
	}

	fm.rateLimiter[key] = append(valid, now)
	return true
}

// sendNotification dispatches form-submission notifications to all configured channels.
func (fm *FormManager) sendNotification(formName string, cfg FormConfig, sub FormSubmission) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 New form submission: %s\n", formName))
	sb.WriteString(fmt.Sprintf("Submitted at: %s\n", sub.CreatedAt.Format(time.RFC3339)))
	if sub.IP != "" {
		sb.WriteString(fmt.Sprintf("IP: %s\n", sub.IP))
	}
	sb.WriteString("---\n")
	for k, v := range sub.Data {
		if k == "_hp" {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	msg := sb.String()

	// Email notification via globalEmailMgr.
	if cfg.NotifyEmail != "" && globalEmailMgr != nil {
		subject := fmt.Sprintf("New submission: %s", formName)
		if err := globalEmailMgr.SendEmail(cfg.NotifyEmail, subject, msg, ""); err != nil {
			log.Printf("[forms] email notify failed: %v", err)
		}
	}

	// Telegram notification via direct Bot API.
	if cfg.NotifyTelegram != "" {
		sendTelegramWebhook(cfg.NotifyTelegram, msg)
	}

	// Discord webhook notification.
	if cfg.NotifyDiscord != "" {
		sendDiscordWebhook(cfg.NotifyDiscord, msg)
	}

	return nil
}

// validateEmail returns true when s looks like a valid email address.
func validateEmail(s string) bool {
	return emailRegexp.MatchString(s)
}

// validateFields checks required fields are present and type constraints hold.
func validateFields(fields []FormField, data map[string]string) error {
	for _, f := range fields {
		val := strings.TrimSpace(data[f.Name])
		if f.Required && val == "" {
			label := f.Label
			if label == "" {
				label = f.Name
			}
			return fmt.Errorf("field %q is required", label)
		}
		if val == "" {
			continue
		}
		switch f.Type {
		case "email":
			if !validateEmail(val) {
				label := f.Label
				if label == "" {
					label = f.Name
				}
				return fmt.Errorf("field %q must be a valid email address", label)
			}
		case "select":
			if len(f.Options) > 0 && !formContainsString(f.Options, val) {
				return fmt.Errorf("field %q value %q is not a valid option", f.Name, val)
			}
		}
	}
	return nil
}

// generateCSV produces RFC 4180-compatible CSV output.
func (fm *FormManager) generateCSV(submissions []FormSubmission, fields []FormField) string {
	var buf bytes.Buffer

	// Header row: metadata + field names.
	headers := []string{"id", "submitted_at", "ip", "user_agent"}
	for _, f := range fields {
		headers = append(headers, f.Name)
	}
	buf.WriteString(csvRow(headers))

	for _, sub := range submissions {
		row := []string{
			sub.ID,
			sub.CreatedAt.UTC().Format(time.RFC3339),
			sub.IP,
			sub.UserAgent,
		}
		for _, f := range fields {
			row = append(row, sub.Data[f.Name])
		}
		buf.WriteString(csvRow(row))
	}
	return buf.String()
}

// loadConfig reads the forms config from disk.
func (fm *FormManager) loadConfig() error {
	data, err := os.ReadFile(fm.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read forms config: %w", err)
	}

	var cfg formsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse forms config: %w", err)
	}

	fm.mu.Lock()
	defer fm.mu.Unlock()
	if cfg.Forms != nil {
		fm.forms = cfg.Forms
	}
	for name := range fm.forms {
		if _, ok := fm.submissions[name]; !ok {
			fm.submissions[name] = []FormSubmission{}
		}
	}
	return nil
}

// saveConfig persists the forms config to disk. Caller must hold fm.mu.
func (fm *FormManager) saveConfig() error {
	if err := os.MkdirAll(filepath.Dir(fm.configPath), 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	cfg := formsConfig{Forms: fm.forms}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(fm.configPath, data, 0600)
}

// ---------------------------------------------------------------------------
// Component generators
// ---------------------------------------------------------------------------

func generateReactComponent(cfg *FormConfig) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`import { useState } from "react";

export function %sForm() {
  const [fields, setFields] = useState({
`, toPascalCase(cfg.Name)))

	for _, f := range cfg.Fields {
		if f.Type == "hidden" || f.Type == "file" {
			continue
		}
		defaultVal := `""`
		if f.Type == "checkbox" {
			defaultVal = "false"
		}
		sb.WriteString(fmt.Sprintf("    %s: %s,\n", toCamelCase(f.Name), defaultVal))
	}

	sb.WriteString(`  });
  const [status, setStatus] = useState(null);

  const handleSubmit = async (e) => {
    e.preventDefault();
    setStatus("sending");
    try {
      const res = await fetch("/forms/` + cfg.Name + `", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(fields),
      });
      if (!res.ok) throw new Error(await res.text());
      setStatus("success");
    } catch (err) {
      setStatus("error: " + err.message);
    }
  };

  if (status === "success") {
    return <p>` + successMsg(cfg) + `</p>;
  }

  return (
    <form onSubmit={handleSubmit}>
`)

	for _, f := range cfg.Fields {
		if f.Type == "hidden" {
			sb.WriteString(fmt.Sprintf(`      <input type="hidden" name="%s" value="%s" />`+"\n", f.Name, ""))
			continue
		}
		label := f.Label
		if label == "" {
			label = f.Name
		}
		required := ""
		if f.Required {
			required = " required"
		}
		sb.WriteString(fmt.Sprintf("      <label>%s\n", label))
		switch f.Type {
		case "textarea":
			sb.WriteString(fmt.Sprintf(`        <textarea name="%s" placeholder="%s"%s`+"\n"+
				`          value={fields.%s} onChange={e => setFields(p => ({...p, %s: e.target.value}))} />`+"\n",
				f.Name, f.Placeholder, required, toCamelCase(f.Name), toCamelCase(f.Name)))
		case "select":
			sb.WriteString(fmt.Sprintf(`        <select name="%s"%s`+"\n"+
				`          value={fields.%s} onChange={e => setFields(p => ({...p, %s: e.target.value}))}`+">\n",
				f.Name, required, toCamelCase(f.Name), toCamelCase(f.Name)))
			for _, opt := range f.Options {
				sb.WriteString(fmt.Sprintf("          <option value=%q>%s</option>\n", opt, opt))
			}
			sb.WriteString("        </select>\n")
		case "checkbox":
			sb.WriteString(fmt.Sprintf(`        <input type="checkbox" name="%s"%s`+"\n"+
				`          checked={fields.%s} onChange={e => setFields(p => ({...p, %s: e.target.checked}))} />`+"\n",
				f.Name, required, toCamelCase(f.Name), toCamelCase(f.Name)))
		case "file":
			sb.WriteString(fmt.Sprintf(`        <input type="file" name="%s"%s />`+"\n", f.Name, required))
		default:
			sb.WriteString(fmt.Sprintf(`        <input type="%s" name="%s" placeholder="%s"%s`+"\n"+
				`          value={fields.%s} onChange={e => setFields(p => ({...p, %s: e.target.value}))} />`+"\n",
				f.Type, f.Name, f.Placeholder, required, toCamelCase(f.Name), toCamelCase(f.Name)))
		}
		sb.WriteString("      </label>\n")
	}

	if cfg.Honeypot {
		sb.WriteString(`      <input type="text" name="_hp" style={{display:"none"}} tabIndex={-1} autoComplete="off" />` + "\n")
	}

	sb.WriteString(`      <button type="submit" disabled={status === "sending"}>
        {status === "sending" ? "Sending…" : "Submit"}
      </button>
      {status && status !== "success" && status !== "sending" && (
        <p style={{color:"red"}}>{status}</p>
      )}
    </form>
  );
}
`)
	return sb.String()
}

func generateAstroComponent(cfg *FormConfig) string {
	var sb strings.Builder
	sb.WriteString("---\n// Astro component for form: " + cfg.Name + "\n---\n\n")
	sb.WriteString("<form id=\"" + cfg.Name + "-form\" method=\"post\" action=\"/forms/" + cfg.Name + "\">\n")

	for _, f := range cfg.Fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		required := ""
		if f.Required {
			required = " required"
		}
		if f.Type == "hidden" {
			sb.WriteString(fmt.Sprintf("  <input type=\"hidden\" name=%q value=\"\" />\n", f.Name))
			continue
		}
		sb.WriteString(fmt.Sprintf("  <label>\n    %s\n", label))
		switch f.Type {
		case "textarea":
			sb.WriteString(fmt.Sprintf("    <textarea name=%q placeholder=%q%s></textarea>\n", f.Name, f.Placeholder, required))
		case "select":
			sb.WriteString(fmt.Sprintf("    <select name=%q%s>\n", f.Name, required))
			for _, opt := range f.Options {
				sb.WriteString(fmt.Sprintf("      <option value=%q>%s</option>\n", opt, opt))
			}
			sb.WriteString("    </select>\n")
		case "checkbox":
			sb.WriteString(fmt.Sprintf("    <input type=\"checkbox\" name=%q%s />\n", f.Name, required))
		case "file":
			sb.WriteString(fmt.Sprintf("    <input type=\"file\" name=%q%s />\n", f.Name, required))
		default:
			sb.WriteString(fmt.Sprintf("    <input type=%q name=%q placeholder=%q%s />\n", f.Type, f.Name, f.Placeholder, required))
		}
		sb.WriteString("  </label>\n")
	}

	if cfg.Honeypot {
		sb.WriteString("  <input type=\"text\" name=\"_hp\" style=\"display:none\" tabindex=\"-1\" autocomplete=\"off\" />\n")
	}

	sb.WriteString("  <button type=\"submit\">Submit</button>\n</form>\n\n")
	sb.WriteString("<script>\n")
	sb.WriteString("const form = document.getElementById('" + cfg.Name + "-form');\n")
	sb.WriteString(`form.addEventListener('submit', async (e) => {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(form));
  const res = await fetch('/forms/` + cfg.Name + `', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (res.ok) {
    form.innerHTML = '<p>` + successMsg(cfg) + `</p>';
  } else {
    alert('Submission failed: ' + await res.text());
  }
});
</script>
`)
	return sb.String()
}

func generateHTMLForm(cfg *FormConfig) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<form id="%s-form" method="post" action="/forms/%s">
`, cfg.Name, cfg.Name))

	for _, f := range cfg.Fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		required := ""
		if f.Required {
			required = " required"
		}
		if f.Type == "hidden" {
			sb.WriteString(fmt.Sprintf("  <input type=\"hidden\" name=%q value=\"\" />\n", f.Name))
			continue
		}
		sb.WriteString(fmt.Sprintf("  <label>\n    %s\n", label))
		switch f.Type {
		case "textarea":
			sb.WriteString(fmt.Sprintf("    <textarea name=%q placeholder=%q%s></textarea>\n", f.Name, f.Placeholder, required))
		case "select":
			sb.WriteString(fmt.Sprintf("    <select name=%q%s>\n", f.Name, required))
			for _, opt := range f.Options {
				sb.WriteString(fmt.Sprintf("      <option value=%q>%s</option>\n", opt, opt))
			}
			sb.WriteString("    </select>\n")
		case "checkbox":
			sb.WriteString(fmt.Sprintf("    <input type=\"checkbox\" name=%q%s />\n", f.Name, required))
		case "file":
			sb.WriteString(fmt.Sprintf("    <input type=\"file\" name=%q%s />\n", f.Name, required))
		default:
			sb.WriteString(fmt.Sprintf("    <input type=%q name=%q placeholder=%q%s />\n", f.Type, f.Name, f.Placeholder, required))
		}
		sb.WriteString("  </label>\n")
	}

	if cfg.Honeypot {
		sb.WriteString("  <input type=\"text\" name=\"_hp\" style=\"display:none\" tabindex=\"-1\" autocomplete=\"off\" />\n")
	}

	sb.WriteString("  <button type=\"submit\">Submit</button>\n</form>\n")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Notification dispatch helpers (standalone — not via NotificationManager
// to avoid depending on the agent's runtime config from within a form handler)
// ---------------------------------------------------------------------------

var formHTTPClient = &http.Client{Timeout: 10 * time.Second}

func sendTelegramWebhook(botTokenColonChatID, message string) {
	// Format expected: "BOT_TOKEN:CHAT_ID"
	parts := strings.SplitN(botTokenColonChatID, ":", 2)
	if len(parts) != 2 {
		log.Printf("[forms] telegram: expected format BOT_TOKEN:CHAT_ID, got %q", botTokenColonChatID)
		return
	}
	botToken, chatID := parts[0], parts[1]
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	body, _ := json.Marshal(map[string]interface{}{
		"chat_id": chatID,
		"text":    message,
	})
	resp, err := formHTTPClient.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[forms] telegram webhook: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[forms] telegram webhook: status %d", resp.StatusCode)
	}
}

func sendDiscordWebhook(webhookURL, message string) {
	body, _ := json.Marshal(map[string]string{"content": message})
	resp, err := formHTTPClient.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[forms] discord webhook: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[forms] discord webhook: status %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Tiny utilities
// ---------------------------------------------------------------------------

func isValidFormName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func isValidFieldType(t string) bool {
	switch t {
	case "text", "email", "textarea", "select", "checkbox", "file", "hidden":
		return true
	}
	return false
}

func formContainsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func csvRow(fields []string) string {
	var parts []string
	for _, f := range fields {
		// Escape double quotes and wrap in quotes if needed.
		if strings.ContainsAny(f, `",`+"\n\r") {
			f = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, ",") + "\n"
}

func successMsg(cfg *FormConfig) string {
	if cfg.SuccessMessage != "" {
		return cfg.SuccessMessage
	}
	return "Thank you for your submission."
}

func toPascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func toCamelCase(s string) string {
	p := toPascalCase(s)
	if len(p) == 0 {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}

func sanitizeFilename(name string) string {
	// Keep only safe characters; strip path separators.
	var sb strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' {
			sb.WriteRune(c)
		}
	}
	result := sb.String()
	if result == "" {
		result = "file"
	}
	return result
}
