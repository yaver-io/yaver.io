package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeWiFiMeshConfig(t *testing.T) {
	cfg := normalizeWiFiMeshConfig(&WiFiMeshConfig{
		MeshID:     "yaver-lab",
		Interface:  "wlan0",
		Passphrase: "test-passphrase",
	})
	if cfg.Backend != "80211s" {
		t.Fatalf("backend = %q, want 80211s", cfg.Backend)
	}
	if cfg.MeshIface != "wlan0mesh" {
		t.Fatalf("mesh interface = %q", cfg.MeshIface)
	}
	if cfg.FrequencyMHz != 2437 {
		t.Fatalf("frequency = %d, want 2437", cfg.FrequencyMHz)
	}
}

func TestWiFiMeshWpaSupplicantConfig(t *testing.T) {
	dir := t.TempDir()
	mgr := NewWiFiMeshManager(dir)
	cfg := &WiFiMeshConfig{
		MeshID:       `Yaver "Lab"`,
		Passphrase:   `mesh\passphrase`,
		Interface:    "wlan0",
		MeshIface:    "mesh0",
		Backend:      "batman",
		FrequencyMHz: 5180,
		CountryCode:  "us",
		IPAddress:    "10.47.0.1/24",
	}
	if err := mgr.GenerateWpaSupplicantConfig(cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".yaver", "wifi-mesh", "wpa_supplicant.conf"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{
		"country=US",
		`ssid="Yaver \"Lab\""`,
		"mode=5",
		"frequency=5180",
		"key_mgmt=SAE",
		`psk="mesh\\passphrase"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("wpa config missing %q:\n%s", want, body)
		}
	}
}

func TestIWSupportsMeshPoint(t *testing.T) {
	out := `
Wiphy phy0
	Supported interface modes:
		 * managed
		 * AP
		 * mesh point
`
	if !iwSupportsInterfaceMode(out, "mesh point") {
		t.Fatal("expected mesh point support")
	}
	if iwSupportsInterfaceMode(out, "monitor") {
		t.Fatal("did not expect monitor support")
	}
}

func TestChannelToFrequencyMHz(t *testing.T) {
	cases := map[int]int{1: 2412, 6: 2437, 14: 2484, 36: 5180}
	for ch, want := range cases {
		if got := channelToFrequencyMHz(ch); got != want {
			t.Fatalf("channel %d = %d, want %d", ch, got, want)
		}
	}
}
