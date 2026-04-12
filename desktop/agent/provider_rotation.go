package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProviderRotateResult describes what happened when rotating a credential
// at the upstream provider (not just in .env.local).
type ProviderRotateResult struct {
	Provider    string            `json:"provider"`
	Action      string            `json:"action"`
	OK          bool              `json:"ok"`
	NewSecret   string            `json:"newSecret,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
	Error       string            `json:"error,omitempty"`
	ManualSteps []string          `json:"manualSteps,omitempty"`
}

// RotateProvider rotates a credential at a cloud/SaaS provider using stored
// account tokens. Implemented: stripe (webhook secret), aws (IAM access key),
// cloudflare (API token rotation). Others return guidance.
func RotateProvider(provider, action string, opts map[string]string) *ProviderRotateResult {
	r := &ProviderRotateResult{Provider: provider, Action: action}
	switch provider {
	case "stripe":
		return rotateStripeWebhook(r, opts)
	case "aws":
		return rotateAWSAccessKey(r, opts)
	case "cloudflare":
		return rotateCloudflareToken(r, opts)
	case "vercel":
		r.Error = "Vercel tokens can only be rotated in the web dashboard (no API)."
		r.ManualSteps = []string{"https://vercel.com/account/tokens — create new, delete old, run `vercel login`"}
		return r
	case "github":
		r.Error = "GitHub PATs can't be created via API (requires user-mediated OAuth or fine-grained-tokens flow)."
		r.ManualSteps = []string{"https://github.com/settings/tokens — generate new, copy, then `yaver account_connect github`"}
		return r
	case "hetzner":
		return rotateHetznerToken(r, opts)
	}
	r.Error = fmt.Sprintf("provider rotation not supported for %q", provider)
	return r
}

// ---- Stripe: rotate webhook endpoint's signing secret ----

func rotateStripeWebhook(r *ProviderRotateResult, opts map[string]string) *ProviderRotateResult {
	stripeSecret := opts["apiKey"]
	if stripeSecret == "" {
		// Check env / accounts.
		if s := accountField("stripe", "token"); s != "" {
			stripeSecret = s
		}
	}
	if stripeSecret == "" {
		r.Error = "Stripe secret key required (pass apiKey or connect account 'stripe')"
		return r
	}
	webhookID := opts["webhookId"]
	if webhookID == "" {
		r.Error = "webhookId required (we_xxx — find at https://dashboard.stripe.com/webhooks)"
		return r
	}
	// POST /v1/webhook_endpoints/{id} with disabled=false triggers no rotation.
	// Stripe lacks a public rotate endpoint; the canonical flow is:
	//   1. Create a new webhook endpoint with same URL
	//   2. Switch app to new secret
	//   3. Delete old endpoint
	// We do steps 1 and 3 only if the caller passes `url=https://…`.
	targetURL := opts["url"]
	if targetURL == "" {
		r.Error = "url required (target webhook URL)"
		r.ManualSteps = []string{
			"Stripe has no rotate-webhook API. Workflow:",
			"1. Create a new webhook endpoint (this tool can: pass url=https://myapp.com/api/webhook)",
			"2. Update your app's STRIPE_WEBHOOK_SECRET to the new value",
			"3. Delete the old webhook (dashboard or pass deleteId=we_old)",
		}
		return r
	}
	form := url.Values{}
	form.Set("url", targetURL)
	for _, ev := range strings.Split(opts["events"], ",") {
		if ev = strings.TrimSpace(ev); ev != "" {
			form.Add("enabled_events[]", ev)
		}
	}
	if len(form["enabled_events[]"]) == 0 {
		form.Add("enabled_events[]", "*")
	}
	req, _ := http.NewRequest("POST", "https://api.stripe.com/v1/webhook_endpoints",
		strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+stripeSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		r.Error = fmt.Sprintf("stripe: %d %s", res.StatusCode, strings.TrimSpace(string(data)))
		return r
	}
	var out struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(data, &out)
	r.OK = true
	r.NewSecret = out.Secret
	r.Details = map[string]string{"webhookId": out.ID, "url": targetURL}
	// Optional: delete old.
	if del := opts["deleteId"]; del != "" {
		delReq, _ := http.NewRequest("DELETE", "https://api.stripe.com/v1/webhook_endpoints/"+del, nil)
		delReq.Header.Set("Authorization", "Bearer "+stripeSecret)
		_, _ = (&http.Client{Timeout: 10 * time.Second}).Do(delReq)
		r.Details["deleted"] = del
	}
	return r
}

// ---- AWS IAM: create new access key for current user, deactivate old ----

func rotateAWSAccessKey(r *ProviderRotateResult, opts map[string]string) *ProviderRotateResult {
	username := opts["username"]
	if username == "" {
		r.Error = "username required (IAM user name)"
		return r
	}
	access := accountField("aws", "accessKey")
	secret := accountField("aws", "secretKey")
	if access == "" || secret == "" {
		r.Error = "AWS creds not connected (yaver account_connect aws accessKey=... secretKey=...)"
		return r
	}
	// IAM CreateAccessKey call — SigV4 against iam.amazonaws.com.
	body := url.Values{}
	body.Set("Action", "CreateAccessKey")
	body.Set("Version", "2010-05-08")
	body.Set("UserName", username)
	reqBody := []byte(body.Encode())
	req, _ := http.NewRequest("POST", "https://iam.amazonaws.com/",
		bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signSigV4(req, access, secret, "us-east-1", "iam", reqBody)
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		r.Error = fmt.Sprintf("iam: %d %s", res.StatusCode, strings.TrimSpace(string(data)))
		return r
	}
	// Minimal XML parse: pull AccessKeyId + SecretAccessKey out.
	body_s := string(data)
	newKey := between(body_s, "<AccessKeyId>", "</AccessKeyId>")
	newSecret := between(body_s, "<SecretAccessKey>", "</SecretAccessKey>")
	r.OK = true
	r.NewSecret = newSecret
	r.Details = map[string]string{"accessKeyId": newKey, "username": username}
	r.ManualSteps = []string{
		"Update Yaver's stored AWS creds: yaver account_connect aws accessKey=" + newKey + " secretKey=...",
		"After verifying the new key works, deactivate the old key via IAM console or `aws iam update-access-key --status Inactive --access-key-id <OLD>`",
	}
	return r
}

// ---- Cloudflare: create new API token with same policy, revoke old ----

func rotateCloudflareToken(r *ProviderRotateResult, opts map[string]string) *ProviderRotateResult {
	existing := accountField("cloudflare", "token")
	if existing == "" {
		r.Error = "Cloudflare token not connected"
		return r
	}
	// Cloudflare's "roll" endpoint: PUT /user/tokens/{id}/value returns a new token.
	tokenID := opts["tokenId"]
	if tokenID == "" {
		r.Error = "tokenId required (get from https://dash.cloudflare.com/profile/api-tokens)"
		return r
	}
	req, _ := http.NewRequest("PUT",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/user/tokens/%s/value", tokenID), nil)
	req.Header.Set("Authorization", "Bearer "+existing)
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		r.Error = fmt.Sprintf("cf: %d %s", res.StatusCode, strings.TrimSpace(string(data)))
		return r
	}
	var out struct {
		Result  string `json:"result"`
		Success bool   `json:"success"`
	}
	_ = json.Unmarshal(data, &out)
	r.OK = out.Success
	r.NewSecret = out.Result
	r.ManualSteps = []string{
		"yaver account_connect cloudflare token=" + out.Result,
	}
	return r
}

// ---- Hetzner: create new token, revoke old ----

func rotateHetznerToken(r *ProviderRotateResult, opts map[string]string) *ProviderRotateResult {
	r.Error = "Hetzner API does not expose token rotation; create a new one in the console and run `yaver account_connect hetzner token=<new>`"
	r.ManualSteps = []string{
		"https://console.hetzner.cloud/projects → Security → API Tokens → Create new",
		"yaver account_connect hetzner token=<new>",
		"Delete the old token after confirming things still work",
	}
	return r
}

// between extracts the first substring between open and close markers.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	s = s[i+len(open):]
	j := strings.Index(s, close)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// ---- HTTP ----

func (s *HTTPServer) handleProviderRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Provider string            `json:"provider"`
		Action   string            `json:"action"`
		Opts     map[string]string `json:"opts"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res := RotateProvider(b.Provider, b.Action, b.Opts)
	AuditLog("", "provider_rotate", b.Provider, b.Action, outcomeBool(res.OK), res.Error, "")
	writeJSON(w, http.StatusOK, res)
}

func outcomeBool(ok bool) string {
	if ok {
		return "success"
	}
	return "failed"
}
