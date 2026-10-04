package main

// One-time P2P credential handoff receiver. The sender encrypts directly to an
// ephemeral endpoint key using the same NaCl box format as the native clients.
// No provider credential appears in an ops schema, relay-visible envelope,
// response, log, or device directory.

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
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
}

func credentialAccountFingerprintGo(accountID string) string {
	sum := sha512.Sum512([]byte("yaver-credential-account-v1\x00" + strings.TrimSpace(accountID)))
	return base64.RawURLEncoding.EncodeToString(sum[:18])
}

func opsCredentialHandoffRequest(c OpsContext, _ json.RawMessage) OpsResult {
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
