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

func TestRefreshCannotReplaceAccountChangedDuringRequest(t *testing.T) {
	vaultDirForTest(t)
	if err := SaveConfig(&Config{AuthToken: "first-account"}); err != nil {
		t.Fatal(err)
	}
	_, err := refreshSavedAuthSession("https://example.invalid", func(_ string, token string) (string, error) {
		if token != "first-account" {
			t.Fatal("wrong refresh identity")
		}
		if err := SaveConfig(&Config{AuthToken: "second-account"}); err != nil {
			t.Fatal(err)
		}
		return "first-account-rotated", nil
	})
	if err == nil {
		t.Fatal("old refresh replaced new account")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken != "second-account" {
		t.Fatal("new account was overwritten")
	}
}

func TestRefreshRefusesSignedOutSession(t *testing.T) {
	vaultDirForTest(t)
	if err := SaveConfigClearingAuth(&Config{}); err != nil {
		t.Fatal(err)
	}
	_, err := refreshSavedAuthSession("https://example.invalid", func(string, string) (string, error) {
		t.Fatal("signed-out device must not refresh")
		return "", nil
	})
	if err == nil {
		t.Fatal("missing session accepted")
	}
}
