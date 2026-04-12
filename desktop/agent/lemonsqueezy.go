package main

// lemonsqueezy.go — LemonSqueezy payment/subscription management for Yaver.
//
// LemonSqueezy is a merchant of record for SaaS products.
// API base: https://api.lemonsqueezy.com/v1
// Auth: Bearer token via API key.
//
// Config:
//   - env LEMONSQUEEZY_API_KEY
//   - or ~/.yaver/lemonsqueezy.json {"apiKey": "..."}
//
// HTTP endpoints:
//   GET  /lemonsqueezy/status           — connectivity + store info
//   GET  /lemonsqueezy/products         — list products
//   GET  /lemonsqueezy/orders           — list orders (filter: ?email=)
//   GET  /lemonsqueezy/subscriptions    — list subscriptions (filter: ?status=)
//   GET  /lemonsqueezy/customers        — list customers (filter: ?email=)
//   GET  /lemonsqueezy/discounts        — list discounts
//   POST /lemonsqueezy/discounts        — create discount
//   GET  /lemonsqueezy/revenue          — aggregated revenue stats
//   GET  /lemonsqueezy/setup            — Next.js integration code
//   POST /lemonsqueezy/webhook/start    — start local webhook listener
//   POST /lemonsqueezy/webhook/stop     — stop webhook listener
//   GET  /lemonsqueezy/webhook/logs     — recent webhook payloads

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const lsAPIBase = "https://api.lemonsqueezy.com/v1"

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

type LSStatus struct {
	Connected bool   `json:"connected"`
	StoreName string `json:"storeName"`
	StoreID   string `json:"storeId"`
	StoreURL  string `json:"storeUrl"`
}

type LSProduct struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Price          int    `json:"price"`          // cents
	PriceFormatted string `json:"priceFormatted"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	BuyURL         string `json:"buyUrl"`
	CreatedAt      string `json:"createdAt"`
}

type LSOrder struct {
	ID             string `json:"id"`
	CustomerEmail  string `json:"customerEmail"`
	Total          int    `json:"total"` // cents
	TotalFormatted string `json:"totalFormatted"`
	Status         string `json:"status"`
	CreatedAt      string `json:"createdAt"`
	ProductName    string `json:"productName"`
}

type LSSubscription struct {
	ID            string `json:"id"`
	CustomerEmail string `json:"customerEmail"`
	ProductName   string `json:"productName"`
	Status        string `json:"status"` // active, cancelled, expired, past_due
	RenewsAt      string `json:"renewsAt"`
	CreatedAt     string `json:"createdAt"`
	Price         int    `json:"price"`
	Interval      string `json:"interval"` // month, year
}

type LSCustomer struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	TotalSpent int    `json:"totalSpent"`
	CreatedAt  string `json:"createdAt"`
}

type LSDiscount struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Code       string `json:"code"`
	Amount     int    `json:"amount"`
	AmountType string `json:"amountType"` // "percent" or "fixed"
	IsLimited  bool   `json:"isLimited"`
	UsageCount int    `json:"usageCount"`
}

type LSRevenue struct {
	TotalRevenue   int `json:"totalRevenue"`   // cents
	MRR            int `json:"mrr"`            // monthly recurring revenue, cents
	ActiveSubs     int `json:"activeSubs"`
	CancelledSubs  int `json:"cancelledSubs"`
	TotalOrders    int `json:"totalOrders"`
	TotalCustomers int `json:"totalCustomers"`
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// lsConfig is the on-disk config format for ~/.yaver/lemonsqueezy.json
type lsConfig struct {
	APIKey        string `json:"apiKey"`
	WebhookSecret string `json:"webhookSecret,omitempty"`
}

// LemonSqueezyManager wraps the Lemon Squeezy API.
type LemonSqueezyManager struct {
	apiKey        string
	webhookSecret string

	mu         sync.Mutex
	webhookSrv *http.Server
	webhookBuf []string // ring buffer of recent payloads
}

// NewLemonSqueezyManager reads the API key from env or ~/.yaver/lemonsqueezy.json.
func NewLemonSqueezyManager() *LemonSqueezyManager {
	m := &LemonSqueezyManager{}

	// 1. env var takes priority
	if key := os.Getenv("LEMONSQUEEZY_API_KEY"); key != "" {
		m.apiKey = key
		m.webhookSecret = os.Getenv("LEMONSQUEEZY_WEBHOOK_SECRET")
		return m
	}

	// 2. config file
	cfgPath, err := lsConfigPath()
	if err == nil {
		data, err := os.ReadFile(cfgPath)
		if err == nil {
			var cfg lsConfig
			if json.Unmarshal(data, &cfg) == nil && cfg.APIKey != "" {
				m.apiKey = cfg.APIKey
				m.webhookSecret = cfg.WebhookSecret
			}
		}
	}

	return m
}

func lsConfigPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lemonsqueezy.json"), nil
}

// ---------------------------------------------------------------------------
// Core HTTP helper
// ---------------------------------------------------------------------------

// lsAPIGet calls the Lemon Squeezy API and returns the raw JSON `data` value.
// If the API returns a list, `data` is an array; if a single resource, it's an object.
func (m *LemonSqueezyManager) lsAPIGet(path string, params url.Values) (json.RawMessage, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("LEMONSQUEEZY_API_KEY not set (set env var or write ~/.yaver/lemonsqueezy.json)")
	}

	u := lsAPIBase + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Accept", "application/vnd.api+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to surface the LS error message
		var errBody struct {
			Errors []struct {
				Title  string `json:"title"`
				Detail string `json:"detail"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(body, &errBody)
		if len(errBody.Errors) > 0 {
			return nil, fmt.Errorf("API error %d: %s — %s", resp.StatusCode, errBody.Errors[0].Title, errBody.Errors[0].Detail)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return envelope.Data, nil
}

// lsAPIPost issues a POST to the Lemon Squeezy API with a JSON:API body.
func (m *LemonSqueezyManager) lsAPIPost(path string, body interface{}) (json.RawMessage, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("LEMONSQUEEZY_API_KEY not set")
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, lsAPIBase+path, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/vnd.api+json")
	req.Header.Set("Accept", "application/vnd.api+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errBody struct {
			Errors []struct {
				Title  string `json:"title"`
				Detail string `json:"detail"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(respBody, &errBody)
		if len(errBody.Errors) > 0 {
			return nil, fmt.Errorf("API error %d: %s — %s", resp.StatusCode, errBody.Errors[0].Title, errBody.Errors[0].Detail)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return envelope.Data, nil
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status checks API connectivity and returns store info.
func (m *LemonSqueezyManager) Status() (*LSStatus, error) {
	data, err := m.lsAPIGet("/stores", nil)
	if err != nil {
		return &LSStatus{Connected: false}, err
	}

	// data is an array of store objects
	var stores []struct {
		ID         string `json:"id"`
		Attributes struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
			URL  string `json:"url"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &stores); err != nil {
		return &LSStatus{Connected: false}, fmt.Errorf("parse stores: %w", err)
	}

	if len(stores) == 0 {
		return &LSStatus{Connected: true, StoreName: "(no stores)"}, nil
	}

	s := stores[0]
	storeURL := s.Attributes.URL
	if storeURL == "" {
		storeURL = "https://app.lemonsqueezy.com/stores/" + s.ID
	}

	return &LSStatus{
		Connected: true,
		StoreName: s.Attributes.Name,
		StoreID:   s.ID,
		StoreURL:  storeURL,
	}, nil
}

// ---------------------------------------------------------------------------
// Products
// ---------------------------------------------------------------------------

// Products lists products from the store.
func (m *LemonSqueezyManager) Products(limit int) ([]LSProduct, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("page[size]", fmt.Sprintf("%d", limit))
	}

	data, err := m.lsAPIGet("/products", params)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		ID         string `json:"id"`
		Attributes struct {
			Name           string `json:"name"`
			Price          int    `json:"price"`
			PriceFormatted string `json:"price_formatted"`
			PriceCurrency  string `json:"price_currency"`
			Status         string `json:"status"`
			BuyNowURL      string `json:"buy_now_url"`
			CreatedAt      string `json:"created_at"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse products: %w", err)
	}

	out := make([]LSProduct, 0, len(raw))
	for _, r := range raw {
		out = append(out, LSProduct{
			ID:             r.ID,
			Name:           r.Attributes.Name,
			Price:          r.Attributes.Price,
			PriceFormatted: r.Attributes.PriceFormatted,
			Currency:       r.Attributes.PriceCurrency,
			Status:         r.Attributes.Status,
			BuyURL:         r.Attributes.BuyNowURL,
			CreatedAt:      r.Attributes.CreatedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Orders
// ---------------------------------------------------------------------------

// Orders lists orders, optionally filtered by email.
func (m *LemonSqueezyManager) Orders(limit int, email string) ([]LSOrder, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("page[size]", fmt.Sprintf("%d", limit))
	}
	if email != "" {
		params.Set("filter[user_email]", email)
	}

	data, err := m.lsAPIGet("/orders", params)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		ID         string `json:"id"`
		Attributes struct {
			UserEmail      string `json:"user_email"`
			Total          int    `json:"total"`
			TotalFormatted string `json:"total_formatted"`
			Status         string `json:"status"`
			CreatedAt      string `json:"created_at"`
			FirstOrderItem struct {
				ProductName string `json:"product_name"`
			} `json:"first_order_item"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse orders: %w", err)
	}

	out := make([]LSOrder, 0, len(raw))
	for _, r := range raw {
		out = append(out, LSOrder{
			ID:             r.ID,
			CustomerEmail:  r.Attributes.UserEmail,
			Total:          r.Attributes.Total,
			TotalFormatted: r.Attributes.TotalFormatted,
			Status:         r.Attributes.Status,
			CreatedAt:      r.Attributes.CreatedAt,
			ProductName:    r.Attributes.FirstOrderItem.ProductName,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

// Subscriptions lists subscriptions, optionally filtered by status.
// Valid statuses: active, cancelled, expired, past_due, on_trial, unpaid, paused
func (m *LemonSqueezyManager) Subscriptions(limit int, status string) ([]LSSubscription, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("page[size]", fmt.Sprintf("%d", limit))
	}
	if status != "" {
		params.Set("filter[status]", status)
	}

	data, err := m.lsAPIGet("/subscriptions", params)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		ID         string `json:"id"`
		Attributes struct {
			UserEmail   string `json:"user_email"`
			ProductName string `json:"product_name"`
			Status      string `json:"status"`
			RenewsAt    string `json:"renews_at"`
			CreatedAt   string `json:"created_at"`
			// Price comes from the first variant price
			FirstSubscriptionItem struct {
				Price    int    `json:"price"`
				Interval string `json:"billing_anchor"`
			} `json:"first_subscription_item"`
			Pause interface{} `json:"pause"`
		} `json:"attributes"`
		// Relationships hold the variant which has interval info
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse subscriptions: %w", err)
	}

	out := make([]LSSubscription, 0, len(raw))
	for _, r := range raw {
		out = append(out, LSSubscription{
			ID:            r.ID,
			CustomerEmail: r.Attributes.UserEmail,
			ProductName:   r.Attributes.ProductName,
			Status:        r.Attributes.Status,
			RenewsAt:      r.Attributes.RenewsAt,
			CreatedAt:     r.Attributes.CreatedAt,
			Price:         r.Attributes.FirstSubscriptionItem.Price,
			Interval:      r.Attributes.FirstSubscriptionItem.Interval,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Customers
// ---------------------------------------------------------------------------

// Customers lists customers, optionally filtered by email.
func (m *LemonSqueezyManager) Customers(limit int, email string) ([]LSCustomer, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("page[size]", fmt.Sprintf("%d", limit))
	}
	if email != "" {
		params.Set("filter[email]", email)
	}

	data, err := m.lsAPIGet("/customers", params)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		ID         string `json:"id"`
		Attributes struct {
			Name       string `json:"name"`
			Email      string `json:"email"`
			TotalSpent int    `json:"total_revenue_currency"`
			CreatedAt  string `json:"created_at"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse customers: %w", err)
	}

	out := make([]LSCustomer, 0, len(raw))
	for _, r := range raw {
		out = append(out, LSCustomer{
			ID:         r.ID,
			Name:       r.Attributes.Name,
			Email:      r.Attributes.Email,
			TotalSpent: r.Attributes.TotalSpent,
			CreatedAt:  r.Attributes.CreatedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Discounts
// ---------------------------------------------------------------------------

// Discounts lists discount codes.
func (m *LemonSqueezyManager) Discounts(limit int) ([]LSDiscount, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("page[size]", fmt.Sprintf("%d", limit))
	}

	data, err := m.lsAPIGet("/discounts", params)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		ID         string `json:"id"`
		Attributes struct {
			Name       string `json:"name"`
			Code       string `json:"code"`
			Amount     int    `json:"amount"`
			AmountType string `json:"amount_type"`
			IsLimited  bool   `json:"is_limited_redemptions"`
			UsageCount int    `json:"redemptions_count"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse discounts: %w", err)
	}

	out := make([]LSDiscount, 0, len(raw))
	for _, r := range raw {
		out = append(out, LSDiscount{
			ID:         r.ID,
			Name:       r.Attributes.Name,
			Code:       r.Attributes.Code,
			Amount:     r.Attributes.Amount,
			AmountType: r.Attributes.AmountType,
			IsLimited:  r.Attributes.IsLimited,
			UsageCount: r.Attributes.UsageCount,
		})
	}
	return out, nil
}

// CreateDiscount creates a new discount code.
// amountType is "percent" or "fixed".
// productID can be empty to apply to all products.
func (m *LemonSqueezyManager) CreateDiscount(name, code string, amount int, amountType, productID string) (*LSDiscount, error) {
	attrs := map[string]interface{}{
		"name":        name,
		"code":        code,
		"amount":      amount,
		"amount_type": amountType,
	}

	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "discounts",
			"attributes": attrs,
		},
	}

	if productID != "" {
		body["data"].(map[string]interface{})["relationships"] = map[string]interface{}{
			"variants": map[string]interface{}{
				"data": []map[string]interface{}{
					{"type": "variants", "id": productID},
				},
			},
		}
	}

	data, err := m.lsAPIPost("/discounts", body)
	if err != nil {
		return nil, err
	}

	var raw struct {
		ID         string `json:"id"`
		Attributes struct {
			Name       string `json:"name"`
			Code       string `json:"code"`
			Amount     int    `json:"amount"`
			AmountType string `json:"amount_type"`
			IsLimited  bool   `json:"is_limited_redemptions"`
			UsageCount int    `json:"redemptions_count"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse discount: %w", err)
	}

	return &LSDiscount{
		ID:         raw.ID,
		Name:       raw.Attributes.Name,
		Code:       raw.Attributes.Code,
		Amount:     raw.Attributes.Amount,
		AmountType: raw.Attributes.AmountType,
		IsLimited:  raw.Attributes.IsLimited,
		UsageCount: raw.Attributes.UsageCount,
	}, nil
}

// ---------------------------------------------------------------------------
// Revenue
// ---------------------------------------------------------------------------

// Revenue aggregates revenue stats from orders and subscriptions.
func (m *LemonSqueezyManager) Revenue() (*LSRevenue, error) {
	// Fetch all orders (up to 100 per page — good enough for small stores)
	orders, err := m.Orders(100, "")
	if err != nil {
		return nil, fmt.Errorf("fetch orders: %w", err)
	}

	// Fetch all subscriptions
	subs, err := m.Subscriptions(100, "")
	if err != nil {
		return nil, fmt.Errorf("fetch subscriptions: %w", err)
	}

	// Fetch customer count
	customers, err := m.Customers(1, "")
	if err != nil {
		// Non-fatal — just set 0
		customers = nil
	}

	rev := &LSRevenue{}

	// Aggregate order totals
	for _, o := range orders {
		if o.Status == "paid" || o.Status == "refunded" {
			rev.TotalRevenue += o.Total
		}
		rev.TotalOrders++
	}

	// Aggregate subscription stats + MRR
	for _, s := range subs {
		switch s.Status {
		case "active", "on_trial":
			rev.ActiveSubs++
			// Normalize price to monthly
			monthlyPrice := s.Price
			if s.Interval == "year" {
				monthlyPrice = s.Price / 12
			}
			rev.MRR += monthlyPrice
		case "cancelled":
			rev.CancelledSubs++
		}
	}

	// Customer count (approximate from the first-page meta)
	// If we fetched customers, use the length; the actual meta.total isn't surfaced here
	_ = customers
	// Count unique emails from orders + subs as a rough customer count
	emails := map[string]struct{}{}
	for _, o := range orders {
		if o.CustomerEmail != "" {
			emails[o.CustomerEmail] = struct{}{}
		}
	}
	for _, s := range subs {
		if s.CustomerEmail != "" {
			emails[s.CustomerEmail] = struct{}{}
		}
	}
	rev.TotalCustomers = len(emails)

	return rev, nil
}

// ---------------------------------------------------------------------------
// Webhook listener
// ---------------------------------------------------------------------------

const lsWebhookBufSize = 100

// WebhookListen starts a local HTTP server to receive Lemon Squeezy webhooks.
// Verifies X-Signature if a webhook secret is configured.
// Incoming payloads are stored in a ring buffer.
func (m *LemonSqueezyManager) WebhookListen(port int, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.webhookSrv != nil {
		return fmt.Errorf("webhook listener already running")
	}

	if path == "" {
		path = "/webhook"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, m.handleWebhook)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen on :%d: %w", port, err)
	}

	m.webhookSrv = srv
	m.webhookBuf = make([]string, 0, lsWebhookBufSize)

	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "[lemonsqueezy] webhook server error: %v\n", serveErr)
		}
	}()

	fmt.Printf("[lemonsqueezy] webhook listener started on :%d%s\n", port, path)
	return nil
}

// handleWebhook processes an incoming Lemon Squeezy webhook POST.
func (m *LemonSqueezyManager) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB max
	if err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}

	// Verify signature if webhook secret is configured
	if m.webhookSecret != "" {
		sig := r.Header.Get("X-Signature")
		if !lsVerifySignature(body, sig, m.webhookSecret) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	// Pretty-print for the log buffer
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		pretty.Write(body) // fallback to raw if not valid JSON
	}

	entry := fmt.Sprintf("[%s] %s\n%s", time.Now().Format(time.RFC3339), r.Header.Get("X-Event-Name"), pretty.String())
	fmt.Println("[lemonsqueezy webhook]", entry)

	m.mu.Lock()
	if len(m.webhookBuf) >= lsWebhookBufSize {
		m.webhookBuf = m.webhookBuf[1:] // drop oldest
	}
	m.webhookBuf = append(m.webhookBuf, entry)
	m.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

// lsVerifySignature validates the X-Signature header using HMAC-SHA256.
func lsVerifySignature(payload []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// WebhookStop stops the webhook listener.
func (m *LemonSqueezyManager) WebhookStop() error {
	m.mu.Lock()
	srv := m.webhookSrv
	m.webhookSrv = nil
	m.mu.Unlock()

	if srv == nil {
		return fmt.Errorf("webhook listener is not running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// WebhookLogs returns recent webhook payloads from the ring buffer.
func (m *LemonSqueezyManager) WebhookLogs() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.webhookBuf) == 0 {
		return "(no webhook events received yet)", nil
	}
	return strings.Join(m.webhookBuf, "\n---\n"), nil
}

// ---------------------------------------------------------------------------
// Setup (integration code)
// ---------------------------------------------------------------------------

// Setup returns Next.js integration code for webhook handling and checkout URL generation.
func (m *LemonSqueezyManager) Setup() string {
	return `// Lemon Squeezy integration for Next.js
// ---------------------------------------------------------------------------
// 1. Install the SDK
//    npm install @lemonsqueezy/lemonsqueezy.js
//
// 2. Set environment variables in .env.local
//    LEMONSQUEEZY_API_KEY=your_api_key_here
//    LEMONSQUEEZY_WEBHOOK_SECRET=your_webhook_secret_here
//    LEMONSQUEEZY_STORE_ID=your_store_id_here
// ---------------------------------------------------------------------------

// app/api/lemonsqueezy/webhook/route.ts
// ---------------------------------------------------------------------------
import crypto from 'crypto';
import { NextRequest, NextResponse } from 'next/server';

export async function POST(req: NextRequest) {
  const rawBody = await req.text();
  const signature = req.headers.get('X-Signature') ?? '';

  // Verify HMAC-SHA256 signature
  const secret = process.env.LEMONSQUEEZY_WEBHOOK_SECRET!;
  const hmac = crypto.createHmac('sha256', secret);
  hmac.update(rawBody);
  const digest = hmac.digest('hex');

  if (!crypto.timingSafeEqual(Buffer.from(digest), Buffer.from(signature))) {
    return NextResponse.json({ error: 'invalid signature' }, { status: 401 });
  }

  const payload = JSON.parse(rawBody);
  const eventName = req.headers.get('X-Event-Name');

  switch (eventName) {
    case 'order_created':
      await handleOrderCreated(payload);
      break;
    case 'subscription_created':
      await handleSubscriptionCreated(payload);
      break;
    case 'subscription_updated':
      await handleSubscriptionUpdated(payload);
      break;
    case 'subscription_cancelled':
      await handleSubscriptionCancelled(payload);
      break;
    default:
      console.log('Unhandled event:', eventName);
  }

  return NextResponse.json({ ok: true });
}

async function handleOrderCreated(payload: any) {
  const order = payload.data.attributes;
  // e.g. create user subscription record in DB
  console.log('New order:', order.user_email, order.total_formatted);
}

async function handleSubscriptionCreated(payload: any) {
  const sub = payload.data.attributes;
  console.log('New subscription:', sub.user_email, sub.status);
}

async function handleSubscriptionUpdated(payload: any) {
  const sub = payload.data.attributes;
  console.log('Subscription updated:', sub.user_email, sub.status);
}

async function handleSubscriptionCancelled(payload: any) {
  const sub = payload.data.attributes;
  console.log('Subscription cancelled:', sub.user_email);
}
// ---------------------------------------------------------------------------

// app/api/lemonsqueezy/checkout/route.ts
// ---------------------------------------------------------------------------
import { lemonSqueezySetup, createCheckout } from '@lemonsqueezy/lemonsqueezy.js';
import { NextRequest, NextResponse } from 'next/server';

export async function POST(req: NextRequest) {
  lemonSqueezySetup({ apiKey: process.env.LEMONSQUEEZY_API_KEY! });

  const { variantId, email } = await req.json();

  const { data, error } = await createCheckout(
    process.env.LEMONSQUEEZY_STORE_ID!,
    variantId,
    {
      checkoutOptions: {
        embed: false,
        media: true,
        logo: true,
      },
      checkoutData: {
        email,
        custom: {
          user_id: 'optional_internal_user_id',
        },
      },
      productOptions: {
        redirectUrl: process.env.NEXT_PUBLIC_APP_URL + '/dashboard',
        receiptButtonText: 'Go to Dashboard',
        receiptLinkUrl: process.env.NEXT_PUBLIC_APP_URL + '/dashboard',
      },
      expiresAt: null, // no expiry
    }
  );

  if (error) {
    return NextResponse.json({ error: error.message }, { status: 500 });
  }

  return NextResponse.json({ url: data?.data.attributes.url });
}
// ---------------------------------------------------------------------------

// Usage from a client component:
//
//   const res = await fetch('/api/lemonsqueezy/checkout', {
//     method: 'POST',
//     headers: { 'Content-Type': 'application/json' },
//     body: JSON.stringify({ variantId: '12345', email: user.email }),
//   });
//   const { url } = await res.json();
//   window.location.href = url; // redirect to Lemon Squeezy hosted checkout
`
}

// ---------------------------------------------------------------------------
// HTTP handlers (registered in httpserver.go via registerLemonSqueezyRoutes)
// ---------------------------------------------------------------------------

// RegisterLemonSqueezyRoutes wires all /lemonsqueezy/* endpoints onto mux.
func RegisterLemonSqueezyRoutes(mux *http.ServeMux, ls *LemonSqueezyManager) {
	mux.HandleFunc("/lemonsqueezy/status", ls.handleStatus)
	mux.HandleFunc("/lemonsqueezy/products", ls.handleProducts)
	mux.HandleFunc("/lemonsqueezy/orders", ls.handleOrders)
	mux.HandleFunc("/lemonsqueezy/subscriptions", ls.handleSubscriptions)
	mux.HandleFunc("/lemonsqueezy/customers", ls.handleCustomers)
	mux.HandleFunc("/lemonsqueezy/discounts", ls.handleDiscounts)
	mux.HandleFunc("/lemonsqueezy/revenue", ls.handleRevenue)
	mux.HandleFunc("/lemonsqueezy/setup", ls.handleSetup)
	mux.HandleFunc("/lemonsqueezy/webhook/start", ls.handleWebhookStart)
	mux.HandleFunc("/lemonsqueezy/webhook/stop", ls.handleWebhookStop)
	mux.HandleFunc("/lemonsqueezy/webhook/logs", ls.handleWebhookLogs)
}

func (m *LemonSqueezyManager) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := m.Status()
	if err != nil && st == nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Return status even on error (Connected=false)
	writeJSON(w, http.StatusOK, st)
}

func (m *LemonSqueezyManager) handleProducts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := lsQueryInt(r, "limit", 50)
	products, err := m.Products(limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, products)
}

func (m *LemonSqueezyManager) handleOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := lsQueryInt(r, "limit", 50)
	email := r.URL.Query().Get("email")
	orders, err := m.Orders(limit, email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func (m *LemonSqueezyManager) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := lsQueryInt(r, "limit", 50)
	status := r.URL.Query().Get("status")
	subs, err := m.Subscriptions(limit, status)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (m *LemonSqueezyManager) handleCustomers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := lsQueryInt(r, "limit", 50)
	email := r.URL.Query().Get("email")
	customers, err := m.Customers(limit, email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, customers)
}

func (m *LemonSqueezyManager) handleDiscounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := lsQueryInt(r, "limit", 50)
		discounts, err := m.Discounts(limit)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, discounts)

	case http.MethodPost:
		var req struct {
			Name       string `json:"name"`
			Code       string `json:"code"`
			Amount     int    `json:"amount"`
			AmountType string `json:"amountType"`
			ProductID  string `json:"productId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if req.Name == "" || req.Code == "" {
			jsonError(w, http.StatusBadRequest, "name and code are required")
			return
		}
		if req.AmountType == "" {
			req.AmountType = "percent"
		}
		d, err := m.CreateDiscount(req.Name, req.Code, req.Amount, req.AmountType, req.ProductID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, d)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *LemonSqueezyManager) handleRevenue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rev, err := m.Revenue()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

func (m *LemonSqueezyManager) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, m.Setup())
}

func (m *LemonSqueezyManager) handleWebhookStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Port int    `json:"port"`
		Path string `json:"path"`
	}
	req.Port = 9876 // default
	req.Path = "/webhook"
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if err := m.WebhookListen(req.Port, req.Path); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"listening": true,
		"port":      req.Port,
		"path":      req.Path,
		"url":       fmt.Sprintf("http://localhost:%d%s", req.Port, req.Path),
	})
}

func (m *LemonSqueezyManager) handleWebhookStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := m.WebhookStop(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (m *LemonSqueezyManager) handleWebhookLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logs, err := m.WebhookLogs()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, logs)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func lsQueryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}
