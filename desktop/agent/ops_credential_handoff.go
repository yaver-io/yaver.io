package main

// One-time P2P credential handoff receiver. The sender encrypts directly to an
// ephemeral endpoint key using the same NaCl box format as the native clients.
// No plaintext provider credential appears in an ops schema, response, log,
// relay-visible envelope, or device directory. Every handoff verb is direct-
// transport-only; the ordinary relay/chat connection may remain active beside
// this short-lived LAN or private-overlay channel.

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const credentialHandoffLifetime = 2 * time.Minute

type endpointCredentialHandoff struct {
	DeviceID           string
	AccountFingerprint string
	ExpiresAt          time.Time
	PublicKey          *[32]byte
	PrivateKey         *[32]byte
}

var endpointCredentialHandoffs = struct {
	sync.Mutex
	items map[string]endpointCredentialHandoff
}{items: map[string]endpointCredentialHandoff{}}

var credentialHandoffDirectoryHTTPClient = httpClient

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "credential_handoff_request",
		Description: "Create a short-lived endpoint public-key request for same-account, direct encrypted credential handoff. Returns public metadata only.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false},
		Handler:     opsCredentialHandoffRequest,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "credential_handoff_accept",
		Description: "Accept a one-time authenticated ciphertext encrypted for this endpoint. The only supported kind is hetzner-api-token; plaintext is written directly to the local vault and never returned.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"version", "type", "handoffId", "targetDeviceId", "accountFingerprint", "senderPublicKey", "nonce", "ciphertext"},
			"properties": map[string]interface{}{
				"version":            map[string]interface{}{"type": "integer", "const": 1},
				"type":               map[string]interface{}{"type": "string", "const": "yaver-credential-envelope"},
				"handoffId":          map[string]interface{}{"type": "string"},
				"targetDeviceId":     map[string]interface{}{"type": "string"},
				"accountFingerprint": map[string]interface{}{"type": "string"},
				"senderPublicKey":    map[string]interface{}{"type": "string"},
				"nonce":              map[string]interface{}{"type": "string"},
				"ciphertext":         map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: opsCredentialHandoffAccept,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "credential_handoff_offer",
		Description: "Encrypt the endpoint's locally stored Hetzner token to a same-account, directory-verified phone key. Safe across relay because only phone-targeted ciphertext is returned; plaintext never leaves the endpoint vault.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"version", "type", "handoffId", "targetDeviceId", "targetPublicKey", "accountFingerprint", "createdAt", "expiresAt"},
			"properties": map[string]interface{}{
				"version":            map[string]interface{}{"type": "integer", "const": 1},
				"type":               map[string]interface{}{"type": "string", "const": "yaver-credential-request"},
				"handoffId":          map[string]interface{}{"type": "string"},
				"targetDeviceId":     map[string]interface{}{"type": "string"},
				"targetPublicKey":    map[string]interface{}{"type": "string"},
				"accountFingerprint": map[string]interface{}{"type": "string"},
				"createdAt":          map[string]interface{}{"type": "integer"},
				"expiresAt":          map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler: opsCredentialHandoffOffer,
	})
}

func credentialAccountFingerprintGo(accountID string) string {
	sum := sha512.Sum512([]byte("yaver-credential-account-v1\x00" + strings.TrimSpace(accountID)))
	return base64.RawURLEncoding.EncodeToString(sum[:18])
}

func opsCredentialHandoffRequest(c OpsContext, _ json.RawMessage) OpsResult {
	if c.RequestHeaders != nil && c.RequestHeaders.Get("X-Yaver-Via-Relay") == "1" {
		return OpsResult{OK: false, Code: "secure_transport_required", Error: "credential handoff requires a direct LAN or private-overlay connection"}
	}
	if c.Server == nil || strings.TrimSpace(c.Server.ownerUserID) == "" || c.Server.ownerUserID == "offline" {
		return OpsResult{OK: false, Code: "account_required", Error: "a verified owner account is required"}
	}
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return OpsResult{OK: false, Code: "crypto_unavailable", Error: "could not create an endpoint handoff key"}
	}
	randomID := make([]byte, 18)
	if _, err = rand.Read(randomID); err != nil {
		return OpsResult{OK: false, Code: "crypto_unavailable", Error: "could not create a handoff id"}
	}
	now := time.Now()
	id := base64.RawURLEncoding.EncodeToString(randomID)
	fingerprint := credentialAccountFingerprintGo(c.Server.ownerUserID)
	endpointCredentialHandoffs.Lock()
	for key, pending := range endpointCredentialHandoffs.items {
		if now.After(pending.ExpiresAt) {
			delete(endpointCredentialHandoffs.items, key)
		}
	}
	endpointCredentialHandoffs.items[id] = endpointCredentialHandoff{
		DeviceID: c.Server.deviceID, AccountFingerprint: fingerprint,
		ExpiresAt: now.Add(credentialHandoffLifetime), PublicKey: pub, PrivateKey: priv,
	}
	endpointCredentialHandoffs.Unlock()
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"version": 1, "type": "yaver-credential-request", "handoffId": id,
		"targetDeviceId":     c.Server.deviceID,
		"targetPublicKey":    base64.StdEncoding.EncodeToString(pub[:]),
		"accountFingerprint": fingerprint,
		"createdAt":          now.UnixMilli(), "expiresAt": now.Add(credentialHandoffLifetime).UnixMilli(),
	}}
}

type endpointHandoffEnvelope struct {
	Version            int    `json:"version"`
	Type               string `json:"type"`
	HandoffID          string `json:"handoffId"`
	TargetDeviceID     string `json:"targetDeviceId"`
	AccountFingerprint string `json:"accountFingerprint"`
	SenderPublicKey    string `json:"senderPublicKey"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type endpointHandoffRequest struct {
	Version            int    `json:"version"`
	Type               string `json:"type"`
	HandoffID          string `json:"handoffId"`
	TargetDeviceID     string `json:"targetDeviceId"`
	TargetPublicKey    string `json:"targetPublicKey"`
	AccountFingerprint string `json:"accountFingerprint"`
	CreatedAt          int64  `json:"createdAt"`
	ExpiresAt          int64  `json:"expiresAt"`
}

type endpointHandoffPlaintext struct {
	Version            int    `json:"version"`
	HandoffID          string `json:"handoffId"`
	TargetDeviceID     string `json:"targetDeviceId"`
	AccountFingerprint string `json:"accountFingerprint"`
	CreatedAt          int64  `json:"createdAt"`
	ExpiresAt          int64  `json:"expiresAt"`
	Kind               string `json:"kind"`
	Value              string `json:"value"`
}

func opsCredentialHandoffOffer(c OpsContext, raw json.RawMessage) OpsResult {
	if c.RequestHeaders != nil && c.RequestHeaders.Get("X-Yaver-Via-Relay") == "1" {
		return OpsResult{OK: false, Code: "secure_transport_required", Error: "credential handoff requires a direct LAN or private-overlay connection"}
	}
	if c.Server == nil || strings.TrimSpace(c.Server.ownerUserID) == "" || c.Server.ownerUserID == "offline" {
		return OpsResult{OK: false, Code: "account_required", Error: "a verified owner account is required"}
	}
	var request endpointHandoffRequest
	now := time.Now()
	if json.Unmarshal(raw, &request) != nil || request.Version != 1 || request.Type != "yaver-credential-request" ||
		strings.TrimSpace(request.HandoffID) == "" || strings.TrimSpace(request.TargetDeviceID) == "" ||
		request.ExpiresAt <= request.CreatedAt || now.UnixMilli() >= request.ExpiresAt ||
		request.ExpiresAt-now.UnixMilli() > credentialHandoffLifetime.Milliseconds() {
		return OpsResult{OK: false, Code: "handoff_malformed", Error: "invalid or expired credential handoff request"}
	}
	expectedFingerprint := credentialAccountFingerprintGo(c.Server.ownerUserID)
	if request.AccountFingerprint != expectedFingerprint {
		return OpsResult{OK: false, Code: "handoff_binding_failed", Error: "credential handoff account binding failed"}
	}
	registered, err := credentialHandoffReceiverRegistered(c.Ctx, c.Server.convexURL, c.Server.token, request)
	if err != nil {
		return OpsResult{OK: false, Code: "handoff_directory_unavailable", Error: "could not verify the receiving phone with the same-account handoff directory: " + err.Error()}
	}
	if !registered {
		return OpsResult{OK: false, Code: "handoff_binding_failed", Error: "the receiving phone key is not registered to this Yaver account"}
	}
	recipientBytes, err := base64.StdEncoding.DecodeString(request.TargetPublicKey)
	if err != nil || len(recipientBytes) != 32 {
		return OpsResult{OK: false, Code: "handoff_malformed", Error: "invalid credential handoff public key"}
	}
	account, err := localHetznerAccountForCredentialHandoff()
	if err != nil || account == nil || strings.TrimSpace(account.Fields["token"]) == "" {
		return OpsResult{OK: false, Code: "credential_missing", Error: "this endpoint has no Hetzner token in its local vault or active hcloud context"}
	}
	var recipient [32]byte
	copy(recipient[:], recipientBytes)
	senderPublic, senderPrivate, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return OpsResult{OK: false, Code: "crypto_unavailable", Error: "could not create a handoff key"}
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return OpsResult{OK: false, Code: "crypto_unavailable", Error: "could not create a handoff nonce"}
	}
	plain, err := json.Marshal(endpointHandoffPlaintext{
		Version: 1, HandoffID: request.HandoffID, TargetDeviceID: request.TargetDeviceID,
		AccountFingerprint: request.AccountFingerprint, CreatedAt: now.UnixMilli(),
		ExpiresAt: request.ExpiresAt, Kind: "hetzner-api-token", Value: strings.TrimSpace(account.Fields["token"]),
	})
	if err != nil {
		return OpsResult{OK: false, Code: "crypto_unavailable", Error: "could not encode credential handoff"}
	}
	ciphertext := box.Seal(nil, plain, &nonce, &recipient, senderPrivate)
	for i := range plain {
		plain[i] = 0
	}
	return OpsResult{OK: true, Initial: endpointHandoffEnvelope{
		Version: 1, Type: "yaver-credential-envelope", HandoffID: request.HandoffID,
		TargetDeviceID: request.TargetDeviceID, AccountFingerprint: request.AccountFingerprint,
		SenderPublicKey: base64.StdEncoding.EncodeToString(senderPublic[:]),
		Nonce:           base64.StdEncoding.EncodeToString(nonce[:]), Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}}
}

func credentialHandoffReceiverRegistered(ctx context.Context, convexURL, token string, request endpointHandoffRequest) (bool, error) {
	convexURL = strings.TrimRight(strings.TrimSpace(convexURL), "/")
	token = strings.TrimSpace(token)
	if convexURL == "" || token == "" {
		return false, fmt.Errorf("endpoint is not signed in")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, convexURL+"/credential-handoff/devices", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cache-Control", "no-store")
	resp, err := credentialHandoffDirectoryHTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("directory returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Devices []struct {
			DeviceID  string `json:"deviceId"`
			PublicKey string `json:"publicKey"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("decode directory: %w", err)
	}
	for _, device := range result.Devices {
		if device.DeviceID == request.TargetDeviceID && device.PublicKey == request.TargetPublicKey {
			return true, nil
		}
	}
	return false, nil
}

func opsCredentialHandoffAccept(c OpsContext, raw json.RawMessage) OpsResult {
	if c.RequestHeaders.Get("X-Yaver-Via-Relay") == "1" {
		return OpsResult{OK: false, Code: "secure_transport_required", Error: "credential handoff requires a direct or Yaver Mesh encrypted connection"}
	}
	var env endpointHandoffEnvelope
	if json.Unmarshal(raw, &env) != nil || env.Version != 1 || env.Type != "yaver-credential-envelope" {
		return OpsResult{OK: false, Code: "handoff_malformed", Error: "invalid credential handoff envelope"}
	}
	endpointCredentialHandoffs.Lock()
	pending, ok := endpointCredentialHandoffs.items[env.HandoffID]
	if ok {
		delete(endpointCredentialHandoffs.items, env.HandoffID)
	}
	endpointCredentialHandoffs.Unlock()
	if !ok || time.Now().After(pending.ExpiresAt) {
		return OpsResult{OK: false, Code: "handoff_expired", Error: "credential handoff expired or was already used"}
	}
	if env.TargetDeviceID != pending.DeviceID || env.AccountFingerprint != pending.AccountFingerprint {
		return OpsResult{OK: false, Code: "handoff_binding_failed", Error: "credential handoff device/account binding failed"}
	}
	senderBytes, err1 := base64.StdEncoding.DecodeString(env.SenderPublicKey)
	nonceBytes, err2 := base64.StdEncoding.DecodeString(env.Nonce)
	ciphertext, err3 := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err1 != nil || err2 != nil || err3 != nil || len(senderBytes) != 32 || len(nonceBytes) != 24 {
		return OpsResult{OK: false, Code: "handoff_malformed", Error: "invalid credential handoff encoding"}
	}
	var sender [32]byte
	var nonce [24]byte
	copy(sender[:], senderBytes)
	copy(nonce[:], nonceBytes)
	opened, authenticated := box.Open(nil, ciphertext, &nonce, &sender, pending.PrivateKey)
	if !authenticated {
		return OpsResult{OK: false, Code: "handoff_auth_failed", Error: "credential handoff authentication failed"}
	}
	var secret endpointHandoffPlaintext
	if json.Unmarshal(opened, &secret) != nil || secret.Version != 1 ||
		secret.HandoffID != env.HandoffID || secret.TargetDeviceID != env.TargetDeviceID ||
		secret.AccountFingerprint != env.AccountFingerprint || secret.Kind != "hetzner-api-token" ||
		strings.TrimSpace(secret.Value) == "" || len(secret.Value) > 32768 ||
		secret.ExpiresAt <= secret.CreatedAt || time.Now().UnixMilli() >= secret.ExpiresAt {
		return OpsResult{OK: false, Code: "handoff_payload_failed", Error: "credential handoff payload failed validation"}
	}
	if err := globalAccountsManager.Connect(ProviderHetzner, "Secure device handoff", map[string]string{"token": strings.TrimSpace(secret.Value)}); err != nil {
		return OpsResult{OK: false, Code: "vault_write_failed", Error: "trusted endpoint vault could not store the credential"}
	}
	for i := range opened {
		opened[i] = 0
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"kind": secret.Kind, "stored": true}}
}
