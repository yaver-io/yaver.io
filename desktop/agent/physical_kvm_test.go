package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidatePhysicalKVMURLRejectsPublicAndPaths(t *testing.T) {
	if _, err := validatePhysicalKVMURL("https://127.0.0.1:8348"); err == nil {
		t.Fatal("https is not the v1 local bridge protocol")
	}
	if _, err := validatePhysicalKVMURL("http://127.0.0.1:8348/admin"); err == nil {
		t.Fatal("path-bearing URL should be rejected")
	}
	if got, err := validatePhysicalKVMURL("http://127.0.0.1:8348/"); err != nil || got != "http://127.0.0.1:8348" {
		t.Fatalf("local URL = %q, %v", got, err)
	}
}

func TestValidatePhysicalKVMTokenRequires32ByteHex(t *testing.T) {
	if err := validatePhysicalKVMToken("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "0123456789abcdef0123456789abcdef", strings.Repeat("z", 64)} {
		if err := validatePhysicalKVMToken(value); err == nil {
			t.Fatalf("token %q should be rejected", value)
		}
	}
}

func TestPhysicalKVMJPEGFrameUsesFreshCaptureDirectly(t *testing.T) {
	captureStream.mu.Lock()
	previousFrame := captureStream.latest
	previousFrameAt := captureStream.lastFrameAt
	previousWidth, previousHeight := captureStream.width, captureStream.height
	captureStream.latest = []byte{0xff, 0xd8, 0xff, 0xd9}
	captureStream.lastFrameAt = time.Now()
	captureStream.width, captureStream.height = 1280, 720
	captureStream.mu.Unlock()
	t.Cleanup(func() {
		captureStream.mu.Lock()
		captureStream.latest = previousFrame
		captureStream.lastFrameAt = previousFrameAt
		captureStream.width, captureStream.height = previousWidth, previousHeight
		captureStream.mu.Unlock()
	})

	frame, width, height, err := (physicalPCKVMTarget{}).JPEGFrame(t.Context(), "device")
	if err != nil || len(frame) != 4 || width != 1280 || height != 720 {
		t.Fatalf("JPEGFrame = %d bytes %dx%d, %v", len(frame), width, height, err)
	}
	frame[0] = 0
	captureStream.mu.Lock()
	defer captureStream.mu.Unlock()
	if captureStream.latest[0] != 0xff {
		t.Fatal("JPEGFrame exposed mutable shared capture storage")
	}
}

func TestPhysicalKVMManagerPairNonceSequenceAndPermissions(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var leaseID = "lease-one"
	var sequences []uint32
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/challenge" {
			_, _ = w.Write([]byte(`{"ok":true,"nonce":"0123456789abcdef0123456789abcdef0123456789abcdef"}`))
			return
		}
		nonce := r.Header.Get("X-Yaver-Nonce")
		controllerID := r.Header.Get("X-Yaver-Controller-ID")
		body, _ := io.ReadAll(r.Body)
		digest := sha256.Sum256(body)
		canonical := nonce + "\n" + controllerID + "\n" + r.Method + "\n" + r.URL.Path + "\n" + hex.EncodeToString(digest[:])
		mac := hmac.New(sha256.New, []byte(token))
		_, _ = mac.Write([]byte(canonical))
		expectedSignature := hex.EncodeToString(mac.Sum(nil))
		if nonce == "" || controllerID == "" || !hmac.Equal([]byte(r.Header.Get("X-Yaver-Signature")), []byte(expectedSignature)) {
			http.Error(w, `{"code":"KVM_AUTH_REQUIRED","error":"missing signed challenge"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/status", "/v1/pair":
			_, _ = w.Write([]byte(`{"ok":true,"protocol":"yaver-physical-kvm-v1","deviceId":"m5-test","mode":"keyboard","pairingState":"paired","keyboardLayout":"us","usbReady":true,"armed":true}`))
		case "/v1/session/open":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "leaseId": leaseID, "nonce": req["nonce"]})
		case "/v1/action":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["leaseId"] != leaseID {
				http.Error(w, `{"code":"KVM_LEASE_REQUIRED","error":"bad lease"}`, http.StatusConflict)
				return
			}
			seq := uint32(req["sequence"].(float64))
			sequences = append(sequences, seq)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "sequence": seq})
		default:
			http.NotFound(w, r)
		}
	}))
	defer bridge.Close()

	dir := t.TempDir()
	m := newPhysicalKVMManager()
	m.configPath = func() (string, error) { return filepath.Join(dir, "binding.json"), nil }
	status, err := m.pair(t.Context(), bridge.URL, token)
	if err != nil || status.DeviceID != "m5-test" {
		t.Fatalf("pair = %+v, %v", status, err)
	}
	info, err := os.Stat(filepath.Join(dir, "binding.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("binding mode = %o, want 600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "binding.json"))
	if !strings.Contains(string(raw), "m5-test") || !strings.Contains(string(raw), token) {
		t.Fatal("binding did not persist device and credential")
	}

	sessionID, err := m.open(t.Context())
	if err != nil || !strings.HasPrefix(sessionID, "kvm-") {
		t.Fatalf("open = %q, %v", sessionID, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := m.action(t.Context(), sessionID, map[string]any{"kind": "key", "key": "enter"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatalf("sequences = %v, want [1 2]", sequences)
	}
	if _, err := m.action(t.Context(), "wrong-session", map[string]any{"kind": "key"}); err == nil {
		t.Fatal("wrong local session must fail before reaching bridge")
	}
}

func TestPhysicalKVMRemoteRuntimeTargetIsRegistered(t *testing.T) {
	target, err := runtimeTargetFor(physicalKVMTargetID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := target.(physicalPCKVMTarget); !ok {
		t.Fatalf("target type = %T", target)
	}
	caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), "desktop")
	found := false
	for _, item := range caps.Targets {
		found = found || item.ID == physicalKVMTargetID
	}
	if !found {
		t.Fatalf("desktop capabilities omit %s: %+v", physicalKVMTargetID, caps.Targets)
	}
}
