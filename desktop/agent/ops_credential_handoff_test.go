package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
)

func TestEndpointCredentialHandoffStoresHetznerTokenWithoutReturningIt(t *testing.T) {
	originalManager := globalAccountsManager
	globalAccountsManager = &AccountsManager{baseDir: t.TempDir()}
	t.Cleanup(func() { globalAccountsManager = originalManager })

	server := &HTTPServer{ownerUserID: "account-owner-1", deviceID: "desktop-device-1"}
	requestResult := opsCredentialHandoffRequest(OpsContext{Server: server, Caller: "owner"}, nil)
	if !requestResult.OK {
		t.Fatalf("request failed: %#v", requestResult)
	}
	requestJSON, _ := json.Marshal(requestResult.Initial)
	var request struct {
		Version            int    `json:"version"`
		HandoffID          string `json:"handoffId"`
		TargetDeviceID     string `json:"targetDeviceId"`
		TargetPublicKey    string `json:"targetPublicKey"`
		AccountFingerprint string `json:"accountFingerprint"`
		ExpiresAt          int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	recipientBytes, _ := base64.StdEncoding.DecodeString(request.TargetPublicKey)
	var recipient [32]byte
	copy(recipient[:], recipientBytes)
	senderPub, senderPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	secretValue := "HCLOUD_TEST_SECRET_NEVER_RETURN"
	plain, _ := json.Marshal(endpointHandoffPlaintext{
		Version: 1, HandoffID: request.HandoffID, TargetDeviceID: request.TargetDeviceID,
		AccountFingerprint: request.AccountFingerprint, CreatedAt: time.Now().UnixMilli(),
		ExpiresAt: request.ExpiresAt, Kind: "hetzner-api-token", Value: secretValue,
	})
	ciphertext := box.Seal(nil, plain, &nonce, &recipient, senderPriv)
	envelope := endpointHandoffEnvelope{
		Version: 1, Type: "yaver-credential-envelope", HandoffID: request.HandoffID,
		TargetDeviceID: request.TargetDeviceID, AccountFingerprint: request.AccountFingerprint,
		SenderPublicKey: base64.StdEncoding.EncodeToString(senderPub[:]),
		Nonce:           base64.StdEncoding.EncodeToString(nonce[:]), Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	raw, _ := json.Marshal(envelope)
	accepted := opsCredentialHandoffAccept(OpsContext{Server: server, Caller: "owner"}, raw)
	if !accepted.OK {
		t.Fatalf("accept failed: %#v", accepted)
	}
	response, _ := json.Marshal(accepted)
	if string(response) == "" || strings.Contains(string(response), secretValue) {
		t.Fatal("plaintext credential leaked in response")
	}
	stored, err := globalAccountsManager.Get(ProviderHetzner)
	if err != nil || stored == nil || stored.Fields["token"] != secretValue {
		t.Fatal("credential was not stored in endpoint vault")
	}
	replay := opsCredentialHandoffAccept(OpsContext{Server: server, Caller: "owner"}, raw)
	if replay.OK || replay.Code != "handoff_expired" {
		t.Fatalf("replay accepted: %#v", replay)
	}
}

func TestEndpointCredentialHandoffRejectsInspectableRelay(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Yaver-Via-Relay", "1")
	result := opsCredentialHandoffAccept(OpsContext{RequestHeaders: headers}, []byte(`{}`))
	if result.OK || result.Code != "secure_transport_required" {
		t.Fatalf("relay was not rejected: %#v", result)
	}
}
