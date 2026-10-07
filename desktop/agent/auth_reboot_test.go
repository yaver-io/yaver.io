package main

import "testing"

func TestRotatedTokenIsUsableImmediatelyAndAfterReload(t *testing.T) {
	vaultDirForTest(t)
	cfg := &Config{AuthToken: "test-old-token", DeviceID: "test-device"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := persistRotatedAuthToken(cfg, "test-new-token"); err != nil {
		t.Fatal(err)
	}
	if cfg.AuthToken != "test-new-token" {
		t.Fatal("running bootstrap kept the pre-refresh token")
	}
	rebooted, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if rebooted.AuthToken != "test-new-token" || rebooted.DeviceID != "test-device" {
		t.Fatal("reboot lost identity")
	}
}

func TestInFlightRefreshCannotUndoSignOut(t *testing.T) {
	vaultDirForTest(t)
	cfg := &Config{AuthToken: "test-old-token", DeviceID: "test-device"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	signedOut := &Config{DeviceID: "test-device"}
	if err := SaveConfigClearingAuth(signedOut); err != nil {
		t.Fatal(err)
	}
	if err := persistRotatedAuthToken(cfg, "test-new-token"); err == nil {
		t.Fatal("refresh resurrected signed-out session")
	}
	disk, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if disk.AuthToken != "" {
		t.Fatal("sign-out was undone")
	}
}
