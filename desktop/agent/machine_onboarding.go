package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type machineOnboardingProviderStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Ready       bool   `json:"ready"`
	Configured  bool   `json:"configured"`
	CloneReady  bool   `json:"cloneReady,omitempty"`
	CIReady     bool   `json:"ciReady,omitempty"`
	AuthSource  string `json:"authSource,omitempty"`
	CloneSource string `json:"cloneSource,omitempty"`
	CISource    string `json:"ciSource,omitempty"`
	Username    string `json:"username,omitempty"`
	Host        string `json:"host,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

type machineOnboardingStatus struct {
	Providers []machineOnboardingProviderStatus `json:"providers"`
}

type machineOnboardingApplyRequest struct {
	OpenAIAPIKey string `json:"openai_api_key"`
	GitHubToken  string `json:"github_token"`
	GitLabToken  string `json:"gitlab_token"`
	GitLabHost   string `json:"gitlab_host"`
	ApplyClone   *bool  `json:"apply_clone"`
	ApplyCIToken *bool  `json:"apply_ci_token"`
	Notes        string `json:"notes"`
}

type machineOnboardingRemoveRequest struct {
	Providers     []string `json:"providers"`
	GitLabHost    string   `json:"gitlab_host"`
	RemoveClone   *bool    `json:"remove_clone"`
	RemoveCIToken *bool    `json:"remove_ci_token"`
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func openVaultOptional() (*VaultStore, error) {
	if pass := strings.TrimSpace(os.Getenv("YAVER_VAULT_PASSPHRASE")); pass != "" {
		// Manual override — caller picked a stable passphrase, don't
		// silently flip them to v2 (mirrors openVaultE).
		return NewVaultStore(pass)
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("not authenticated")
	}
	// v2 fast path; v1 legacy is the migration fallback. Mirrors
	// openVaultE so all three call sites resolve the vault identically.
	userID := resolveUserIDForVault(cfg)
	if masterKey, mkErr := EnsureMasterKey(userID, cfg.DeviceID); mkErr == nil {
		if vs, v2Err := NewVaultStoreV2(masterKey, cfg.DeviceID); v2Err == nil {
			return vs, nil
		} else if !errors.Is(v2Err, ErrVaultIsLegacyV1) {
			return nil, v2Err
		}
	}
	vs, err := NewVaultStoreWithDevice(DerivePassphraseFromToken(cfg.AuthToken), cfg.DeviceID)
	if err == nil {
		migrateVaultToV2(vs, userID, cfg.DeviceID)
	}
	return vs, err
}

func loadVaultEntryOptional(name string) (*VaultEntry, error) {
	vs, err := openVaultOptional()
	if err != nil {
		return nil, err
	}
	entry, err := vs.Get("", name)
	if err != nil || entry == nil || strings.TrimSpace(entry.Value) == "" {
		return nil, nil
	}
	return entry, nil
}

func sanitizeVaultKeySegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	lastDash := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.'
		if ok {
			b.WriteByte(c)
			lastDash = c == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "gitlab"
	}
	return out
}

func gitLabVaultKey(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "gitlab.com") {
		return "gitlab-token"
	}
	return "gitlab-token." + sanitizeVaultKeySegment(host)
}

func gitLabVaultKeyCandidates(host string) []string {
	keys := []string{}
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	if trimmed := strings.TrimSpace(host); trimmed != "" {
		add(gitLabVaultKey(trimmed))
	}
	add("gitlab-token")
	return keys
}

func listGitLabVaultKeysOptional() ([]string, error) {
	vs, err := openVaultOptional()
	if err != nil {
		return nil, err
	}
	summaries := vs.List("")
	keys := make([]string, 0, len(summaries))
	for _, entry := range summaries {
		if entry.Name == "gitlab-token" || strings.HasPrefix(entry.Name, "gitlab-token.") {
			keys = append(keys, entry.Name)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func loadGitLabVaultEntryOptional(host string) (*VaultEntry, string, error) {
	var lastErr error
	for _, key := range gitLabVaultKeyCandidates(host) {
		entry, err := loadVaultEntryOptional(key)
		if err != nil {
			lastErr = err
			continue
		}
		if entry != nil {
			return entry, key, nil
		}
	}
	return nil, "", lastErr
}

func setGitLabVaultEntry(host, token, notes string) (string, error) {
	key := gitLabVaultKey(host)
	return key, setVaultEntry(key, "git-credential", token, notes)
}

func deleteGitLabVaultEntriesOptional(host string) ([]string, error) {
	keys := []string{}
	if strings.TrimSpace(host) != "" {
		keys = gitLabVaultKeyCandidates(host)
		if !strings.EqualFold(strings.TrimSpace(host), "gitlab.com") {
			keys = keys[:1]
		}
	} else {
		var err error
		keys, err = listGitLabVaultKeysOptional()
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			keys = []string{"gitlab-token"}
		}
	}
	removed := make([]string, 0, len(keys))
	for _, key := range uniqueNonEmptyStrings(keys) {
		if err := deleteVaultEntryOptional(key); err != nil {
			return removed, err
		}
		removed = append(removed, key)
	}
	return removed, nil
}

func collectMachineOnboardingStatus() machineOnboardingStatus {
	githubCred := findCredentialForHost("github.com")
	githubProvider := findProvider("github.com")
	githubVault, vaultErr := loadVaultEntryOptional("github-token")

	gitlabHost := "gitlab.com"
	var gitlabCred *GitCredential
	providers, _ := loadGitProviders()
	for _, provider := range providers {
		if !strings.EqualFold(provider.Provider, "gitlab") || strings.TrimSpace(provider.Host) == "" {
			continue
		}
		gitlabHost = provider.Host
		break
	}
	if gitlabHost == "gitlab.com" {
		creds, _ := loadGitCredentials()
		for _, cred := range creds {
			if strings.TrimSpace(cred.Host) == "" {
				continue
			}
			host := strings.ToLower(strings.TrimSpace(cred.Host))
			if host == "gitlab.com" || strings.Contains(host, "gitlab") {
				gitlabHost = cred.Host
				break
			}
		}
	}
	gitlabCred = findCredentialForHost(gitlabHost)
	gitlabProvider := findProvider(gitlabHost)
	if gitlabProvider == nil {
		if p := findProvider("gitlab.com"); p != nil {
			gitlabProvider = p
			gitlabHost = p.Host
		}
	}
	if gitlabCred == nil {
		gitlabCred = findCredentialForHost(gitlabHost)
	}
	gitlabVault, gitlabVaultKey, gitlabVaultErr := loadGitLabVaultEntryOptional(gitlabHost)
	if vaultErr == nil && gitlabVaultErr != nil {
		vaultErr = gitlabVaultErr
	}

	openAIEntry, openAIVaultErr := loadVaultEntryOptional("OPENAI_API_KEY")
	if vaultErr == nil && openAIVaultErr != nil {
		vaultErr = openAIVaultErr
	}
	openAISource := ""
	openAIConfigured := false
	if openAIEntry != nil {
		openAIConfigured = true
		openAISource = "vault:OPENAI_API_KEY"
	} else if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		openAIConfigured = true
		openAISource = "env:OPENAI_API_KEY"
	}

	githubExt := detectGitHubExternalAuth()
	gitlabExt := detectGitLabExternalAuth(gitlabHost)

	githubCloneReady := (githubCred != nil && strings.TrimSpace(githubCred.Token) != "") || githubExt.Configured
	gitlabCloneReady := (gitlabCred != nil && strings.TrimSpace(gitlabCred.Token) != "") || gitlabExt.Configured

	githubCloneSource := ""
	switch {
	case githubCred != nil && strings.TrimSpace(githubCred.Token) != "":
		githubCloneSource = "git-credentials"
	case len(githubExt.Sources) > 0:
		githubCloneSource = strings.Join(githubExt.Sources, "+")
	}
	gitlabCloneSource := ""
	switch {
	case gitlabCred != nil && strings.TrimSpace(gitlabCred.Token) != "":
		gitlabCloneSource = "git-credentials"
	case len(gitlabExt.Sources) > 0:
		gitlabCloneSource = strings.Join(gitlabExt.Sources, "+")
	}

	out := []machineOnboardingProviderStatus{
		{
			ID:         "openai",
			Name:       "OpenAI",
			Ready:      openAIConfigured,
			Configured: openAIConfigured,
			AuthSource: openAISource,
			Detail: func() string {
				if openAISource != "" {
					return "API key ready via " + openAISource
				}
				return "Not configured"
			}(),
		},
		{
			ID:          "github",
			Name:        "GitHub",
			Host:        "github.com",
			CloneReady:  githubCloneReady,
			CIReady:     githubVault != nil,
			Ready:       githubCloneReady && githubVault != nil,
			Configured:  githubCloneReady || githubVault != nil,
			CloneSource: githubCloneSource,
			CISource: func() string {
				if githubVault != nil {
					return "vault:github-token"
				}
				return ""
			}(),
			Username: firstNonEmpty(func() string {
				if githubProvider != nil {
					return githubProvider.Username
				}
				return ""
			}(), func() string {
				if githubCred != nil {
					return githubCred.Username
				}
				return ""
			}(), githubExt.Username),
		},
		{
			ID:          "gitlab",
			Name:        "GitLab",
			Host:        gitlabHost,
			CloneReady:  gitlabCloneReady,
			CIReady:     gitlabVault != nil,
			Ready:       gitlabCloneReady && gitlabVault != nil,
			Configured:  gitlabCloneReady || gitlabVault != nil,
			CloneSource: gitlabCloneSource,
			CISource: func() string {
				if gitlabVault != nil {
					return "vault:" + gitlabVaultKey
				}
				return ""
			}(),
			Username: firstNonEmpty(func() string {
				if gitlabProvider != nil {
					return gitlabProvider.Username
				}
				return ""
			}(), func() string {
				if gitlabCred != nil {
					return gitlabCred.Username
				}
				return ""
			}(), gitlabExt.Username),
		},
	}

	vaultWarning := ""
	if vaultErr != nil {
		vaultWarning = "Vault unavailable: " + vaultErr.Error()
	}

	for i := range out {
		if out[i].ID == "openai" {
			if !out[i].Configured {
				out[i].Warning = "No OPENAI_API_KEY configured"
				out[i].Detail = "No OPENAI_API_KEY configured"
			}
			if vaultWarning != "" {
				if out[i].Warning == "" {
					out[i].Warning = vaultWarning
				} else {
					out[i].Warning = out[i].Warning + "; " + vaultWarning
				}
			}
			continue
		}
		if out[i].Ready {
			out[i].Detail = fmt.Sprintf("Clone and CI tokens ready for %s", out[i].Host)
		} else if out[i].Configured {
			switch {
			case out[i].CloneReady:
				out[i].Warning = "Clone token present, CI/deploy vault token missing"
				out[i].Detail = out[i].Warning
			case out[i].CIReady:
				out[i].Warning = "CI/deploy token present, clone token missing"
				out[i].Detail = out[i].Warning
			}
		} else {
			out[i].Warning = "Not configured"
			out[i].Detail = "Not configured"
		}
		if vaultWarning != "" && !out[i].CIReady {
			if out[i].Warning == "" {
				out[i].Warning = vaultWarning
			} else {
				out[i].Warning = out[i].Warning + "; " + vaultWarning
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return machineOnboardingStatus{Providers: out}
}

func upsertGitCredential(host, username, token string) error {
	creds, _ := loadGitCredentials()
	found := false
	for i := range creds {
		if strings.EqualFold(creds[i].Host, host) {
			creds[i].Username = username
			creds[i].Token = token
			found = true
			break
		}
	}
	if !found {
		creds = append(creds, GitCredential{Host: host, Username: username, Token: token})
	}
	return saveGitCredentials(creds)
}

func upsertGitProvider(host, provider, username, avatarURL, token string) error {
	providers, _ := loadGitProviders()
	gp := GitProvider{
		Host:      host,
		Provider:  provider,
		Username:  username,
		Token:     token,
		AvatarURL: avatarURL,
		SetupAt:   time.Now().UTC().Format(time.RFC3339),
	}
	found := false
	for i := range providers {
		if strings.EqualFold(providers[i].Host, host) {
			gp.SSHKeyName = providers[i].SSHKeyName
			gp.SSHKeyPath = providers[i].SSHKeyPath
			providers[i] = gp
			found = true
			break
		}
	}
	if !found {
		providers = append(providers, gp)
	}
	return saveGitProviders(providers)
}

func setVaultEntry(name, category, value, notes string) error {
	vs, err := openVaultOptional()
	if err != nil {
		return err
	}
	return vs.Set(VaultEntry{
		Name:     name,
		Category: category,
		Value:    value,
		Notes:    notes,
	})
}

func deleteVaultEntryOptional(name string) error {
	vs, err := openVaultOptional()
	if err != nil {
		return err
	}
	if err := vs.Delete("", name); err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	return nil
}

func removeGitCredentialsForHost(host string) (bool, error) {
	creds, _ := loadGitCredentials()
	filtered := make([]GitCredential, 0, len(creds))
	removed := false
	for _, c := range creds {
		if strings.EqualFold(c.Host, host) {
			removed = true
			continue
		}
		filtered = append(filtered, c)
	}
	if !removed {
		return false, nil
	}
	return true, saveGitCredentials(filtered)
}

func removeGitProvidersByMatch(match func(GitProvider) bool) (bool, error) {
	providers, _ := loadGitProviders()
	filtered := make([]GitProvider, 0, len(providers))
	removed := false
	for _, p := range providers {
		if match(p) {
			removed = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !removed {
		return false, nil
	}
	return true, saveGitProviders(filtered)
}

func removeGitCredentialsByMatch(match func(GitCredential) bool) (bool, error) {
	creds, _ := loadGitCredentials()
	filtered := make([]GitCredential, 0, len(creds))
	removed := false
	for _, c := range creds {
		if match(c) {
			removed = true
			continue
		}
		filtered = append(filtered, c)
	}
	if !removed {
		return false, nil
	}
	return true, saveGitCredentials(filtered)
}

func applyMachineOnboardingRemoveLocal(req machineOnboardingRemoveRequest) (map[string]any, error) {
	providers := uniqueNonEmptyStrings(req.Providers)
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers requested")
	}
	removeClone := boolOrDefault(req.RemoveClone, true)
	removeCIToken := boolOrDefault(req.RemoveCIToken, true)
	removed := []string{}

	for _, provider := range providers {
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "github":
			if removeClone {
				if ok, err := removeGitProvidersByMatch(func(p GitProvider) bool { return strings.EqualFold(p.Provider, "github") || strings.EqualFold(p.Host, "github.com") }); err != nil {
					return nil, err
				} else if ok {
					removed = append(removed, "github.clone")
				}
				if ok, err := removeGitCredentialsByMatch(func(c GitCredential) bool { return strings.EqualFold(c.Host, "github.com") }); err != nil {
					return nil, err
				} else if ok && !containsOnboardingMarker(removed, "github.clone") {
					removed = append(removed, "github.clone")
				}
			}
			if removeCIToken {
				if err := deleteVaultEntryOptional("github-token"); err == nil {
					removed = append(removed, "github.ci")
				}
			}
		case "gitlab":
			targetHost := strings.TrimSpace(req.GitLabHost)
			if removeClone {
				matchProvider := func(p GitProvider) bool {
					if !strings.EqualFold(p.Provider, "gitlab") {
						return false
					}
					return targetHost == "" || strings.EqualFold(p.Host, targetHost)
				}
				matchCred := func(c GitCredential) bool {
					if targetHost != "" {
						return strings.EqualFold(c.Host, targetHost)
					}
					return strings.Contains(strings.ToLower(strings.TrimSpace(c.Host)), "gitlab")
				}
				if ok, err := removeGitProvidersByMatch(matchProvider); err != nil {
					return nil, err
				} else if ok {
					removed = append(removed, "gitlab.clone")
				}
				if ok, err := removeGitCredentialsByMatch(matchCred); err != nil {
					return nil, err
				} else if ok && !containsOnboardingMarker(removed, "gitlab.clone") {
					removed = append(removed, "gitlab.clone")
				}
			}
			if removeCIToken {
				if keys, err := deleteGitLabVaultEntriesOptional(targetHost); err != nil {
					return nil, err
				} else if len(keys) > 0 {
					removed = append(removed, "gitlab.ci")
				}
			}
		default:
			return nil, fmt.Errorf("unsupported provider %q", provider)
		}
	}

	return map[string]any{
		"ok":        true,
		"removed":   removed,
		"providers": collectMachineOnboardingStatus().Providers,
	}, nil
}

func applyMachineOnboardingLocal(req machineOnboardingApplyRequest) (map[string]any, error) {
	applied := []string{}
	notes := strings.TrimSpace(req.Notes)
	if strings.TrimSpace(req.OpenAIAPIKey) != "" {
		// Deprecation per feedback_no_api_keys_subscription_only: API-key
		// machine-onboarding bills against the per-call OpenAI API tier,
		// NOT the user's ChatGPT Plus subscription. We still accept the
		// key for back-compat (existing scripts depend on it) but the
		// next-generation path is `yaver runner-auth browser-start codex`
		// + the mobile Runner Auth flow that mirrors subscription OAuth
		// over P2P. Log a visible warning so users see the migration.
		log.Printf("[machine-onboarding] DEPRECATED: applying OPENAI_API_KEY via onboarding double-bills against the API tier. Switch to ChatGPT Plus subscription OAuth via Yaver mobile or 'yaver runner-auth browser-start codex'.")
		if notes == "" {
			notes = "Set by yaver machine onboarding (API-key path is DEPRECATED — switch to OAuth)."
		}
		if err := setVaultEntry("OPENAI_API_KEY", "api-key", strings.TrimSpace(req.OpenAIAPIKey), notes); err != nil {
			return nil, err
		}
		applied = append(applied, "OPENAI_API_KEY")
	}

	applyClone := boolOrDefault(req.ApplyClone, true)
	applyCIToken := boolOrDefault(req.ApplyCIToken, true)

	if token := strings.TrimSpace(req.GitHubToken); token != "" {
		username, avatarURL, err := verifyGitHubToken(token)
		if err != nil {
			return nil, err
		}
		if applyClone {
			if err := upsertGitCredential("github.com", username, token); err != nil {
				return nil, err
			}
			if err := upsertGitProvider("github.com", "github", username, avatarURL, token); err != nil {
				return nil, err
			}
			applied = append(applied, "github.clone")
		}
		if applyCIToken {
			vaultNotes := fmt.Sprintf("github PAT for github.com (%s)", username)
			if err := setVaultEntry("github-token", "git-credential", token, vaultNotes); err != nil {
				return nil, err
			}
			applied = append(applied, "github.ci")
		}
	}

	if token := strings.TrimSpace(req.GitLabToken); token != "" {
		host := strings.TrimSpace(req.GitLabHost)
		if host == "" {
			host = "gitlab.com"
		}
		username, avatarURL, err := verifyGitLabToken(host, token)
		if err != nil {
			return nil, err
		}
		if applyClone {
			if err := upsertGitCredential(host, username, token); err != nil {
				return nil, err
			}
			if err := upsertGitProvider(host, "gitlab", username, avatarURL, token); err != nil {
				return nil, err
			}
			applied = append(applied, "gitlab.clone")
		}
		if applyCIToken {
			vaultNotes := fmt.Sprintf("gitlab PAT for %s (%s)", host, username)
			if _, err := setGitLabVaultEntry(host, token, vaultNotes); err != nil {
				return nil, err
			}
			applied = append(applied, "gitlab.ci")
		}
	}

	return map[string]any{
		"ok":        true,
		"applied":   applied,
		"providers": collectMachineOnboardingStatus().Providers,
	}, nil
}

func fetchMachineOnboardingStatusRemote(target string) (machineOnboardingStatus, error) {
	out, err := proxyToDeviceJSON(context.Background(), "machine-onboarding-status", target, http.MethodGet, "/machine/onboarding/status", nil)
	if err != nil {
		return machineOnboardingStatus{}, err
	}
	raw, _ := out["providers"].([]any)
	providers := make([]machineOnboardingProviderStatus, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		providers = append(providers, machineOnboardingProviderStatus{
			ID:          stringValue(m["id"]),
			Name:        stringValue(m["name"]),
			Ready:       boolValue(m["ready"]),
			Configured:  boolValue(m["configured"]),
			CloneReady:  boolValue(m["cloneReady"]),
			CIReady:     boolValue(m["ciReady"]),
			AuthSource:  stringValue(m["authSource"]),
			CloneSource: stringValue(m["cloneSource"]),
			CISource:    stringValue(m["ciSource"]),
			Username:    stringValue(m["username"]),
			Host:        stringValue(m["host"]),
			Detail:      stringValue(m["detail"]),
			Warning:     stringValue(m["warning"]),
		})
	}
	return machineOnboardingStatus{Providers: providers}, nil
}

func applyMachineOnboardingRemote(target string, req machineOnboardingApplyRequest) (map[string]any, error) {
	return proxyToDeviceJSON(context.Background(), "machine-onboarding-apply", target, http.MethodPost, "/machine/onboarding/apply", req)
}

func removeMachineOnboardingRemote(target string, req machineOnboardingRemoveRequest) (map[string]any, error) {
	return proxyToDeviceJSON(context.Background(), "machine-onboarding-remove", target, http.MethodPost, "/machine/onboarding/remove", req)
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func mcpMachineOnboardingStatusMulti(deviceIDs []string) interface{} {
	deviceIDs = uniqueNonEmptyStrings(deviceIDs)
	if len(deviceIDs) == 0 {
		return mcpMachineOnboardingStatus("")
	}
	results := make([]map[string]any, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		status, err := fetchMachineOnboardingStatusRemote(deviceID)
		if err != nil {
			results = append(results, map[string]any{
				"device_id": deviceID,
				"error":     err.Error(),
			})
			continue
		}
		results = append(results, map[string]any{
			"device_id": deviceID,
			"providers": status.Providers,
		})
	}
	return map[string]any{"results": results}
}

func mcpMachineOnboardingStatus(deviceID string) interface{} {
	if strings.TrimSpace(deviceID) != "" {
		status, err := fetchMachineOnboardingStatusRemote(strings.TrimSpace(deviceID))
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return map[string]any{"providers": status.Providers, "device_id": strings.TrimSpace(deviceID)}
	}
	status := collectMachineOnboardingStatus()
	return map[string]any{"providers": status.Providers}
}

func mcpMachineOnboardingApply(deviceID string, req machineOnboardingApplyRequest) interface{} {
	var (
		result map[string]any
		err    error
	)
	if strings.TrimSpace(deviceID) != "" {
		result, err = applyMachineOnboardingRemote(strings.TrimSpace(deviceID), req)
	} else {
		result, err = applyMachineOnboardingLocal(req)
	}
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return result
}

func mcpMachineOnboardingApplyMulti(deviceIDs []string, req machineOnboardingApplyRequest) interface{} {
	deviceIDs = uniqueNonEmptyStrings(deviceIDs)
	if len(deviceIDs) == 0 {
		return mcpMachineOnboardingApply("", req)
	}
	results := make([]map[string]any, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		result, err := applyMachineOnboardingRemote(deviceID, req)
		if err != nil {
			results = append(results, map[string]any{
				"device_id": deviceID,
				"ok":        false,
				"error":     err.Error(),
			})
			continue
		}
		if result == nil {
			result = map[string]any{}
		}
		result["device_id"] = deviceID
		results = append(results, result)
	}
	return map[string]any{"results": results}
}

func mcpMachineOnboardingRemove(deviceID string, req machineOnboardingRemoveRequest) interface{} {
	var (
		result map[string]any
		err    error
	)
	if strings.TrimSpace(deviceID) != "" {
		result, err = removeMachineOnboardingRemote(strings.TrimSpace(deviceID), req)
	} else {
		result, err = applyMachineOnboardingRemoveLocal(req)
	}
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return result
}

func mcpMachineOnboardingRemoveMulti(deviceIDs []string, req machineOnboardingRemoveRequest) interface{} {
	deviceIDs = uniqueNonEmptyStrings(deviceIDs)
	if len(deviceIDs) == 0 {
		return mcpMachineOnboardingRemove("", req)
	}
	results := make([]map[string]any, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		result, err := removeMachineOnboardingRemote(deviceID, req)
		if err != nil {
			results = append(results, map[string]any{
				"device_id": deviceID,
				"ok":        false,
				"error":     err.Error(),
			})
			continue
		}
		if result == nil {
			result = map[string]any{}
		}
		result["device_id"] = deviceID
		results = append(results, result)
	}
	return map[string]any{"results": results}
}

func containsOnboardingMarker(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}

func machineOnboardingDoctorLevel(provider machineOnboardingProviderStatus) string {
	if provider.Ready {
		return "pass"
	}
	if provider.Configured {
		return "warn"
	}
	return "fail"
}

func machineOnboardingDoctorDetail(provider machineOnboardingProviderStatus) string {
	if detail := strings.TrimSpace(provider.Detail); detail != "" {
		return detail
	}
	if warning := strings.TrimSpace(provider.Warning); warning != "" {
		return warning
	}
	return "Not configured"
}
