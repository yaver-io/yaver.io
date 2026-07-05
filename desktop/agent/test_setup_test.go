package main

import "os"

func init() {
	if os.Getenv("YAVER_VAULT_SKIP_KEYCHAIN") == "" {
		_ = os.Setenv("YAVER_VAULT_SKIP_KEYCHAIN", "1")
	}
}
