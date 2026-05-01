package main

import "testing"

func TestResolveDeviceAcceptsAtAlias(t *testing.T) {
	dev, err := resolveDevice("@prod-box", []DeviceInfo{
		{DeviceID: "dev_1234567890", Name: "ubuntu-box", Alias: "prod-box"},
	})
	if err != nil {
		t.Fatalf("resolveDevice returned error: %v", err)
	}
	if dev == nil {
		t.Fatal("resolveDevice returned nil device")
	}
	if dev.Alias != "prod-box" {
		t.Fatalf("expected alias prod-box, got %q", dev.Alias)
	}
}

func TestNormalizeDeviceHintPreservesUserAtHost(t *testing.T) {
	if got := normalizeDeviceHint("ubuntu@prod-box"); got != "ubuntu@prod-box" {
		t.Fatalf("expected user@host to stay intact, got %q", got)
	}
}
