package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadActiveHcloudContextCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.toml")
	contents := `active_context = "second"

[[contexts]]
name = "first"
token = "first-secret"

[[contexts]]
name = "second"
token = "second-secret"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := readActiveHcloudContextCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Name != "second" || credential.Token != "second-secret" {
		t.Fatalf("wrong active context selected: name=%q tokenMatch=%v", credential.Name, credential.Token == "second-secret")
	}
}

func TestAccountsManagerAcceptsRawVaultV2MasterKey(t *testing.T) {
	root := t.TempDir()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := os.WriteFile(filepath.Join(root, "master.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &AccountsManager{baseDir: filepath.Join(root, "secrets")}
	if err := manager.Connect(ProviderHetzner, "local", map[string]string{"token": "test-only-token"}); err != nil {
		t.Fatal(err)
	}
	account, err := manager.Get(ProviderHetzner)
	if err != nil || account == nil || account.Fields["token"] != "test-only-token" {
		t.Fatalf("raw vault-v2 master key did not round-trip provider account: account=%v err=%v", account != nil, err)
	}
}
