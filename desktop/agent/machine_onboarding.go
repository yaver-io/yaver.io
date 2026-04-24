package main

import (
	"context"
	"fmt"
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

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func openVaultOptional() (*VaultStore, error) {
	passphrase := strings.TrimSpace(os.Getenv("YAVER_VAULT_PASSPHRASE"))
	if passphrase == "" {
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
			return nil, fmt.Errorf("not authenticated")
		}
		passphrase = DerivePassphraseFromToken(cfg.AuthToken)
	}
	return NewVaultStore(passphrase)
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

func collectMachineOnboardingStatus() machineOnboardingStatus {
	githubCred := findCredentialForHost("github.com")
	githubProvider := findProvider("github.com")
	githubVault, vaultErr := loadVaultEntryOptional("github-token")

	gitlabHost := "gitlab.com"
	var gitlabCred *GitCredential
	for _, host := range []string{"gitlab.com"} {
		if cred := findCredentialForHost(host); cred != nil && strings.TrimSpace(cred.Token) != "" {
			gitlabHost = host
			gitlabCred = cred
			break
		}
	}
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
	gitlabVault, gitlabVaultErr := loadVaultEntryOptional("gitlab-token")
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
			ID:         "github",
			Name:       "GitHub",
			Host:       "github.com",
			CloneReady: githubCred != nil && strings.TrimSpace(githubCred.Token) != "",
			CIReady:    githubVault != nil,
			Ready:      githubCred != nil && strings.TrimSpace(githubCred.Token) != "" && githubVault != nil,
			Configured: (githubCred != nil && strings.TrimSpace(githubCred.Token) != "") || githubVault != nil,
			CloneSource: func() string {
				if githubCred != nil && strings.TrimSpace(githubCred.Token) != "" {
					return "git-credentials"
				}
				return ""
			}(),
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
			}()),
		},
		{
			ID:         "gitlab",
			Name:       "GitLab",
			Host:       gitlabHost,
			CloneReady: gitlabCred != nil && strings.TrimSpace(gitlabCred.Token) != "",
			CIReady:    gitlabVault != nil,
			Ready:      gitlabCred != nil && strings.TrimSpace(gitlabCred.Token) != "" && gitlabVault != nil,
			Configured: (gitlabCred != nil && strings.TrimSpace(gitlabCred.Token) != "") || gitlabVault != nil,
			CloneSource: func() string {
				if gitlabCred != nil && strings.TrimSpace(gitlabCred.Token) != "" {
					return "git-credentials"
				}
				return ""
			}(),
			CISource: func() string {
				if gitlabVault != nil {
					return "vault:gitlab-token"
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
			}()),
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

func applyMachineOnboardingLocal(req machineOnboardingApplyRequest) (map[string]any, error) {
	applied := []string{}
	notes := strings.TrimSpace(req.Notes)
	if strings.TrimSpace(req.OpenAIAPIKey) != "" {
		if notes == "" {
			notes = "Set by yaver machine onboarding."
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
			if err := setVaultEntry("gitlab-token", "git-credential", token, vaultNotes); err != nil {
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
