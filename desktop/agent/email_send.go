package main

// email_send.go — transactional outbound email via an SMTP
// relay the dev configures themselves. Alternative to Resend /
// Postmark / SendGrid for the solo-SaaS case where the dev just
// needs "password reset" + "order confirmation" style mail and
// doesn't want a $20/mo subscription for it.
//
// Architecture: one TransactionalMailer struct backed by Go's
// standard net/smtp. STARTTLS on 587 is the default (matches
// Gmail, Fastmail, Mailgun, AWS SES, Postmark). Plain auth is
// the only mechanism we support — the dev's own relay is the
// trust boundary, and every modern SMTP host requires TLS
// anyway.
//
// Storage: the SMTP credentials live in ~/.yaver/config.json
// under EmailConfig.SMTP*. The send history lives in
// ~/.yaver/email_sent.jsonl (rotating at 4 MB) so the dev can
// `yaver email sent tail` or grep for a specific recipient
// during debugging.

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SendEmailRequest is the shape the CLI + HTTP + MCP entry
// points converge on.
type SendEmailRequest struct {
	To        []string          `json:"to"`
	Cc        []string          `json:"cc,omitempty"`
	Bcc       []string          `json:"bcc,omitempty"`
	Subject   string            `json:"subject"`
	Body      string            `json:"body,omitempty"`     // plain text
	HTMLBody  string            `json:"htmlBody,omitempty"` // optional HTML alternative
	From      string            `json:"from,omitempty"`     // overrides cfg.SMTPFrom
	ReplyTo   string            `json:"replyTo,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"` // extra custom headers
	RequestID string            `json:"requestId,omitempty"` // for dedup / idempotency
}

// SendEmailResult is recorded in the sent-history ledger.
type SendEmailResult struct {
	MessageID  string   `json:"messageId"`
	SentAt     string   `json:"sentAt"`
	To         []string `json:"to"`
	Subject    string   `json:"subject"`
	RequestID  string   `json:"requestId,omitempty"`
	Err        string   `json:"err,omitempty"`
	DurationMs int64    `json:"durationMs"`
}

var transactionalMu sync.Mutex

// SendTransactionalEmail is the one entry point all three
// surfaces (CLI, HTTP, MCP) call. Returns the recorded result
// so the caller can surface the messageId / error upstream.
func SendTransactionalEmail(req SendEmailRequest) (*SendEmailResult, error) {
	if len(req.To) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}
	if strings.TrimSpace(req.Subject) == "" {
		return nil, fmt.Errorf("subject is required")
	}

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.Email == nil {
		return nil, fmt.Errorf("email not configured — run `yaver email config smtp ...`")
	}
	if cfg.Email.SMTPHost == "" {
		return nil, fmt.Errorf("SMTP host missing from config")
	}

	from := req.From
	if from == "" {
		from = cfg.Email.SMTPFrom
	}
	if from == "" {
		return nil, fmt.Errorf("no from address — set smtp_from in config or pass --from")
	}

	start := time.Now()
	msg := buildMIMEMessage(from, req)
	recipients := append([]string{}, req.To...)
	recipients = append(recipients, req.Cc...)
	recipients = append(recipients, req.Bcc...)

	err = dialAndSendSMTP(cfg.Email, from, recipients, msg)
	result := &SendEmailResult{
		MessageID:  extractMessageID(msg),
		SentAt:     time.Now().UTC().Format(time.RFC3339),
		To:         req.To,
		Subject:    req.Subject,
		RequestID:  req.RequestID,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		result.Err = err.Error()
	}
	_ = appendSentHistory(result)
	if err != nil {
		return result, err
	}
	return result, nil
}

// dialAndSendSMTP handles the actual wire — STARTTLS when
// configured, plain auth, envelope recipients.
func dialAndSendSMTP(cfg *EmailConfig, from string, to []string, msg []byte) error {
	port := cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, port)
	host, _, _ := net.SplitHostPort(addr)

	// Dial + greet.
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer client.Close()

	// Server greeting — identify ourselves.
	if err := client.Hello("yaver-agent"); err != nil {
		return fmt.Errorf("smtp HELO: %w", err)
	}

	// STARTTLS upgrade (most 587 relays require this).
	if cfg.SMTPStartTLS || port == 587 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			tlsCfg := &tls.Config{ServerName: host}
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		}
	}

	// AUTH PLAIN when credentials are configured.
	if cfg.SMTPUsername != "" {
		auth := smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	// Envelope.
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	// Message body.
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := writer.Write(msg); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}

	return client.Quit()
}

// buildMIMEMessage composes an RFC 5322 message body. Supports
// text/plain, text/html, and multipart/alternative when both
// plain + HTML bodies are given.
func buildMIMEMessage(from string, req SendEmailRequest) []byte {
	var sb strings.Builder
	messageID := fmt.Sprintf("<%d.%s@yaver>", time.Now().UnixNano(), randomBlobID())

	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(req.To, ", ") + "\r\n")
	if len(req.Cc) > 0 {
		sb.WriteString("Cc: " + strings.Join(req.Cc, ", ") + "\r\n")
	}
	if req.ReplyTo != "" {
		sb.WriteString("Reply-To: " + req.ReplyTo + "\r\n")
	}
	sb.WriteString("Subject: " + req.Subject + "\r\n")
	sb.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	sb.WriteString("Message-ID: " + messageID + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("X-Yaver-Ingest: transactional\r\n")
	for k, v := range req.Headers {
		sb.WriteString(k + ": " + v + "\r\n")
	}

	if req.HTMLBody != "" && req.Body != "" {
		boundary := "yaver_" + randomBlobID()
		sb.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
		sb.WriteString("\r\n")
		sb.WriteString("--" + boundary + "\r\n")
		sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		sb.WriteString(req.Body + "\r\n")
		sb.WriteString("--" + boundary + "\r\n")
		sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		sb.WriteString(req.HTMLBody + "\r\n")
		sb.WriteString("--" + boundary + "--\r\n")
	} else if req.HTMLBody != "" {
		sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		sb.WriteString(req.HTMLBody)
	} else {
		sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		sb.WriteString(req.Body)
	}

	return []byte(sb.String())
}

// extractMessageID pulls the Message-ID header out of a composed
// message body. Used by the sent-history ledger so the dev can
// correlate a send with an SMTP server log entry.
func extractMessageID(msg []byte) string {
	content := string(msg)
	idx := strings.Index(content, "Message-ID: ")
	if idx < 0 {
		return ""
	}
	rest := content[idx+len("Message-ID: "):]
	end := strings.Index(rest, "\r\n")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// randomBlobID is a small, url-safe opaque token used for
// message boundaries and Message-ID suffixes. Shares a helper
// name space with blobs.go but stays intentionally tiny; if
// either file gains more callers we can lift into a util
// package.
func randomBlobID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xffffff)
}

// appendSentHistory records one send attempt to the rolling
// ledger at ~/.yaver/email_sent.jsonl. Rotates at 4 MB so the
// file never grows without bound.
func appendSentHistory(result *SendEmailResult) error {
	transactionalMu.Lock()
	defer transactionalMu.Unlock()

	base, err := ConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(base, "email_sent.jsonl")
	if info, serr := os.Stat(path); serr == nil && info.Size() > 4*1024*1024 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	data, jerr := json.Marshal(result)
	if jerr != nil {
		return jerr
	}
	f.Write(data)
	f.Write([]byte{'\n'})
	return nil
}

// readSentHistory returns the most recent N sent-record entries.
// Used by `yaver email sent tail`.
func readSentHistory(limit int) []SendEmailResult {
	base, err := ConfigDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(base, "email_sent.jsonl"))
	if err != nil {
		return nil
	}
	var out []SendEmailResult
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec SendEmailResult
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			out = append(out, rec)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// --- CLI subcommand handlers (wired into runEmail in main.go) ------------

func emailSendCmd(args []string) {
	req := SendEmailRequest{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func() string {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "missing value for %s\n", a)
				os.Exit(1)
			}
			i++
			return args[i]
		}
		switch a {
		case "--to":
			req.To = append(req.To, take())
		case "--cc":
			req.Cc = append(req.Cc, take())
		case "--bcc":
			req.Bcc = append(req.Bcc, take())
		case "--subject":
			req.Subject = take()
		case "--body":
			req.Body = take()
		case "--html":
			req.HTMLBody = take()
		case "--from":
			req.From = take()
		case "--reply-to":
			req.ReplyTo = take()
		}
	}
	if req.Body == "" && req.HTMLBody == "" {
		// Accept body via stdin — handy for pipes.
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			buf := make([]byte, 1<<20)
			n, _ := os.Stdin.Read(buf)
			req.Body = strings.TrimRight(string(buf[:n]), "\n")
		}
	}
	res, err := SendTransactionalEmail(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ sent %s to %s (%dms)\n", res.MessageID, strings.Join(res.To, ", "), res.DurationMs)
}

func emailConfigCmd(args []string) {
	if len(args) == 0 || args[0] == "show" {
		cfg, _ := LoadConfig()
		if cfg == nil || cfg.Email == nil || cfg.Email.SMTPHost == "" {
			fmt.Println("(no SMTP transactional config)")
			return
		}
		fmt.Printf("host:     %s\n", cfg.Email.SMTPHost)
		fmt.Printf("port:     %d\n", cfg.Email.SMTPPort)
		fmt.Printf("user:     %s\n", cfg.Email.SMTPUsername)
		fmt.Printf("password: %s\n", mask(cfg.Email.SMTPPassword))
		fmt.Printf("from:     %s\n", cfg.Email.SMTPFrom)
		fmt.Printf("starttls: %v\n", cfg.Email.SMTPStartTLS)
		return
	}
	if args[0] != "smtp" {
		fmt.Fprintln(os.Stderr, "usage: yaver email config smtp --host <h> --port <p> --user <u> --pass <p> --from <addr>")
		os.Exit(1)
	}
	cfg, _ := LoadConfig()
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Email == nil {
		cfg.Email = &EmailConfig{}
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		take := func() string {
			if i+1 >= len(rest) {
				return ""
			}
			i++
			return rest[i]
		}
		switch a {
		case "--host":
			cfg.Email.SMTPHost = take()
		case "--port":
			var p int
			fmt.Sscanf(take(), "%d", &p)
			cfg.Email.SMTPPort = p
		case "--user":
			cfg.Email.SMTPUsername = take()
		case "--pass":
			cfg.Email.SMTPPassword = take()
		case "--from":
			cfg.Email.SMTPFrom = take()
		case "--starttls":
			cfg.Email.SMTPStartTLS = true
		}
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ SMTP config saved")
}

func emailSentCmd(args []string) {
	limit := 20
	if len(args) >= 1 {
		fmt.Sscanf(args[0], "%d", &limit)
	}
	recs := readSentHistory(limit)
	if len(recs) == 0 {
		fmt.Println("(no sent history)")
		return
	}
	for _, r := range recs {
		status := "✓"
		if r.Err != "" {
			status = "✗"
		}
		fmt.Printf("%s %s  %s  %s\n", status, r.SentAt, strings.Join(r.To, ","), r.Subject)
		if r.Err != "" {
			fmt.Printf("   error: %s\n", r.Err)
		}
	}
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "••••"
	}
	return s[:2] + strings.Repeat("•", len(s)-4) + s[len(s)-2:]
}

// --- HTTP ----------------------------------------------------------------

// handleEmailSend POST /email/send — auth'd. The SDK can call
// this from a server-side surface that already authenticates
// with a yaver token, e.g. a Node backend orchestrating the
// send from the dev's own server.
func (s *HTTPServer) handleEmailSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req SendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res, err := SendTransactionalEmail(req)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "result": res})
}

// handleEmailSent GET /email/sent?limit=N — read the ledger.
func (s *HTTPServer) handleEmailSent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	recs := readSentHistory(limit)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"sent":   recs,
		"source": "local",
	})
}
