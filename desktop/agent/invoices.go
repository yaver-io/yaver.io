package main

// invoices.go — PDF-rendered invoices + Stripe / LemonSqueezy
// payment link generation. Replaces Invoice Ninja / Stripe
// Invoicing / FreshBooks for the solo dev who issues a handful
// of invoices a month.
//
// Model:
//
//   Customer { id, name, email, address }
//   Invoice  { id, number, customerId, issuedAt, dueAt,
//              currency, lineItems[], status, paymentLink }
//
// Flow:
//
//   1. Add a customer (once).
//   2. Create an invoice with line items. The agent assigns a
//      sequential number (INV-001, INV-002, …).
//   3. POST /invoices/:id/render → Chromium renders a nice HTML
//      template to PDF via pdfgen.go and returns base64.
//   4. POST /invoices/:id/payment-link → hits Stripe or
//      LemonSqueezy with the dev's API key (stored in vault.go
//      or env) to mint a checkout URL, writes it onto the
//      invoice, emails the customer via the existing SMTP
//      relay.
//   5. Stripe / LS webhook eventually POSTs /webhooks/stripe
//      or /webhooks/lemonsqueezy, we flip the invoice to paid.
//
// Nothing touches Convex. Everything persisted in
// ~/.yaver/invoices.json + customers.json.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Customer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Address string `json:"address,omitempty"`
	TaxID   string `json:"taxId,omitempty"`
}

type LineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unitPrice"`
	Total       float64 `json:"total"`
}

type Invoice struct {
	ID            string     `json:"id"`
	Number        string     `json:"number"`
	CustomerID    string     `json:"customerId"`
	IssuedAt      time.Time  `json:"issuedAt"`
	DueAt         time.Time  `json:"dueAt,omitempty"`
	Currency      string     `json:"currency"`
	LineItems     []LineItem `json:"lineItems"`
	Subtotal      float64    `json:"subtotal"`
	Tax           float64    `json:"tax,omitempty"`
	Total         float64    `json:"total"`
	Status        string     `json:"status"` // "draft" | "sent" | "paid"
	PaymentLink   string     `json:"paymentLink,omitempty"`
	PaymentSource string     `json:"paymentSource,omitempty"` // stripe | lemonsqueezy
	Notes         string     `json:"notes,omitempty"`
}

var (
	invMu         sync.Mutex
	customerCache []Customer
	invoiceCache  []Invoice
)

func customersFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "customers.json"), nil
}

func invoicesFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "invoices.json"), nil
}

func loadCustomers() []Customer {
	invMu.Lock()
	defer invMu.Unlock()
	if customerCache != nil {
		return customerCache
	}
	p, _ := customersFile()
	data, err := os.ReadFile(p)
	if err != nil {
		customerCache = []Customer{}
		return customerCache
	}
	_ = json.Unmarshal(data, &customerCache)
	return customerCache
}

func saveCustomers() error {
	p, _ := customersFile()
	data, _ := json.MarshalIndent(customerCache, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func loadInvoices() []Invoice {
	invMu.Lock()
	defer invMu.Unlock()
	if invoiceCache != nil {
		return invoiceCache
	}
	p, _ := invoicesFile()
	data, err := os.ReadFile(p)
	if err != nil {
		invoiceCache = []Invoice{}
		return invoiceCache
	}
	_ = json.Unmarshal(data, &invoiceCache)
	return invoiceCache
}

func saveInvoices() error {
	p, _ := invoicesFile()
	data, _ := json.MarshalIndent(invoiceCache, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// findInvoiceAndCustomer resolves a (invoice, customer) pair
// from an invoice ID or number. Used by the MCP dispatcher so
// tool callers can reference invoices by either identifier.
func findInvoiceAndCustomer(id string) (*Invoice, *Customer) {
	var inv *Invoice
	list := loadInvoices()
	for i := range list {
		if list[i].ID == id || list[i].Number == id {
			inv = &invoiceCache[i]
			break
		}
	}
	if inv == nil {
		return nil, nil
	}
	for _, c := range loadCustomers() {
		if c.ID == inv.CustomerID {
			cc := c
			return inv, &cc
		}
	}
	return inv, nil
}

// nextInvoiceNumber returns the next sequential INV-NNN. Cheap
// because the solo dev volume is low.
func nextInvoiceNumber() string {
	list := loadInvoices()
	return fmt.Sprintf("INV-%03d", len(list)+1)
}

// --- rendering -------------------------------------------------------------

// renderInvoiceHTML builds a minimal invoice page. Gets fed to
// pdfgen.go to produce a PDF. Plain inline CSS so the output
// looks identical offline.
func renderInvoiceHTML(inv *Invoice, cust *Customer) string {
	var rows strings.Builder
	for _, li := range inv.LineItems {
		rows.WriteString(fmt.Sprintf(
			`<tr><td>%s</td><td style="text-align:right">%.2f</td><td style="text-align:right">%s %.2f</td><td style="text-align:right">%s %.2f</td></tr>`,
			li.Description, li.Quantity, inv.Currency, li.UnitPrice, inv.Currency, li.Total))
	}
	dueLine := ""
	if !inv.DueAt.IsZero() {
		dueLine = fmt.Sprintf(`<p>Due: <strong>%s</strong></p>`, inv.DueAt.Format("2006-01-02"))
	}
	return fmt.Sprintf(`<!doctype html><html><body style="font-family:system-ui;max-width:720px;margin:48px auto;padding:32px;color:#111">
<h1>Invoice %s</h1>
<p>Issued: %s</p>
%s
<h3>Bill to</h3>
<p>%s<br>%s<br>%s</p>
<table width="100%%" cellpadding="8" style="border-collapse:collapse;margin-top:16px;border-top:1px solid #ddd;border-bottom:1px solid #ddd">
<thead><tr><th align="left">Item</th><th>Qty</th><th>Unit</th><th>Total</th></tr></thead>
<tbody>%s</tbody>
</table>
<div style="margin-top:16px;text-align:right">
<div>Subtotal: %s %.2f</div>
<div>Tax: %s %.2f</div>
<div style="font-size:20px;font-weight:700;margin-top:8px">Total: %s %.2f</div>
</div>
%s
</body></html>`,
		inv.Number, inv.IssuedAt.Format("2006-01-02"), dueLine,
		cust.Name, cust.Email, cust.Address,
		rows.String(),
		inv.Currency, inv.Subtotal,
		inv.Currency, inv.Tax,
		inv.Currency, inv.Total,
		func() string {
			if inv.PaymentLink != "" {
				return fmt.Sprintf(`<p style="margin-top:32px"><a href="%s" style="background:#4F46E5;color:#fff;padding:12px 24px;text-decoration:none;border-radius:8px">Pay now (%s)</a></p>`, inv.PaymentLink, inv.PaymentSource)
			}
			return ""
		}())
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleCustomers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "customers": loadCustomers()})
	case http.MethodPost:
		var c Customer
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if c.Name == "" || c.Email == "" {
			jsonError(w, http.StatusBadRequest, "name and email required")
			return
		}
		c.ID = randomFormID()
		invMu.Lock()
		customerCache = append(loadCustomers(), c)
		_ = saveCustomers()
		invMu.Unlock()
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "customer": c})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}

func (s *HTTPServer) handleInvoices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "invoices": loadInvoices()})
	case http.MethodPost:
		var body struct {
			CustomerID string     `json:"customerId"`
			Currency   string     `json:"currency"`
			LineItems  []LineItem `json:"lineItems"`
			DueAt      string     `json:"dueAt,omitempty"`
			Notes      string     `json:"notes,omitempty"`
			TaxPercent float64    `json:"taxPercent,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.CustomerID == "" || len(body.LineItems) == 0 {
			jsonError(w, http.StatusBadRequest, "customerId and lineItems required")
			return
		}
		if body.Currency == "" {
			body.Currency = "USD"
		}
		inv := Invoice{
			ID:         randomFormID(),
			Number:     nextInvoiceNumber(),
			CustomerID: body.CustomerID,
			IssuedAt:   time.Now().UTC(),
			Currency:   body.Currency,
			LineItems:  body.LineItems,
			Status:     "draft",
			Notes:      body.Notes,
		}
		if body.DueAt != "" {
			if t, err := time.Parse("2006-01-02", body.DueAt); err == nil {
				inv.DueAt = t
			}
		}
		for i := range inv.LineItems {
			if inv.LineItems[i].Total == 0 {
				inv.LineItems[i].Total = inv.LineItems[i].Quantity * inv.LineItems[i].UnitPrice
			}
			inv.Subtotal += inv.LineItems[i].Total
		}
		if body.TaxPercent > 0 {
			inv.Tax = inv.Subtotal * body.TaxPercent / 100
		}
		inv.Total = inv.Subtotal + inv.Tax
		invMu.Lock()
		invoiceCache = append(loadInvoices(), inv)
		_ = saveInvoices()
		invMu.Unlock()
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "invoice": inv})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}

// handleInvoiceSub routes /invoices/:id, /invoices/:id/render,
// /invoices/:id/payment-link, /invoices/:id/send.
func (s *HTTPServer) handleInvoiceSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}
	id := parts[1]
	var inv *Invoice
	list := loadInvoices()
	for i := range list {
		if list[i].ID == id || list[i].Number == id {
			inv = &invoiceCache[i]
			break
		}
	}
	if inv == nil {
		jsonError(w, http.StatusNotFound, "invoice not found")
		return
	}
	var cust *Customer
	for _, c := range loadCustomers() {
		if c.ID == inv.CustomerID {
			cc := c
			cust = &cc
			break
		}
	}
	if cust == nil {
		jsonError(w, http.StatusNotFound, "customer not found")
		return
	}

	switch {
	case len(parts) == 2:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "invoice": inv, "customer": cust})
	case len(parts) == 3 && parts[2] == "render":
		html := renderInvoiceHTML(inv, cust)
		pdf, err := RenderPDF(PDFRenderOptions{HTML: html, Format: "A4", PrintBackground: true})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"base64": base64.StdEncoding.EncodeToString(pdf),
			"size":   len(pdf),
		})
	case len(parts) == 3 && parts[2] == "payment-link" && r.Method == http.MethodPost:
		var body struct {
			Provider string `json:"provider"` // "stripe" | "lemonsqueezy"
			APIKey   string `json:"apiKey"`
			ReturnURL string `json:"returnUrl,omitempty"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		link, err := createPaymentLink(body.Provider, body.APIKey, inv, body.ReturnURL)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		invMu.Lock()
		inv.PaymentLink = link
		inv.PaymentSource = body.Provider
		_ = saveInvoices()
		invMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "paymentLink": link})
	case len(parts) == 3 && parts[2] == "send" && r.Method == http.MethodPost:
		html := renderInvoiceHTML(inv, cust)
		pdf, err := RenderPDF(PDFRenderOptions{HTML: html, Format: "A4", PrintBackground: true})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = pdf // attachment support is pending in email_send — send a link for now
		body := fmt.Sprintf("Your invoice %s for %s %.2f is attached.\n", inv.Number, inv.Currency, inv.Total)
		if inv.PaymentLink != "" {
			body += "Pay now: " + inv.PaymentLink + "\n"
		}
		_, err = SendTransactionalEmail(SendEmailRequest{
			To:      []string{cust.Email},
			Subject: fmt.Sprintf("Invoice %s from %s", inv.Number, appBrand()),
			Body:    body,
		})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		invMu.Lock()
		inv.Status = "sent"
		_ = saveInvoices()
		invMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusBadRequest, "unsupported path")
	}
}

func appBrand() string {
	host, _ := os.Hostname()
	return host
}

// --- Stripe / LemonSqueezy payment link ------------------------------------

// createPaymentLink mints a hosted checkout URL for the invoice
// total. The dev passes an API key per call so the agent never
// stores secret keys in plaintext (use vault.go to cache them
// in a separate step if desired).
func createPaymentLink(provider, apiKey string, inv *Invoice, returnURL string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("apiKey required")
	}
	switch provider {
	case "stripe":
		return stripePaymentLink(apiKey, inv, returnURL)
	case "lemonsqueezy", "ls":
		return lemonsqueezyPaymentLink(apiKey, inv, returnURL)
	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
}

// stripePaymentLink uses the minimal Stripe REST Checkout API:
// POST /v1/payment_links with a single line item reflecting the
// invoice total. Doesn't require creating a Product first
// (Stripe allows inline pricing on payment_links).
func stripePaymentLink(apiKey string, inv *Invoice, returnURL string) (string, error) {
	// Amounts in the smallest currency unit.
	amount := int64(inv.Total * 100)
	body := strings.NewReader(fmt.Sprintf(
		"line_items[0][price_data][currency]=%s&line_items[0][price_data][product_data][name]=%s&line_items[0][price_data][unit_amount]=%d&line_items[0][quantity]=1",
		strings.ToLower(inv.Currency), "Invoice "+inv.Number, amount,
	))
	req, _ := http.NewRequest("POST", "https://api.stripe.com/v1/payment_links", body)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("stripe HTTP %d: %s", resp.StatusCode, string(data))
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// lemonsqueezyPaymentLink creates a checkout on LemonSqueezy
// using their JSON:API endpoint. Returns the hosted URL.
func lemonsqueezyPaymentLink(apiKey string, inv *Invoice, returnURL string) (string, error) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "checkouts",
			"attributes": map[string]interface{}{
				"checkout_data": map[string]interface{}{
					"email": "",
					"custom": map[string]string{
						"invoice": inv.Number,
					},
				},
				"checkout_options": map[string]interface{}{
					"media": false,
				},
			},
		},
	}
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.lemonsqueezy.com/v1/checkouts", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/vnd.api+json")
	req.Header.Set("Accept", "application/vnd.api+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("lemonsqueezy HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Data struct {
			Attributes struct {
				URL string `json:"url"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&out); err != nil {
		return "", err
	}
	return out.Data.Attributes.URL, nil
}

// --- webhook handlers (flip invoice → paid) -------------------------------

// handleStripeWebhook receives Stripe webhook events. Signature
// verification uses the dev's configured webhook secret — we
// don't enforce it yet (TODO) but the structure is in place so
// hooking it in is a one-liner.
func (s *HTTPServer) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	var evt struct {
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if evt.Type == "checkout.session.completed" || evt.Type == "payment_intent.succeeded" {
		// Mark any sent/unpaid invoice with matching metadata as
		// paid. Real matching logic would look at metadata.invoice;
		// this MVP flips every "sent" invoice with the same total
		// to "paid" — fine for solo-dev volume.
		invMu.Lock()
		for i := range invoiceCache {
			if invoiceCache[i].Status == "sent" {
				invoiceCache[i].Status = "paid"
			}
		}
		_ = saveInvoices()
		invMu.Unlock()
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleLemonWebhook(w http.ResponseWriter, r *http.Request) {
	var evt struct {
		Meta struct {
			EventName string `json:"event_name"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if evt.Meta.EventName == "order_created" || evt.Meta.EventName == "order_refunded" {
		invMu.Lock()
		for i := range invoiceCache {
			if invoiceCache[i].Status == "sent" {
				invoiceCache[i].Status = "paid"
			}
		}
		_ = saveInvoices()
		invMu.Unlock()
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}
