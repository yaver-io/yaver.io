package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// EmailSummary is a lightweight representation of an email for list views.
type EmailSummary struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	SenderEmail string `json:"senderEmail"`
	SenderName  string `json:"senderName"`
	BodyPreview string `json:"bodyPreview"`
	ReceivedAt  string `json:"receivedAt"`
	IsRead      bool   `json:"isRead"`
	Folder      string `json:"folder"`
}

// EmailDetail extends EmailSummary with the full message body and metadata.
type EmailDetail struct {
	EmailSummary
	Body           string   `json:"body"`
	Recipients     []string `json:"recipients"`
	HasAttachments bool     `json:"hasAttachments"`
	Summary        string   `json:"summary,omitempty"`
	Category       string   `json:"category,omitempty"`
}

// EmailProvider is the abstraction over Office 365 and Gmail.
type EmailProvider interface {
	// FetchInbox returns emails from the given folder (e.g. "inbox", "sentitems").
	FetchInbox(folder string, limit int) ([]EmailDetail, error)
	// SearchEmails performs a provider-specific search.
	SearchEmails(query string, limit int) ([]EmailDetail, error)
	// GetEmail retrieves a single email by ID.
	GetEmail(emailID string) (*EmailDetail, error)
	// SendEmail sends a new email.
	SendEmail(to, subject, body, cc string) error
	// ReplyToEmail replies to an existing email thread.
	ReplyToEmail(emailID, body string) error
}

// ---------------------------------------------------------------------------
// Office 365 provider
// ---------------------------------------------------------------------------

type office365Provider struct {
	cfg         *EmailConfig
	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func newOffice365Provider(cfg *EmailConfig) *office365Provider {
	return &office365Provider{cfg: cfg}
}

// ensureToken returns a valid access token, refreshing if necessary.
func (p *office365Provider) ensureToken() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.accessToken, nil
	}
	token, expiry, err := getGraphAccessToken(p.cfg)
	if err != nil {
		return "", err
	}
	p.accessToken = token
	p.tokenExpiry = expiry
	return p.accessToken, nil
}

// getGraphAccessToken obtains an access token using client credentials flow.
func getGraphAccessToken(cfg *EmailConfig) (string, time.Time, error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", cfg.AzureTenantID)
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {cfg.AzureClientID},
		"client_secret": {cfg.AzureClientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
	}
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("graph token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading graph token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("graph token error %d: %s", resp.StatusCode, body)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parsing graph token: %w", err)
	}
	// Subtract 60 seconds as safety margin.
	expiry := time.Now().Add(time.Duration(result.ExpiresIn-60) * time.Second)
	return result.AccessToken, expiry, nil
}

func (p *office365Provider) graphGet(path string) ([]byte, error) {
	token, err := p.ensureToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", "https://graph.microsoft.com/v1.0/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph GET %s returned %d: %s", path, resp.StatusCode, body)
	}
	return body, nil
}

func (p *office365Provider) FetchInbox(folder string, limit int) ([]EmailDetail, error) {
	if folder == "" {
		folder = "inbox"
	}
	if limit <= 0 {
		limit = 25
	}
	path := fmt.Sprintf("users/%s/mailFolders/%s/messages?$top=%d&$orderby=receivedDateTime desc",
		url.PathEscape(p.cfg.SenderEmail), url.PathEscape(folder), limit)
	body, err := p.graphGet(path)
	if err != nil {
		return nil, err
	}
	return parseGraphMessages(body, folder)
}

func (p *office365Provider) SearchEmails(query string, limit int) ([]EmailDetail, error) {
	if limit <= 0 {
		limit = 25
	}
	path := fmt.Sprintf("users/%s/messages?$search=%q&$top=%d&$orderby=receivedDateTime desc",
		url.PathEscape(p.cfg.SenderEmail), query, limit)
	body, err := p.graphGet(path)
	if err != nil {
		return nil, err
	}
	return parseGraphMessages(body, "")
}

func (p *office365Provider) GetEmail(emailID string) (*EmailDetail, error) {
	path := fmt.Sprintf("users/%s/messages/%s",
		url.PathEscape(p.cfg.SenderEmail), url.PathEscape(emailID))
	body, err := p.graphGet(path)
	if err != nil {
		return nil, err
	}
	var msg graphMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("parsing graph message: %w", err)
	}
	detail := graphMessageToDetail(&msg, "")
	return &detail, nil
}

func (p *office365Provider) SendEmail(to, subject, body, cc string) error {
	token, err := p.ensureToken()
	if err != nil {
		return err
	}
	toRecipients := buildGraphRecipients(to)
	ccRecipients := buildGraphRecipients(cc)

	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": "HTML",
				"content":     body,
			},
			"toRecipients": toRecipients,
			"ccRecipients": ccRecipients,
		},
	}
	payloadBytes, _ := json.Marshal(payload)
	sendURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/sendMail",
		url.PathEscape(p.cfg.SenderEmail))
	req, err := http.NewRequest("POST", sendURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("graph sendMail: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("graph sendMail returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func (p *office365Provider) ReplyToEmail(emailID, body string) error {
	token, err := p.ensureToken()
	if err != nil {
		return err
	}
	payload := map[string]interface{}{
		"comment": body,
	}
	payloadBytes, _ := json.Marshal(payload)
	replyURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/messages/%s/reply",
		url.PathEscape(p.cfg.SenderEmail), url.PathEscape(emailID))
	req, err := http.NewRequest("POST", replyURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("graph reply: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("graph reply returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// Graph API helpers

type graphMessage struct {
	ID                 string `json:"id"`
	ConversationID     string `json:"conversationId"`
	Subject            string `json:"subject"`
	BodyPreview        string `json:"bodyPreview"`
	ReceivedDateTime   string `json:"receivedDateTime"`
	IsRead             bool   `json:"isRead"`
	HasAttachments     bool   `json:"hasAttachments"`
	ParentFolderID     string `json:"parentFolderId"`
	From               *struct {
		EmailAddress struct {
			Address string `json:"address"`
			Name    string `json:"name"`
		} `json:"emailAddress"`
	} `json:"from"`
	ToRecipients []struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"toRecipients"`
	Body struct {
		Content string `json:"content"`
	} `json:"body"`
}

func parseGraphMessages(data []byte, folder string) ([]EmailDetail, error) {
	var result struct {
		Value []graphMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing graph messages: %w", err)
	}
	details := make([]EmailDetail, 0, len(result.Value))
	for i := range result.Value {
		details = append(details, graphMessageToDetail(&result.Value[i], folder))
	}
	return details, nil
}

func graphMessageToDetail(msg *graphMessage, folder string) EmailDetail {
	var senderEmail, senderName string
	if msg.From != nil {
		senderEmail = msg.From.EmailAddress.Address
		senderName = msg.From.EmailAddress.Name
	}
	recipients := make([]string, 0, len(msg.ToRecipients))
	for _, r := range msg.ToRecipients {
		recipients = append(recipients, r.EmailAddress.Address)
	}
	return EmailDetail{
		EmailSummary: EmailSummary{
			ID:          msg.ID,
			Subject:     msg.Subject,
			SenderEmail: senderEmail,
			SenderName:  senderName,
			BodyPreview: msg.BodyPreview,
			ReceivedAt:  msg.ReceivedDateTime,
			IsRead:      msg.IsRead,
			Folder:      folder,
		},
		Body:           msg.Body.Content,
		Recipients:     recipients,
		HasAttachments: msg.HasAttachments,
	}
}

func buildGraphRecipients(addresses string) []map[string]interface{} {
	if addresses == "" {
		return nil
	}
	parts := strings.Split(addresses, ",")
	result := make([]map[string]interface{}, 0, len(parts))
	for _, addr := range parts {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		result = append(result, map[string]interface{}{
			"emailAddress": map[string]string{
				"address": addr,
			},
		})
	}
	return result
}

// ---------------------------------------------------------------------------
// Gmail provider
// ---------------------------------------------------------------------------

type gmailProvider struct {
	cfg         *EmailConfig
	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func newGmailProvider(cfg *EmailConfig) *gmailProvider {
	return &gmailProvider{cfg: cfg}
}

func (p *gmailProvider) ensureToken() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.accessToken, nil
	}
	token, expiry, err := getGmailAccessToken(p.cfg)
	if err != nil {
		return "", err
	}
	p.accessToken = token
	p.tokenExpiry = expiry
	return p.accessToken, nil
}

func getGmailAccessToken(cfg *EmailConfig) (string, time.Time, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cfg.GoogleClientID},
		"client_secret": {cfg.GoogleClientSecret},
		"refresh_token": {cfg.GoogleRefreshToken},
	}
	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gmail token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading gmail token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("gmail token error %d: %s", resp.StatusCode, body)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parsing gmail token: %w", err)
	}
	expiry := time.Now().Add(time.Duration(result.ExpiresIn-60) * time.Second)
	return result.AccessToken, expiry, nil
}

func (p *gmailProvider) gmailGet(path string) ([]byte, error) {
	token, err := p.ensureToken()
	if err != nil {
		return nil, err
	}
	reqURL := "https://gmail.googleapis.com/gmail/v1/users/me/" + path
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmail GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gmail GET %s returned %d: %s", path, resp.StatusCode, body)
	}
	return body, nil
}

func (p *gmailProvider) FetchInbox(folder string, limit int) ([]EmailDetail, error) {
	if limit <= 0 {
		limit = 25
	}
	// Map common folder names to Gmail label IDs.
	labelID := gmailFolderToLabel(folder)
	path := fmt.Sprintf("messages?labelIds=%s&maxResults=%d", url.QueryEscape(labelID), limit)
	return p.fetchMessageList(path)
}

func (p *gmailProvider) SearchEmails(query string, limit int) ([]EmailDetail, error) {
	if limit <= 0 {
		limit = 25
	}
	path := fmt.Sprintf("messages?q=%s&maxResults=%d", url.QueryEscape(query), limit)
	return p.fetchMessageList(path)
}

func (p *gmailProvider) fetchMessageList(path string) ([]EmailDetail, error) {
	body, err := p.gmailGet(path)
	if err != nil {
		return nil, err
	}
	var listResp struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("parsing gmail message list: %w", err)
	}
	details := make([]EmailDetail, 0, len(listResp.Messages))
	for _, m := range listResp.Messages {
		detail, err := p.GetEmail(m.ID)
		if err != nil {
			log.Printf("email: skipping message %s: %v", m.ID, err)
			continue
		}
		details = append(details, *detail)
	}
	return details, nil
}

func (p *gmailProvider) GetEmail(emailID string) (*EmailDetail, error) {
	body, err := p.gmailGet("messages/" + url.PathEscape(emailID) + "?format=full")
	if err != nil {
		return nil, err
	}
	var msg gmailMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("parsing gmail message: %w", err)
	}
	detail := gmailMessageToDetail(&msg)
	return &detail, nil
}

func (p *gmailProvider) SendEmail(to, subject, body, cc string) error {
	token, err := p.ensureToken()
	if err != nil {
		return err
	}
	from := p.cfg.SenderEmail
	if p.cfg.SenderName != "" {
		from = fmt.Sprintf("%s <%s>", p.cfg.SenderName, p.cfg.SenderEmail)
	}

	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	if cc != "" {
		msg.WriteString("Cc: " + cc + "\r\n")
	}
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	raw := base64.URLEncoding.EncodeToString([]byte(msg.String()))
	payload, _ := json.Marshal(map[string]string{"raw": raw})

	sendURL := "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
	req, err := http.NewRequest("POST", sendURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("gmail send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gmail send returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func (p *gmailProvider) ReplyToEmail(emailID, body string) error {
	// Fetch original message to get thread ID and headers.
	original, err := p.GetEmail(emailID)
	if err != nil {
		return fmt.Errorf("fetching original for reply: %w", err)
	}

	token, err := p.ensureToken()
	if err != nil {
		return err
	}

	from := p.cfg.SenderEmail
	if p.cfg.SenderName != "" {
		from = fmt.Sprintf("%s <%s>", p.cfg.SenderName, p.cfg.SenderEmail)
	}

	subject := original.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + original.SenderEmail + "\r\n")
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("In-Reply-To: " + emailID + "\r\n")
	msg.WriteString("References: " + emailID + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	raw := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	// We need the threadId from the original message; fetch raw JSON again.
	rawBody, err := p.gmailGet("messages/" + url.PathEscape(emailID) + "?format=metadata&metadataHeaders=Message-Id")
	if err != nil {
		return err
	}
	var meta struct {
		ThreadID string `json:"threadId"`
	}
	json.Unmarshal(rawBody, &meta)

	payload, _ := json.Marshal(map[string]string{
		"raw":      raw,
		"threadId": meta.ThreadID,
	})

	sendURL := "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
	req, err := http.NewRequest("POST", sendURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("gmail reply: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gmail reply returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// Gmail helpers

type gmailMessage struct {
	ID        string `json:"id"`
	ThreadID  string `json:"threadId"`
	LabelIDs  []string `json:"labelIds"`
	Snippet   string `json:"snippet"`
	Payload   struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		MimeType string `json:"mimeType"`
		Body     struct {
			Data string `json:"data"`
			Size int    `json:"size"`
		} `json:"body"`
		Parts []struct {
			MimeType string `json:"mimeType"`
			Body     struct {
				Data string `json:"data"`
				Size int    `json:"size"`
			} `json:"body"`
		} `json:"parts"`
	} `json:"payload"`
	InternalDate string `json:"internalDate"`
}

func gmailMessageToDetail(msg *gmailMessage) EmailDetail {
	headers := make(map[string]string)
	for _, h := range msg.Payload.Headers {
		headers[strings.ToLower(h.Name)] = h.Value
	}

	// Parse received time from internalDate (epoch millis).
	var receivedAt string
	if msg.InternalDate != "" {
		var millis int64
		fmt.Sscanf(msg.InternalDate, "%d", &millis)
		if millis > 0 {
			receivedAt = time.UnixMilli(millis).UTC().Format(time.RFC3339)
		}
	}

	// Extract body from parts or top-level payload.
	bodyContent := extractGmailBody(msg)

	// Parse sender.
	senderEmail, senderName := parseEmailAddress(headers["from"])

	// Parse recipients.
	recipients := parseRecipientList(headers["to"])

	// Determine folder from labels.
	folder := "inbox"
	for _, l := range msg.LabelIDs {
		switch l {
		case "SENT":
			folder = "sent"
		case "DRAFT":
			folder = "drafts"
		case "TRASH":
			folder = "trash"
		case "SPAM":
			folder = "spam"
		}
	}

	isRead := true
	for _, l := range msg.LabelIDs {
		if l == "UNREAD" {
			isRead = false
			break
		}
	}

	hasAttachments := false
	for _, part := range msg.Payload.Parts {
		if part.MimeType != "text/plain" && part.MimeType != "text/html" &&
			!strings.HasPrefix(part.MimeType, "multipart/") {
			hasAttachments = true
			break
		}
	}

	return EmailDetail{
		EmailSummary: EmailSummary{
			ID:          msg.ID,
			Subject:     headers["subject"],
			SenderEmail: senderEmail,
			SenderName:  senderName,
			BodyPreview: msg.Snippet,
			ReceivedAt:  receivedAt,
			IsRead:      isRead,
			Folder:      folder,
		},
		Body:           bodyContent,
		Recipients:     recipients,
		HasAttachments: hasAttachments,
	}
}

func extractGmailBody(msg *gmailMessage) string {
	// Try to find text/html or text/plain in parts.
	for _, part := range msg.Payload.Parts {
		if part.MimeType == "text/html" && part.Body.Data != "" {
			decoded, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil {
				return string(decoded)
			}
		}
	}
	for _, part := range msg.Payload.Parts {
		if part.MimeType == "text/plain" && part.Body.Data != "" {
			decoded, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil {
				return string(decoded)
			}
		}
	}
	// Fallback to top-level body.
	if msg.Payload.Body.Data != "" {
		decoded, err := base64.URLEncoding.DecodeString(msg.Payload.Body.Data)
		if err == nil {
			return string(decoded)
		}
	}
	return msg.Snippet
}

func gmailFolderToLabel(folder string) string {
	switch strings.ToLower(folder) {
	case "inbox", "":
		return "INBOX"
	case "sent", "sentitems":
		return "SENT"
	case "drafts":
		return "DRAFT"
	case "trash":
		return "TRASH"
	case "spam":
		return "SPAM"
	case "starred":
		return "STARRED"
	default:
		return strings.ToUpper(folder)
	}
}

// parseEmailAddress extracts name and email from "Name <email>" format.
func parseEmailAddress(raw string) (email, name string) {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "<"); idx >= 0 {
		name = strings.TrimSpace(raw[:idx])
		name = strings.Trim(name, "\"")
		end := strings.Index(raw, ">")
		if end > idx {
			email = raw[idx+1 : end]
		}
	} else {
		email = raw
	}
	return
}

func parseRecipientList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		addr, _ := parseEmailAddress(p)
		if addr != "" {
			result = append(result, addr)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// EmailManager — manages the active provider and local SQLite storage
// ---------------------------------------------------------------------------

// EmailManager provides high-level email operations for MCP tools.
type EmailManager struct {
	provider EmailProvider
	cfg      *EmailConfig
	db       *sql.DB
	mu       sync.Mutex
}

// NewEmailManager initialises the manager, opens/creates the local database,
// and selects the appropriate provider based on config.
func NewEmailManager(cfg *EmailConfig) (*EmailManager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("email config is nil")
	}

	// Open SQLite database.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	dbDir := filepath.Join(homeDir, configDirName)
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		return nil, fmt.Errorf("creating config dir: %w", err)
	}
	dbPath := filepath.Join(dbDir, "emails.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening email db: %w", err)
	}

	// Create schema.
	if err := initEmailDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialising email db: %w", err)
	}

	// Select provider.
	var provider EmailProvider
	switch strings.ToLower(cfg.Provider) {
	case "office365":
		provider = newOffice365Provider(cfg)
	case "gmail":
		provider = newGmailProvider(cfg)
	default:
		db.Close()
		return nil, fmt.Errorf("unsupported email provider: %q", cfg.Provider)
	}

	return &EmailManager{
		provider: provider,
		cfg:      cfg,
		db:       db,
	}, nil
}

func initEmailDB(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS emails (
    id TEXT PRIMARY KEY,
    conversation_id TEXT,
    folder TEXT,
    subject TEXT,
    sender_email TEXT,
    sender_name TEXT,
    recipients TEXT,
    body_preview TEXT,
    body TEXT,
    received_at TEXT,
    is_read INTEGER,
    has_attachments INTEGER,
    summary TEXT,
    category TEXT,
    synced_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_emails_folder ON emails(folder);
CREATE INDEX IF NOT EXISTS idx_emails_received ON emails(received_at);
`
	_, err := db.Exec(schema)
	return err
}

// ListInbox fetches emails from a folder, optionally filtered by search term.
// It tries the local DB first if synced data exists, otherwise queries the provider.
func (m *EmailManager) ListInbox(folder, search string, limit int) ([]EmailSummary, error) {
	if limit <= 0 {
		limit = 25
	}

	// Try local DB first.
	summaries, err := m.queryLocalEmails(folder, search, limit)
	if err == nil && len(summaries) > 0 {
		return summaries, nil
	}

	// Fall back to provider.
	details, err := m.provider.FetchInbox(folder, limit)
	if err != nil {
		return nil, err
	}

	// Store in local DB.
	m.storeEmails(details)

	// Filter by search if needed.
	result := make([]EmailSummary, 0, len(details))
	searchLower := strings.ToLower(search)
	for _, d := range details {
		if search != "" {
			if !strings.Contains(strings.ToLower(d.Subject), searchLower) &&
				!strings.Contains(strings.ToLower(d.SenderEmail), searchLower) &&
				!strings.Contains(strings.ToLower(d.SenderName), searchLower) &&
				!strings.Contains(strings.ToLower(d.BodyPreview), searchLower) {
				continue
			}
		}
		result = append(result, d.EmailSummary)
	}
	return result, nil
}

// SearchEmails performs a full-text search across synced emails and the provider.
func (m *EmailManager) SearchEmails(query string, limit int) ([]EmailSummary, error) {
	if limit <= 0 {
		limit = 25
	}

	// Try local DB first.
	summaries, err := m.queryLocalEmails("", query, limit)
	if err == nil && len(summaries) > 0 {
		return summaries, nil
	}

	// Fall back to provider search.
	details, err := m.provider.SearchEmails(query, limit)
	if err != nil {
		return nil, err
	}

	m.storeEmails(details)

	result := make([]EmailSummary, 0, len(details))
	for _, d := range details {
		result = append(result, d.EmailSummary)
	}
	return result, nil
}

// GetEmail retrieves a single email by ID, checking local DB first.
func (m *EmailManager) GetEmail(emailID string) (*EmailDetail, error) {
	// Try local DB.
	detail, err := m.getLocalEmail(emailID)
	if err == nil && detail != nil {
		return detail, nil
	}

	// Fall back to provider.
	detail, err = m.provider.GetEmail(emailID)
	if err != nil {
		return nil, err
	}

	// Store locally.
	m.storeEmails([]EmailDetail{*detail})
	return detail, nil
}

// SendEmail sends a new email via the configured provider.
func (m *EmailManager) SendEmail(to, subject, body, cc string) error {
	return m.provider.SendEmail(to, subject, body, cc)
}

// ReplyToEmail replies to an existing email thread.
func (m *EmailManager) ReplyToEmail(emailID, body string) error {
	return m.provider.ReplyToEmail(emailID, body)
}

// SyncEmails fetches recent emails from the provider and stores them locally.
// Returns the number of emails synced.
func (m *EmailManager) SyncEmails() (int, error) {
	folders := []string{"inbox", "sentitems"}
	total := 0

	for _, folder := range folders {
		details, err := m.provider.FetchInbox(folder, 50)
		if err != nil {
			log.Printf("email: sync %s failed: %v", folder, err)
			continue
		}
		m.storeEmails(details)
		total += len(details)
	}
	return total, nil
}

// Close releases the database connection.
func (m *EmailManager) Close() {
	if m.db != nil {
		m.db.Close()
	}
}

// ---------------------------------------------------------------------------
// Local SQLite operations
// ---------------------------------------------------------------------------

func (m *EmailManager) storeEmails(emails []EmailDetail) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx, err := m.db.Begin()
	if err != nil {
		log.Printf("email: begin tx: %v", err)
		return
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT OR REPLACE INTO emails
    (id, conversation_id, folder, subject, sender_email, sender_name, recipients,
     body_preview, body, received_at, is_read, has_attachments, summary, category, synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		log.Printf("email: prepare insert: %v", err)
		return
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range emails {
		recipientsJSON, _ := json.Marshal(e.Recipients)
		isRead := 0
		if e.IsRead {
			isRead = 1
		}
		hasAttach := 0
		if e.HasAttachments {
			hasAttach = 1
		}
		_, err := stmt.Exec(
			e.ID, "", e.Folder, e.Subject, e.SenderEmail, e.SenderName,
			string(recipientsJSON), e.BodyPreview, e.Body, e.ReceivedAt,
			isRead, hasAttach, e.Summary, e.Category, now,
		)
		if err != nil {
			log.Printf("email: insert %s: %v", e.ID, err)
		}
	}
	tx.Commit()
}

func (m *EmailManager) queryLocalEmails(folder, search string, limit int) ([]EmailSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `SELECT id, folder, subject, sender_email, sender_name, body_preview, received_at, is_read
              FROM emails WHERE 1=1`
	args := make([]interface{}, 0)

	if folder != "" {
		query += " AND folder = ?"
		args = append(args, folder)
	}
	if search != "" {
		query += " AND (subject LIKE ? OR sender_email LIKE ? OR sender_name LIKE ? OR body_preview LIKE ?)"
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}
	query += " ORDER BY received_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := m.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []EmailSummary
	for rows.Next() {
		var s EmailSummary
		var isRead int
		err := rows.Scan(&s.ID, &s.Folder, &s.Subject, &s.SenderEmail, &s.SenderName,
			&s.BodyPreview, &s.ReceivedAt, &isRead)
		if err != nil {
			return nil, err
		}
		s.IsRead = isRead != 0
		result = append(result, s)
	}
	return result, rows.Err()
}

func (m *EmailManager) getLocalEmail(emailID string) (*EmailDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	row := m.db.QueryRow(`
SELECT id, folder, subject, sender_email, sender_name, recipients,
       body_preview, body, received_at, is_read, has_attachments, summary, category
FROM emails WHERE id = ?`, emailID)

	var d EmailDetail
	var recipientsJSON string
	var isRead, hasAttach int
	var body sql.NullString
	err := row.Scan(&d.ID, &d.Folder, &d.Subject, &d.SenderEmail, &d.SenderName,
		&recipientsJSON, &d.BodyPreview, &body, &d.ReceivedAt, &isRead, &hasAttach,
		&d.Summary, &d.Category)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	d.IsRead = isRead != 0
	d.HasAttachments = hasAttach != 0
	if body.Valid {
		d.Body = body.String
	}
	if recipientsJSON != "" {
		json.Unmarshal([]byte(recipientsJSON), &d.Recipients)
	}
	return &d, nil
}
