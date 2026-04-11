package main

// newsletter.go — self-hosted list management + broadcast mail.
// Replaces ConvertKit / Mailchimp / Buttondown for the solo dev
// who already has an SMTP relay wired. The server owns the data,
// the reader owns the list, nobody else sees it.
//
// Model:
//
//   Subscriber     — email + confirm state + one-click
//                    unsubscribe token. Persisted in
//                    subscribers.json.
//   Campaign       — subject + HTML + text + status
//                    (draft/sending/sent). Persisted in
//                    campaigns.json.
//
// HTTP surface:
//
//   POST /newsletter/subscribe              public — double opt-in
//   GET  /newsletter/confirm?token=...      public — verify subscribe
//   GET  /newsletter/unsubscribe?token=...  public — one-click
//   GET  /newsletter/subscribers            owner — list subscribers
//   GET  /newsletter/campaigns              owner — list campaigns
//   POST /newsletter/campaigns              owner — create draft
//   POST /newsletter/campaigns/:id/send     owner — broadcast
//
// The broadcast loop walks confirmed subscribers and calls
// SendTransactionalEmail per recipient. Failures land in the
// campaign record so the dev can see which addresses bounced.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Subscriber is one row in the list.
type Subscriber struct {
	Email           string    `json:"email"`
	Status          string    `json:"status"` // "pending" | "confirmed" | "unsubscribed"
	CreatedAt       time.Time `json:"createdAt"`
	ConfirmedAt     time.Time `json:"confirmedAt,omitempty"`
	UnsubscribedAt  time.Time `json:"unsubscribedAt,omitempty"`
	ConfirmToken    string    `json:"confirmToken"`
	UnsubToken      string    `json:"unsubToken"`
	Source          string    `json:"source,omitempty"`
}

// Campaign is one broadcast.
type Campaign struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	HTMLBody  string    `json:"htmlBody,omitempty"`
	Body      string    `json:"body,omitempty"`
	Status    string    `json:"status"` // "draft" | "sending" | "sent" | "failed"
	CreatedAt time.Time `json:"createdAt"`
	SentAt    time.Time `json:"sentAt,omitempty"`
	Stats     struct {
		Total     int      `json:"total"`
		Delivered int      `json:"delivered"`
		Failed    int      `json:"failed"`
		Bounces   []string `json:"bounces,omitempty"`
	} `json:"stats"`
}

// --- storage ---------------------------------------------------------------

var (
	newsletterMu      sync.Mutex
	subscribersCache  []Subscriber
	campaignsCache    []Campaign
	newsletterSecret  []byte
)

func newsletterDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	nlDir := filepath.Join(dir, "newsletter")
	if err := os.MkdirAll(nlDir, 0o700); err != nil {
		return "", err
	}
	return nlDir, nil
}

func subscribersFile() (string, error) {
	dir, err := newsletterDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "subscribers.json"), nil
}

func campaignsFile() (string, error) {
	dir, err := newsletterDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "campaigns.json"), nil
}

func newsletterHMACSecret() []byte {
	newsletterMu.Lock()
	defer newsletterMu.Unlock()
	if newsletterSecret != nil {
		return newsletterSecret
	}
	dir, _ := newsletterDir()
	sFile := filepath.Join(dir, "hmac.key")
	if data, err := os.ReadFile(sFile); err == nil && len(data) >= 32 {
		newsletterSecret = data
		return newsletterSecret
	}
	buf := make([]byte, 32)
	_, _ = randomRead(buf)
	_ = os.WriteFile(sFile, buf, 0o600)
	newsletterSecret = buf
	return newsletterSecret
}

func loadSubscribers() []Subscriber {
	newsletterMu.Lock()
	defer newsletterMu.Unlock()
	if subscribersCache != nil {
		return subscribersCache
	}
	p, err := subscribersFile()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		subscribersCache = []Subscriber{}
		return subscribersCache
	}
	var out []Subscriber
	_ = json.Unmarshal(data, &out)
	subscribersCache = out
	return out
}

func saveSubscribers(subs []Subscriber) error {
	p, err := subscribersFile()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(subs, "", "  ")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	newsletterMu.Lock()
	subscribersCache = subs
	newsletterMu.Unlock()
	return nil
}

func loadCampaigns() []Campaign {
	newsletterMu.Lock()
	defer newsletterMu.Unlock()
	if campaignsCache != nil {
		return campaignsCache
	}
	p, err := campaignsFile()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		campaignsCache = []Campaign{}
		return campaignsCache
	}
	var out []Campaign
	_ = json.Unmarshal(data, &out)
	campaignsCache = out
	return out
}

func saveCampaigns(camps []Campaign) error {
	p, err := campaignsFile()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(camps, "", "  ")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	newsletterMu.Lock()
	campaignsCache = camps
	newsletterMu.Unlock()
	return nil
}

// tokenFor returns an HMAC-based token for the given email +
// purpose. Used for both confirm and unsubscribe links so the
// dev can hand out one-click URLs that can't be forged.
func tokenFor(email, purpose string) string {
	mac := hmac.New(sha256.New, newsletterHMACSecret())
	mac.Write([]byte(purpose + ":" + strings.ToLower(email)))
	return hex.EncodeToString(mac.Sum(nil))
}

func findSubscriberByToken(token, purpose string) *Subscriber {
	subs := loadSubscribers()
	for i := range subs {
		if tokenFor(subs[i].Email, purpose) == token {
			return &subs[i]
		}
	}
	return nil
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleNewsletterSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Email  string `json:"email"`
		Source string `json:"source,omitempty"`
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&body)
	} else {
		_ = r.ParseForm()
		body.Email = r.PostForm.Get("email")
		body.Source = r.PostForm.Get("source")
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		jsonError(w, http.StatusBadRequest, "email required")
		return
	}

	subs := loadSubscribers()
	for i := range subs {
		if subs[i].Email == email {
			// Already on the list — resend confirm link if still pending.
			if subs[i].Status == "pending" {
				go sendSubscribeConfirm(&subs[i])
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "status": subs[i].Status})
			return
		}
	}
	newSub := Subscriber{
		Email:        email,
		Status:       "pending",
		CreatedAt:    time.Now().UTC(),
		ConfirmToken: tokenFor(email, "confirm"),
		UnsubToken:   tokenFor(email, "unsubscribe"),
		Source:       body.Source,
	}
	subs = append(subs, newSub)
	if err := saveSubscribers(subs); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go sendSubscribeConfirm(&newSub)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "status": "pending"})
}

func (s *HTTPServer) handleNewsletterConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	sub := findSubscriberByToken(token, "confirm")
	if sub == nil {
		jsonError(w, http.StatusNotFound, "invalid token")
		return
	}
	subs := loadSubscribers()
	for i := range subs {
		if subs[i].Email == sub.Email {
			subs[i].Status = "confirmed"
			subs[i].ConfirmedAt = time.Now().UTC()
			break
		}
	}
	_ = saveSubscribers(subs)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><body style='font-family:system-ui;padding:40px;text-align:center'><h1>Confirmed!</h1><p>You're subscribed. Thanks.</p></body></html>")
}

func (s *HTTPServer) handleNewsletterUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	sub := findSubscriberByToken(token, "unsubscribe")
	if sub == nil {
		jsonError(w, http.StatusNotFound, "invalid token")
		return
	}
	subs := loadSubscribers()
	for i := range subs {
		if subs[i].Email == sub.Email {
			subs[i].Status = "unsubscribed"
			subs[i].UnsubscribedAt = time.Now().UTC()
			break
		}
	}
	_ = saveSubscribers(subs)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><body style='font-family:system-ui;padding:40px;text-align:center'><h1>Unsubscribed</h1><p>You won't hear from us again.</p></body></html>")
}

func (s *HTTPServer) handleNewsletterSubscribers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	subs := loadSubscribers()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"subscribers": subs,
		"count": map[string]int{
			"total":        len(subs),
			"confirmed":    countByStatus(subs, "confirmed"),
			"pending":      countByStatus(subs, "pending"),
			"unsubscribed": countByStatus(subs, "unsubscribed"),
		},
	})
}

func countByStatus(subs []Subscriber, status string) int {
	n := 0
	for _, s := range subs {
		if s.Status == status {
			n++
		}
	}
	return n
}

func (s *HTTPServer) handleNewsletterCampaigns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "campaigns": loadCampaigns()})
	case http.MethodPost:
		var c Campaign
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if c.Subject == "" {
			jsonError(w, http.StatusBadRequest, "subject required")
			return
		}
		c.ID = randomFormID()
		c.Status = "draft"
		c.CreatedAt = time.Now().UTC()
		camps := append(loadCampaigns(), c)
		if err := saveCampaigns(camps); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "campaign": c})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleNewsletterSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	// Path: /newsletter/campaigns/:id/send
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "newsletter" || parts[1] != "campaigns" || parts[3] != "send" {
		jsonError(w, http.StatusNotFound, "invalid send path")
		return
	}
	id := parts[2]
	camps := loadCampaigns()
	var camp *Campaign
	for i := range camps {
		if camps[i].ID == id {
			camp = &camps[i]
			break
		}
	}
	if camp == nil {
		jsonError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if camp.Status == "sent" || camp.Status == "sending" {
		jsonError(w, http.StatusBadRequest, "already "+camp.Status)
		return
	}
	camp.Status = "sending"
	_ = saveCampaigns(camps)
	go broadcastCampaign(camp.ID)
	jsonReply(w, http.StatusAccepted, map[string]interface{}{"ok": true, "status": "sending"})
}

// broadcastCampaign walks the confirmed subscribers and ships
// the campaign one at a time. Appends an unsubscribe footer per
// recipient so each email has a unique one-click URL.
func broadcastCampaign(id string) {
	camps := loadCampaigns()
	var camp *Campaign
	for i := range camps {
		if camps[i].ID == id {
			camp = &camps[i]
			break
		}
	}
	if camp == nil {
		return
	}
	subs := loadSubscribers()
	confirmed := make([]Subscriber, 0, len(subs))
	for _, s := range subs {
		if s.Status == "confirmed" {
			confirmed = append(confirmed, s)
		}
	}
	camp.Stats.Total = len(confirmed)

	for _, sub := range confirmed {
		unsubURL := fmt.Sprintf("/newsletter/unsubscribe?token=%s", sub.UnsubToken)
		body := camp.Body + "\n\n--\nTo unsubscribe, visit " + unsubURL
		htmlBody := ""
		if camp.HTMLBody != "" {
			htmlBody = camp.HTMLBody + `<hr><p style="font-size:12px;color:#888">To unsubscribe, <a href="` + unsubURL + `">click here</a>.</p>`
		}
		_, err := SendTransactionalEmail(SendEmailRequest{
			To:       []string{sub.Email},
			Subject:  camp.Subject,
			Body:     body,
			HTMLBody: htmlBody,
		})
		if err != nil {
			camp.Stats.Failed++
			camp.Stats.Bounces = append(camp.Stats.Bounces, sub.Email)
		} else {
			camp.Stats.Delivered++
		}
		time.Sleep(100 * time.Millisecond) // gentle pace — don't trip SMTP rate limits
	}
	camp.Status = "sent"
	camp.SentAt = time.Now().UTC()
	_ = saveCampaigns(camps)
}

// sendSubscribeConfirm ships the one-click confirm email.
func sendSubscribeConfirm(sub *Subscriber) {
	url := fmt.Sprintf("/newsletter/confirm?token=%s", sub.ConfirmToken)
	body := "Click to confirm your subscription: " + url
	_, _ = SendTransactionalEmail(SendEmailRequest{
		To:      []string{sub.Email},
		Subject: "Confirm your subscription",
		Body:    body,
	})
}
