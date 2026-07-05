package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ops_mail.go — provider-neutral email verbs for MCP and constrained surfaces.
//
// The lower layer already normalizes Gmail and Microsoft 365 into MailMessage
// via FetchMail and sends through SendTransactionalEmail. These verbs expose a
// small surface that car/watch/TV/phone/MCP can safely use.

type mailSearchPayload struct {
	Provider     string `json:"provider,omitempty"` // auto | gmail | o365
	Folder       string `json:"folder,omitempty"`   // inbox | sent | all
	Query        string `json:"query,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	OnlyPersonal bool   `json:"onlyPersonal,omitempty"`
}

type mailUnreadPayload struct {
	Provider     string `json:"provider,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	OnlyPersonal bool   `json:"onlyPersonal,omitempty"`
}

type mailSendPayload struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	HTML    string   `json:"html,omitempty"`
	CC      []string `json:"cc,omitempty"`
	BCC     []string `json:"bcc,omitempty"`
	Execute bool     `json:"execute,omitempty"`
	Confirm string   `json:"confirm,omitempty"`
	Surface string   `json:"surface,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "mail_search",
		Description: "Search/fetch incoming email from Gmail or Microsoft 365 using the configured OAuth email connector. Payload {provider?, folder?, query?, limit?, onlyPersonal?}.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":     map[string]interface{}{"type": "string"},
				"folder":       map[string]interface{}{"type": "string"},
				"query":        map[string]interface{}{"type": "string"},
				"limit":        map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 100},
				"onlyPersonal": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler: mailSearchOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "mail_unread",
		Description: "Return a driving/watch-safe summary of recent inbox messages. Payload {provider?, limit?, onlyPersonal?}.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":     map[string]interface{}{"type": "string"},
				"limit":        map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 100},
				"onlyPersonal": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler: mailUnreadOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "mail_send",
		Description: "Send an email through Yaver's configured SMTP relay. Defaults to dry-run; execute=true requires confirm:'send'. Payload {to, subject, body, html?, cc?, bcc?, execute?, confirm?, surface?}.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"to", "subject", "body"},
			"properties": map[string]interface{}{
				"to":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"cc":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"bcc":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"subject": map[string]interface{}{"type": "string"},
				"body":    map[string]interface{}{"type": "string"},
				"html":    map[string]interface{}{"type": "string"},
				"execute": map[string]interface{}{"type": "boolean"},
				"confirm": map[string]interface{}{"type": "string"},
				"surface": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: mailSendOpsHandler,
	})
}

func mailSearchOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p mailSearchPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}
	if p.Limit > 100 {
		p.Limit = 100
	}
	msgs, err := FetchMail(MailFetchOptions{
		Provider:     normalizeMailProvider(p.Provider),
		Folder:       p.Folder,
		Query:        p.Query,
		Limit:        p.Limit,
		OnlyPersonal: p.OnlyPersonal,
	})
	if err != nil {
		return OpsResult{OK: false, Code: "mail_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"count":    len(msgs),
		"messages": mailSummaries(msgs),
	}}
}

func mailUnreadOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p mailUnreadPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}
	msgs, err := FetchMail(MailFetchOptions{
		Provider:     normalizeMailProvider(p.Provider),
		Folder:       "inbox",
		Query:        "is:unread",
		Limit:        p.Limit,
		OnlyPersonal: p.OnlyPersonal,
	})
	if err != nil {
		return OpsResult{OK: false, Code: "mail_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"count":    len(msgs),
		"messages": mailSummaries(msgs),
		"spoken":   mailSpokenSummary(msgs),
	}}
}

func mailSendOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	if len(payload) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "payload is required"}
	}
	var p mailSendPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if len(p.To) == 0 || strings.TrimSpace(p.Subject) == "" || strings.TrimSpace(p.Body) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "to, subject, and body are required"}
	}
	preview := map[string]interface{}{
		"to":      p.To,
		"cc":      p.CC,
		"bcc":     p.BCC,
		"subject": p.Subject,
		"body":    p.Body,
		"html":    p.HTML,
		"surface": normalizeMeetingSurface(p.Surface),
		"note":    "DRY-RUN by default. Re-call with execute:true and confirm:\"send\" to send.",
	}
	if !p.Execute {
		return OpsResult{OK: true, Initial: map[string]interface{}{"dryRun": true, "preview": preview}}
	}
	if !strings.EqualFold(strings.TrimSpace(p.Confirm), "send") {
		return OpsResult{OK: false, Code: "confirm_required", Error: "mail_send requires confirm:\"send\" when execute=true", Initial: preview}
	}
	res, err := SendTransactionalEmail(SendEmailRequest{
		To:       p.To,
		Cc:       p.CC,
		Bcc:      p.BCC,
		Subject:  p.Subject,
		Body:     p.Body,
		HTMLBody: p.HTML,
	})
	if err != nil {
		return OpsResult{OK: false, Code: "mail_error", Error: err.Error(), Initial: preview}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sent": true, "result": res}}
}

func normalizeMailProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", "auto":
		return "auto"
	case "google", "gmail":
		return "gmail"
	case "office365", "m365", "microsoft365", "microsoft", "o365", "outlook":
		return "o365"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func mailSummaries(msgs []MailMessage) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]interface{}{
			"id":             m.ID,
			"threadId":       m.ThreadID,
			"provider":       m.Provider,
			"from":           m.From,
			"fromName":       m.FromName,
			"subject":        m.Subject,
			"snippet":        m.Snippet,
			"date":           m.Date.UTC().Format(timeRFC3339OrEmpty(m.Date)),
			"classification": m.Classification,
			"score":          m.Score,
		})
	}
	return out
}

func mailSpokenSummary(msgs []MailMessage) string {
	if len(msgs) == 0 {
		return "No recent matching email."
	}
	first := msgs[0]
	from := first.FromName
	if from == "" {
		from = first.From
	}
	if len(msgs) == 1 {
		return fmt.Sprintf("One email from %s: %s.", from, first.Subject)
	}
	return fmt.Sprintf("%d emails. Latest from %s: %s.", len(msgs), from, first.Subject)
}

func timeRFC3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return time.RFC3339
}
