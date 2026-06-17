package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeA11yService stands in for the on-device AccessibilityService control
// surface so the driver is exercised fully offline.
func fakeA11yService(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var hits []string
	mux := http.NewServeMux()
	mux.HandleFunc("/a11y/launch", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "launch")
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/a11y/type", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "type")
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/a11y/tap", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "tap")
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/a11y/texts", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "texts")
		w.Write([]byte(`{"nodes":[{"text":"Sign in"},{"text":"Your code is 123456"}]}`))
	})
	mux.HandleFunc("/a11y/frame", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "frame")
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	})
	mux.HandleFunc("/a11y/sms/latest", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "sms")
		w.Write([]byte(`{"code":"654321"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestLocalAccessibilityDriver(t *testing.T) {
	srv, _ := fakeA11yService(t)
	d := newLocalAccessibilityDriver(srv.URL)

	if err := d.Launch("com.bank.app"); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := d.Type("hunter2"); err != nil {
		t.Fatalf("Type: %v", err)
	}
	if err := d.Tap("Sign in"); err != nil {
		t.Fatalf("Tap: %v", err)
	}
	nodes, err := d.UiTexts()
	if err != nil || len(nodes) != 2 || nodes[0].Text != "Sign in" {
		t.Fatalf("UiTexts wrong: %+v err=%v", nodes, err)
	}
	img, err := d.Frame()
	if err != nil || len(img) != 4 {
		t.Fatalf("Frame wrong: len=%d err=%v", len(img), err)
	}

	// Persistent-session semantics: snapshot returns a self ref; restore is a
	// no-op success (a logged-in phone stays logged in).
	inst, snap, err := d.Snapshot()
	if err != nil || inst == "" || snap != "live" {
		t.Fatalf("Snapshot wrong: %q %q %v", inst, snap, err)
	}
	if err := d.RestoreSnapshot(inst, snap); err != nil {
		t.Fatalf("RestoreSnapshot should succeed (persistent session): %v", err)
	}
}

func TestLocalAccessibilityDriverReadSMSConsent(t *testing.T) {
	isolateHome(t)
	srv, hits := fakeA11yService(t)
	d := newLocalAccessibilityDriver(srv.URL)

	// Ungranted ⇒ "" WITHOUT calling the service.
	code, err := d.ReadSMS()
	if err != nil || code != "" {
		t.Fatalf("ungranted ReadSMS must be (\"\", nil), got (%q,%v)", code, err)
	}
	for _, h := range *hits {
		if h == "sms" {
			t.Fatal("ReadSMS hit the service without consent")
		}
	}

	// Granted ⇒ reads the code from the service.
	if err := saveGatewayConsent(GatewayConsent{ReadDeviceSms: true}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	code, err = d.ReadSMS()
	if err != nil || code != "654321" {
		t.Fatalf("granted ReadSMS = (%q,%v), want 654321", code, err)
	}
}

// TestDeviceDriverForSelfSentinel confirms Connector.Device == "self" selects the
// AccessibilityService driver, not an adb serial.
func TestDeviceDriverForSelfSentinel(t *testing.T) {
	b := &broker{handlers: map[string]AuthMethod{}}
	b.register(newPasswordTotpHandler(nil, &redroidDeviceDriver{serial: "default-dev"}, newGateStore(nil)))

	c := &Connector{ID: "bank", Engine: "device", Device: localDeviceSentinel,
		Auth: ConnectorAuth{Method: "password_totp"}}
	drv, ok := b.deviceDriverFor(c)
	if !ok {
		t.Fatal("expected a driver for the self-driving connector")
	}
	if _, isLocal := drv.(*localAccessibilityDriver); !isLocal {
		t.Fatalf("expected localAccessibilityDriver, got %T", drv)
	}
}
