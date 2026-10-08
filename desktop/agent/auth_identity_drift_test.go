package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendHeartbeatDoesNotPublishPrivateNetworkCoordinates(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer srv.Close()

	_, err := SendHeartbeat(srv.URL, "tok", "device", nil, nil,
		"100.64.0.7", []string{"192.168.1.8", "100.64.0.7"},
		[]string{"https://private.example.invalid"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	for _, forbidden := range [][]byte{[]byte(`"quicHost"`), []byte(`"localIps"`), []byte(`"publicEndpoints"`), []byte("100.64.0.7"), []byte("192.168.1.8")} {
		if bytes.Contains(body, forbidden) {
			t.Fatalf("heartbeat leaked private network coordinate %q: %s", forbidden, body)
		}
	}
}

func TestSendHeartbeatCarriesCanonicalDeviceIDRepair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/heartbeat" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":                   true,
			"pendingRescue":        false,
			"pendingPublish":       false,
			"canonicalDeviceId":    "canonical-device",
			"repairedDeviceIdFrom": "stale-device",
		})
	}))
	defer srv.Close()

	hb, err := SendHeartbeat(srv.URL, "tok", "stale-device", nil, nil, "", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	if hb.CanonicalDeviceID != "canonical-device" {
		t.Fatalf("canonical id = %q", hb.CanonicalDeviceID)
	}
	if hb.RepairedDeviceIDFrom != "stale-device" {
		t.Fatalf("repaired from = %q", hb.RepairedDeviceIDFrom)
	}
}

func TestSendHeartbeatClassifiesIdentityDriftFailures(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{
			name: "stale",
			body: `{"error":"DEVICE_ID_STALE: device row not found"}`,
			want: ErrDeviceIDStale,
		},
		{
			name: "ambiguous",
			body: `{"error":"IDENTITY_DRIFT_AMBIGUOUS: multiple rows"}`,
			want: ErrDeviceIDAmbiguous,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, tc.body, http.StatusConflict)
			}))
			defer srv.Close()

			_, err := SendHeartbeat(srv.URL, "tok", "stale-device", nil, nil, "", nil, nil, nil, nil, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAdoptCanonicalDeviceIDPersistsAndRequestsRestart(t *testing.T) {
	withTempHome(t)
	if err := SaveConfig(&Config{DeviceID: "stale-device"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	restarted := false
	ok := adoptCanonicalDeviceIDFromHeartbeat("stale-device", &HeartbeatResult{
		CanonicalDeviceID:    "canonical-device",
		RepairedDeviceIDFrom: "stale-device",
	}, func() { restarted = true })
	if !ok {
		t.Fatal("expected adoption")
	}
	if !restarted {
		t.Fatal("expected restart callback")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DeviceID != "canonical-device" {
		t.Fatalf("device id = %q", cfg.DeviceID)
	}
}

func TestAdoptCanonicalDeviceIDDoesNotOverwriteConcurrentRepair(t *testing.T) {
	withTempHome(t)
	if err := SaveConfig(&Config{DeviceID: "already-fixed"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	restarted := false
	ok := adoptCanonicalDeviceIDFromHeartbeat("stale-device", &HeartbeatResult{
		CanonicalDeviceID:    "canonical-device",
		RepairedDeviceIDFrom: "stale-device",
	}, func() { restarted = true })
	if ok {
		t.Fatal("adopted despite disk no longer matching the stale start id")
	}
	if restarted {
		t.Fatal("restart callback should not run")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DeviceID != "already-fixed" {
		t.Fatalf("device id = %q", cfg.DeviceID)
	}
}
