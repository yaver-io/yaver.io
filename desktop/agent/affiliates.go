package main

// affiliates.go — affiliate / referral tracking on top of the
// shortener. Replaces FirstPromoter / Rewardful / PartnerStack
// for the solo dev who ships indie.
//
// Model:
//
//   Affiliate {
//     id, email, name, code,     // code = short-link code
//     commissionPercent, payouts []Payout, createdAt
//   }
//   Conversion { affiliateId, amount, currency, at, sourceRef }
//
// Flow:
//
//   1. Dev creates an affiliate → gets a short-link code that
//      /s/:code already redirects through, plus a referral URL
//      pattern (e.g. https://app.com?ref=<code>) the partner
//      can share.
//   2. Partner sends visitors → /s/:code logs the click (reused
//      from shortener.go). Attribution cookie optional — the
//      solo-dev pattern is usually an explicit ?ref= param.
//   3. When a purchase completes (Stripe webhook, manual POST),
//      the dev records a Conversion with the affiliate code.
//   4. Agent computes the commission owed + rolls it into the
//      affiliate's ledger. Payouts are recorded when the dev
//      pays out (Wise / bank transfer — the agent doesn't
//      touch money, it just tracks).
//
// HTTP:
//
//   POST /affiliates                        create
//   GET  /affiliates                        list
//   GET  /affiliates/:id                    detail + conversions
//   POST /affiliates/:id/conversion         record a sale
//   POST /affiliates/:id/payout             mark a payout
//
// Nothing touches Convex; ledger lives in affiliates.json +
// affiliate-conversions.jsonl.

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

type Affiliate struct {
	ID                string    `json:"id"`
	Email             string    `json:"email"`
	Name              string    `json:"name,omitempty"`
	Code              string    `json:"code"`
	CommissionPercent float64   `json:"commissionPercent"`
	CreatedAt         time.Time `json:"createdAt"`
	TotalOwed         float64   `json:"totalOwed"`
	TotalPaid         float64   `json:"totalPaid"`
}

type Conversion struct {
	AffiliateID string    `json:"affiliateId"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency"`
	Commission  float64   `json:"commission"`
	SourceRef   string    `json:"sourceRef,omitempty"`
	At          time.Time `json:"at"`
}

type Payout struct {
	AffiliateID string    `json:"affiliateId"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency"`
	Method      string    `json:"method,omitempty"` // "wise", "paypal", "bank"
	Note        string    `json:"note,omitempty"`
	At          time.Time `json:"at"`
}

var (
	affMu        sync.Mutex
	affCache     []Affiliate
)

func affFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "affiliates.json"), nil
}

func convEventsFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "affiliate-conversions.jsonl"), nil
}

func loadAffiliates() []Affiliate {
	affMu.Lock()
	defer affMu.Unlock()
	if affCache != nil {
		return affCache
	}
	p, _ := affFile()
	data, err := os.ReadFile(p)
	if err != nil {
		affCache = []Affiliate{}
		return affCache
	}
	_ = json.Unmarshal(data, &affCache)
	return affCache
}

func saveAffiliates() error {
	p, _ := affFile()
	data, _ := json.MarshalIndent(affCache, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleAffiliates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "affiliates": loadAffiliates()})
	case http.MethodPost:
		var a Affiliate
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if a.Email == "" {
			jsonError(w, http.StatusBadRequest, "email required")
			return
		}
		if a.Code == "" {
			a.Code = randomShortCode()
		}
		if a.ID == "" {
			a.ID = randomFormID()
		}
		if a.CommissionPercent <= 0 {
			a.CommissionPercent = 20
		}
		a.CreatedAt = time.Now().UTC()
		affMu.Lock()
		affCache = append(loadAffiliates(), a)
		_ = saveAffiliates()
		affMu.Unlock()
		jsonReply(w, http.StatusCreated, map[string]interface{}{
			"ok":         true,
			"affiliate":  a,
			"referralUrl": fmt.Sprintf("?ref=%s", a.Code),
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}

// handleAffiliateSub routes /affiliates/:id, /affiliates/:id/conversion,
// /affiliates/:id/payout.
func (s *HTTPServer) handleAffiliateSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}
	id := parts[1]
	var aff *Affiliate
	list := loadAffiliates()
	for i := range list {
		if list[i].ID == id || list[i].Code == id {
			aff = &affCache[i]
			break
		}
	}
	if aff == nil {
		jsonError(w, http.StatusNotFound, "affiliate not found")
		return
	}
	switch {
	case len(parts) == 2:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "affiliate": aff})
	case len(parts) == 3 && parts[2] == "conversion" && r.Method == http.MethodPost:
		var body struct {
			Amount    float64 `json:"amount"`
			Currency  string  `json:"currency"`
			SourceRef string  `json:"sourceRef"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Amount <= 0 {
			jsonError(w, http.StatusBadRequest, "amount required")
			return
		}
		if body.Currency == "" {
			body.Currency = "USD"
		}
		commission := body.Amount * aff.CommissionPercent / 100
		conv := Conversion{
			AffiliateID: aff.ID,
			Amount:      body.Amount,
			Currency:    body.Currency,
			Commission:  commission,
			SourceRef:   body.SourceRef,
			At:          time.Now().UTC(),
		}
		_ = appendConversionRow(conv)
		affMu.Lock()
		aff.TotalOwed += commission
		_ = saveAffiliates()
		affMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "conversion": conv})
	case len(parts) == 3 && parts[2] == "payout" && r.Method == http.MethodPost:
		var p Payout
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p.Amount <= 0 {
			jsonError(w, http.StatusBadRequest, "amount required")
			return
		}
		p.AffiliateID = aff.ID
		p.At = time.Now().UTC()
		affMu.Lock()
		aff.TotalOwed -= p.Amount
		aff.TotalPaid += p.Amount
		_ = saveAffiliates()
		affMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "payout": p})
	default:
		jsonError(w, http.StatusBadRequest, "unsupported path/method")
	}
}

func appendConversionRow(c Conversion) error {
	p, _ := convEventsFile()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(c)
}
