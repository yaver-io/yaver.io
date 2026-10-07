//go:build !windows

package main

import (
	"os/user"
	"testing"
)

func TestLaunchdInstallPreservesSudoUserIdentity(t *testing.T) {
	current := func() (*user.User, error) { return &user.User{Username: "root", Uid: "0", HomeDir: "/var/root"}, nil }
	lookup := func(name string) (*user.User, error) {
		return &user.User{Username: name, Uid: "501", HomeDir: "/test/operator"}, nil
	}
	account, err := launchdInstallAccount(0, "operator", current, lookup)
	if err != nil || account.Username != "operator" || account.HomeDir != "/test/operator" {
		t.Fatalf("wrong reboot account: %v", err)
	}
	if _, err := launchdInstallAccount(0, "", current, lookup); err == nil {
		t.Fatal("bare root silently used a different identity")
	}
}
