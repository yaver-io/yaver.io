package main

// Local-only bridge for users who already configured the official hcloud CLI.
// It is invoked only by an authenticated, same-account credential handoff
// request. The token is never logged or returned as plaintext: it is imported
// into the endpoint's encrypted provider vault and immediately sealed to the
// requesting device's ephemeral public key.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type hcloudContextCredential struct {
	Name  string
	Token string
}

var hcloudConfigPathForCredentialHandoff = defaultHcloudConfigPath

func defaultHcloudConfigPath() (string, error) {
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "hcloud", "cli.toml"), nil
		}
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "hcloud", "cli.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "hcloud", "cli.toml"), nil
}

func parseHcloudQuotedValue(line, key string) (string, bool) {
	left, right, ok := strings.Cut(line, "=")
	if !ok || strings.TrimSpace(left) != key {
		return "", false
	}
	raw := strings.TrimSpace(right)
	value, err := strconv.Unquote(raw)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func readActiveHcloudContextCredential(path string) (*hcloudContextCredential, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("hcloud config permissions are too broad")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	active := ""
	type entry struct{ name, token string }
	entries := []entry{}
	current := -1
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[contexts]]" {
			entries = append(entries, entry{})
			current = len(entries) - 1
			continue
		}
		if value, ok := parseHcloudQuotedValue(line, "active_context"); ok {
			active = value
			continue
		}
		if current >= 0 {
			if value, ok := parseHcloudQuotedValue(line, "name"); ok {
				entries[current].name = value
			} else if value, ok := parseHcloudQuotedValue(line, "token"); ok {
				entries[current].token = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if active == "" {
		return nil, fmt.Errorf("hcloud has no active context")
	}
	for _, item := range entries {
		if item.name != active {
			continue
		}
		if item.token == "" || len(item.token) > 512 || strings.IndexFunc(item.token, func(r rune) bool { return r <= ' ' }) >= 0 {
			return nil, fmt.Errorf("active hcloud context has no valid token")
		}
		return &hcloudContextCredential{Name: item.name, Token: item.token}, nil
	}
	return nil, fmt.Errorf("active hcloud context was not found")
}

func localHetznerAccountForCredentialHandoff() (*AccountRecord, error) {
	account, err := globalAccountsManager.Get(ProviderHetzner)
	if err != nil {
		return nil, err
	}
	if account != nil && strings.TrimSpace(account.Fields["token"]) != "" {
		return account, nil
	}
	path, err := hcloudConfigPathForCredentialHandoff()
	if err != nil {
		return nil, err
	}
	credential, err := readActiveHcloudContextCredential(path)
	if err != nil {
		return nil, err
	}
	label := "hcloud context: " + credential.Name
	if err := globalAccountsManager.Connect(ProviderHetzner, label, map[string]string{"token": credential.Token}); err != nil {
		return nil, err
	}
	credential.Token = ""
	return globalAccountsManager.Get(ProviderHetzner)
}
