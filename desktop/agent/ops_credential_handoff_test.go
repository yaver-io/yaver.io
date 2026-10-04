package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
)

type credentialHandoffRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn credentialHandoffRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func stubCredentialHandoffDirectory(t *testing.T, response string, inspect func(*http.Request)) {
	t.Helper()
	original := credentialHandoffDirectoryHTTPClient
	credentialHandoffDirectoryHTTPClient = &http.Client{Transport: credentialHandoffRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if inspect != nil {
			inspect(req)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(response)),
			Request:    req,
		}, nil
	})}
	t.Cleanup(func() { credentialHandoffDirectoryHTTPClient = original })
}

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
	for name, result := range map[string]OpsResult{
		"request": opsCredentialHandoffRequest(OpsContext{RequestHeaders: headers}, []byte(`{}`)),
		"accept":  opsCredentialHandoffAccept(OpsContext{RequestHeaders: headers}, []byte(`{}`)),
	} {
		if result.OK || result.Code != "secure_transport_required" {
			t.Fatalf("relay %s was not rejected: %#v", name, result)
		}
	}
}

func TestEndpointCredentialHandoffOfferRejectsInspectableRelay(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Yaver-Via-Relay", "1")
	result := opsCredentialHandoffOffer(OpsContext{RequestHeaders: headers}, []byte(`{}`))
	if result.OK || result.Code != "secure_transport_required" {
		t.Fatalf("relay offer was not rejected: %#v", result)
	}
}

func TestEndpointCredentialHandoffOffersHetznerTokenAsPhoneTargetedCiphertextOverDirectP2P(t *testing.T) {
	originalManager := globalAccountsManager
	globalAccountsManager = &AccountsManager{baseDir: t.TempDir()}
	t.Cleanup(func() { globalAccountsManager = originalManager })
	secretValue := "HCLOUD_TEST_SECRET_PHONE_MUST_DECRYPT"
	if err := globalAccountsManager.Connect(ProviderHetzner, "local", map[string]string{"token": secretValue}); err != nil {
		t.Fatal(err)
	}

	recipientPublic, recipientPrivate, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	request := endpointHandoffRequest{
		Version: 1, Type: "yaver-credential-request", HandoffID: "phone-request-1",
		TargetDeviceID: "phone-device-1", TargetPublicKey: base64.StdEncoding.EncodeToString(recipientPublic[:]),
		AccountFingerprint: credentialAccountFingerprintGo("account-owner-1"),
		CreatedAt:          now.UnixMilli(), ExpiresAt: now.Add(time.Minute).UnixMilli(),
	}
	raw, _ := json.Marshal(request)
	directoryResponse, _ := json.Marshal(map[string]interface{}{"devices": []map[string]interface{}{{
		"deviceId": request.TargetDeviceID, "publicKey": request.TargetPublicKey, "platform": "ios", "updatedAt": now.UnixMilli(),
	}}})
	stubCredentialHandoffDirectory(t, string(directoryResponse), func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer endpoint-session" {
			t.Fatalf("handoff directory request was not authenticated")
		}
	})
	server := &HTTPServer{
		ownerUserID: "account-owner-1", deviceID: "desktop-device-1",
		convexURL: "https://directory.test", token: "endpoint-session",
	}
	result := opsCredentialHandoffOffer(OpsContext{Server: server, Caller: "owner"}, raw)
	if !result.OK {
		t.Fatalf("offer failed: %#v", result)
	}
	response, _ := json.Marshal(result)
	if strings.Contains(string(response), secretValue) {
		t.Fatal("plaintext credential leaked in handoff response")
	}
	envelopeJSON, _ := json.Marshal(result.Initial)
	var envelope endpointHandoffEnvelope
	if err := json.Unmarshal(envelopeJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	senderBytes, _ := base64.StdEncoding.DecodeString(envelope.SenderPublicKey)
	nonceBytes, _ := base64.StdEncoding.DecodeString(envelope.Nonce)
	ciphertext, _ := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	var sender [32]byte
	var nonce [24]byte
	copy(sender[:], senderBytes)
	copy(nonce[:], nonceBytes)
	opened, ok := box.Open(nil, ciphertext, &nonce, &sender, recipientPrivate)
	if !ok || !strings.Contains(string(opened), secretValue) {
		t.Fatal("phone could not authenticate and decrypt the offered credential")
	}
}

func TestEndpointCredentialHandoffOfferRejectsUnregisteredPhoneKey(t *testing.T) {
	stubCredentialHandoffDirectory(t, `{"devices":[]}`, nil)
	recipientPublic, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	request := endpointHandoffRequest{
		Version: 1, Type: "yaver-credential-request", HandoffID: "unregistered-phone",
		TargetDeviceID: "phone-device-2", TargetPublicKey: base64.StdEncoding.EncodeToString(recipientPublic[:]),
		AccountFingerprint: credentialAccountFingerprintGo("account-owner-1"),
		CreatedAt:          now.UnixMilli(), ExpiresAt: now.Add(time.Minute).UnixMilli(),
	}
	raw, _ := json.Marshal(request)
	server := &HTTPServer{ownerUserID: "account-owner-1", convexURL: "https://directory.test", token: "endpoint-session"}
	result := opsCredentialHandoffOffer(OpsContext{Server: server, Caller: "owner"}, raw)
	if result.OK || result.Code != "handoff_binding_failed" {
		t.Fatalf("unregistered receiver key was not rejected: %#v", result)
	}
}
