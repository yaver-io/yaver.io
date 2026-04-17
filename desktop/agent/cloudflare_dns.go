package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Cloudflare DNS helper — the minimum surface needed for the phone-first
// "point my custom domain at Yaver Cloud" UX (PHONE_EXPORT_PIPELINE.md
// §Handoff 1.5). User pastes a scoped API token (Zone:DNS:Edit), we list
// their zones, let them pick one, create/delete records. No OAuth — CF
// doesn't support OAuth-for-API-key and their scoped tokens are the
// recommended flow anyway.
//
// Token never lives in a YAML on disk — it's passed per-request via the
// X-CF-Token header (UI pastes once, mobile/web vault stores it
// client-side). That keeps every Cloudflare call the developer's
// explicit action and leaves no shared credential in the agent's
// persisted state.

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// CloudflareClient wraps a single API token. Zero-value is safe for
// dependency injection in tests that replace BaseURL + HTTP.
type CloudflareClient struct {
	Token   string
	BaseURL string        // override for tests; empty = cloudflareAPI
	HTTP    *http.Client  // override for tests; nil = 15s default
}

func NewCloudflareClient(token string) *CloudflareClient {
	return &CloudflareClient{Token: token, BaseURL: cloudflareAPI}
}

func (c *CloudflareClient) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return cloudflareAPI
}

func (c *CloudflareClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfEnvelope[T any] struct {
	Success bool       `json:"success"`
	Errors  []cfError  `json:"errors"`
	Result  T          `json:"result"`
}

func (c *CloudflareClient) do(method, path string, body interface{}) ([]byte, int, error) {
	if strings.TrimSpace(c.Token) == "" {
		return nil, 0, fmt.Errorf("cloudflare token required")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base()+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudflare %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// ---- Public types ----

// CloudflareZone is a DNS zone owned by the token.
type CloudflareZone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // active, pending, etc.
}

// CloudflareRecord is one DNS record in a zone.
type CloudflareRecord struct {
	ID      string `json:"id"`
	ZoneID  string `json:"zone_id,omitempty"`
	Type    string `json:"type"`           // A, AAAA, CNAME, TXT, MX, ...
	Name    string `json:"name"`           // FQDN (e.g. myapp.example.com)
	Content string `json:"content"`        // IP / target / value
	TTL     int    `json:"ttl,omitempty"`  // 1 = auto
	Proxied bool   `json:"proxied,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// CloudflareRecordInput is the write-shape for Create / Update.
type CloudflareRecordInput struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// TokenStatus reports whether the token is usable — used by the mobile UI
// to give a green check before the user commits to a save.
type CloudflareTokenStatus struct {
	Valid   bool   `json:"valid"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

// ---- Operations ----

// VerifyToken hits /user/tokens/verify. Returns {valid: true} only when the
// token is active AND has at least Zone:DNS:Read.
func (c *CloudflareClient) VerifyToken() (*CloudflareTokenStatus, error) {
	data, status, err := c.do(http.MethodGet, "/user/tokens/verify", nil)
	if err != nil {
		return nil, err
	}
	var env cfEnvelope[struct {
		Status string `json:"status"`
	}]
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decode verify: %w (body: %s)", err, string(data))
	}
	out := &CloudflareTokenStatus{Status: env.Result.Status, Valid: env.Success && status == http.StatusOK}
	if !env.Success && len(env.Errors) > 0 {
		out.Message = env.Errors[0].Message
	}
	return out, nil
}

// ListZones returns every zone the token can read. Paginated — CF returns
// up to 50 per page by default, we walk up to 10 pages (500 zones is more
// than any real Yaver user has).
func (c *CloudflareClient) ListZones() ([]CloudflareZone, error) {
	var out []CloudflareZone
	for page := 1; page <= 10; page++ {
		path := fmt.Sprintf("/zones?page=%d&per_page=50", page)
		data, _, err := c.do(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var env cfEnvelope[[]CloudflareZone]
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("decode zones: %w", err)
		}
		if !env.Success {
			return nil, cfEnvelopeError(env.Errors, "list zones")
		}
		out = append(out, env.Result...)
		if len(env.Result) < 50 {
			break
		}
	}
	return out, nil
}

// ListRecords enumerates DNS records in a zone. Paginated same as zones.
func (c *CloudflareClient) ListRecords(zoneID string) ([]CloudflareRecord, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID required")
	}
	var out []CloudflareRecord
	for page := 1; page <= 10; page++ {
		path := fmt.Sprintf("/zones/%s/dns_records?page=%d&per_page=100", zoneID, page)
		data, _, err := c.do(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var env cfEnvelope[[]CloudflareRecord]
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("decode records: %w", err)
		}
		if !env.Success {
			return nil, cfEnvelopeError(env.Errors, "list records")
		}
		out = append(out, env.Result...)
		if len(env.Result) < 100 {
			break
		}
	}
	return out, nil
}

// CreateRecord creates a DNS record. The minimal happy path for Yaver's
// "custom domain" flow is {type:"CNAME", name:"myapp.example.com",
// content:"cloud.yaver.io", proxied:true}.
func (c *CloudflareClient) CreateRecord(zoneID string, input CloudflareRecordInput) (*CloudflareRecord, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID required")
	}
	if input.Type == "" || input.Name == "" || input.Content == "" {
		return nil, fmt.Errorf("type, name, content required")
	}
	data, _, err := c.do(http.MethodPost, "/zones/"+zoneID+"/dns_records", input)
	if err != nil {
		return nil, err
	}
	var env cfEnvelope[CloudflareRecord]
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decode create: %w", err)
	}
	if !env.Success {
		return nil, cfEnvelopeError(env.Errors, "create record")
	}
	return &env.Result, nil
}

// DeleteRecord removes a record by ID.
func (c *CloudflareClient) DeleteRecord(zoneID, recordID string) error {
	if zoneID == "" || recordID == "" {
		return fmt.Errorf("zoneID and recordID required")
	}
	data, _, err := c.do(http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil)
	if err != nil {
		return err
	}
	var env cfEnvelope[struct {
		ID string `json:"id"`
	}]
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode delete: %w", err)
	}
	if !env.Success {
		return cfEnvelopeError(env.Errors, "delete record")
	}
	return nil
}

func cfEnvelopeError(errs []cfError, op string) error {
	if len(errs) == 0 {
		return fmt.Errorf("cloudflare %s: unknown error", op)
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
	}
	return fmt.Errorf("cloudflare %s: %s", op, strings.Join(msgs, "; "))
}

// ---- HTTP handlers (auth'd under /dns/cloudflare/*) ----

// tokenFromRequest extracts the Cloudflare API token. Priority: X-CF-Token
// header → ?token= query param → JSON body "token" field. The agent never
// persists it.
func cloudflareTokenFromRequest(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get("X-CF-Token")); t != "" {
		return t
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// registerDNSRoutes wires /dns/* endpoints. Called from registerRoutes.
func (s *HTTPServer) registerDNSRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dns/cloudflare/verify", s.auth(s.handleCFVerify))
	mux.HandleFunc("/dns/cloudflare/zones", s.auth(s.handleCFZones))
	mux.HandleFunc("/dns/cloudflare/records", s.auth(s.handleCFRecords))
}

func (s *HTTPServer) handleCFVerify(w http.ResponseWriter, r *http.Request) {
	tok := cloudflareTokenFromRequest(r)
	if tok == "" {
		if body := parseTokenBody(r); body != "" {
			tok = body
		}
	}
	if tok == "" {
		jsonError(w, http.StatusBadRequest, "token required (X-CF-Token header or ?token= query param)")
		return
	}
	cli := NewCloudflareClient(tok)
	res, err := cli.VerifyToken()
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleCFZones(w http.ResponseWriter, r *http.Request) {
	tok := cloudflareTokenFromRequest(r)
	if tok == "" {
		jsonError(w, http.StatusBadRequest, "X-CF-Token required")
		return
	}
	cli := NewCloudflareClient(tok)
	zones, err := cli.ListZones()
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"zones": zones})
}

// handleCFRecords serves GET (list) + POST (create) + DELETE for
// /dns/cloudflare/records. Zone ID is always required.
func (s *HTTPServer) handleCFRecords(w http.ResponseWriter, r *http.Request) {
	tok := cloudflareTokenFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		zoneID := r.URL.Query().Get("zoneId")
		if tok == "" || zoneID == "" {
			jsonError(w, http.StatusBadRequest, "X-CF-Token and zoneId required")
			return
		}
		cli := NewCloudflareClient(tok)
		recs, err := cli.ListRecords(zoneID)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"records": recs})
	case http.MethodPost:
		var body struct {
			ZoneID string                  `json:"zoneId"`
			Record CloudflareRecordInput   `json:"record"`
			Token  string                  `json:"token,omitempty"` // fallback if no header
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if tok == "" {
			tok = body.Token
		}
		if tok == "" || body.ZoneID == "" {
			jsonError(w, http.StatusBadRequest, "X-CF-Token and zoneId required")
			return
		}
		cli := NewCloudflareClient(tok)
		rec, err := cli.CreateRecord(body.ZoneID, body.Record)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"record": rec})
	case http.MethodDelete:
		zoneID := r.URL.Query().Get("zoneId")
		recID := r.URL.Query().Get("recordId")
		if tok == "" || zoneID == "" || recID == "" {
			jsonError(w, http.StatusBadRequest, "X-CF-Token, zoneId, recordId required")
			return
		}
		cli := NewCloudflareClient(tok)
		if err := cli.DeleteRecord(zoneID, recID); err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET / POST / DELETE")
	}
}

// parseTokenBody peeks into a JSON body for a "token" field, then resets the
// body so the primary handler can re-decode. Kept small — only used by
// /dns/cloudflare/verify which has a 1-field body.
func parseTokenBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1024))
	_ = r.Body.Close()
	if err != nil || len(data) == 0 {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Token)
}
