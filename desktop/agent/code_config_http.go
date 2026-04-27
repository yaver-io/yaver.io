package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type CodeConfigSummary struct {
	Config     *CodeCLIConfig         `json:"config"`
	TargetInfo map[string]interface{} `json:"targetInfo,omitempty"`
	Context    map[string]interface{} `json:"context,omitempty"`
	OpenCode   *OpenCodeConfigSummary `json:"openCode,omitempty"`
}

type codeConfigPatchRequest struct {
	Runner             *string `json:"runner,omitempty"`
	Model              *string `json:"model,omitempty"`
	Provider           *string `json:"provider,omitempty"`
	BaseURL            *string `json:"baseUrl,omitempty"`
	OrchestrationMode  *string `json:"orchestrationMode,omitempty"`
	WorkMode           *string `json:"workMode,omitempty"`
	AttachedDeviceID   *string `json:"attachedDeviceId,omitempty"`
	AttachedDeviceName *string `json:"attachedDeviceName,omitempty"`
	RepoPath           *string `json:"repoPath,omitempty"`
	RepoRemote         *bool   `json:"repoRemote,omitempty"`
	BYOKProvider       *string `json:"byokProvider,omitempty"`
	BYOKAPIKey         *string `json:"byokApiKey,omitempty"`
	SmallModel         *string `json:"smallModel,omitempty"`
	PlanModel          *string `json:"planModel,omitempty"`
	BuildModel         *string `json:"buildModel,omitempty"`
}

func (s *HTTPServer) handleCodeConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		summary, err := buildCodeConfigSummary()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "code config: "+err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "code": summary})
	case http.MethodPost, http.MethodPatch:
		var patch codeConfigPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		summary, err := applyCodeConfigPatch(patch)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "code config update: "+err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "code": summary})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func buildCodeConfigSummary() (*CodeConfigSummary, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	summary := &CodeConfigSummary{Config: profile}
	if info, _, err := codeTargetInfo(); err == nil {
		summary.TargetInfo = info
	}
	if ctx, err := codeCurrentContext(codeAttachedDevice(profile)); err == nil {
		summary.Context = ctx
	}
	if normalizeRunnerID(profile.Runner) == "opencode" {
		if oc, err := codeGetOpenCodeConfig(codeAttachedDevice(profile)); err == nil {
			summary.OpenCode = oc
		}
	}
	return summary, nil
}

func applyCodeConfigPatch(patch codeConfigPatchRequest) (*CodeConfigSummary, error) {
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	if patch.AttachedDeviceID != nil {
		profile.AttachedDeviceID = strings.TrimSpace(*patch.AttachedDeviceID)
	}
	if patch.AttachedDeviceName != nil {
		profile.AttachedDeviceName = strings.TrimSpace(*patch.AttachedDeviceName)
	}
	if patch.WorkMode != nil {
		profile.WorkMode = strings.TrimSpace(*patch.WorkMode)
	}
	targetDeviceID := codeAttachedDevice(profile)
	if patch.Runner != nil {
		runner := normalizeRunnerID(strings.TrimSpace(*patch.Runner))
		if runner != "" {
			if err := codeSwitchRunner(targetDeviceID, runner); err != nil {
				return nil, err
			}
			profile.Runner = runner
		}
	}
	if patch.Model != nil {
		model := strings.TrimSpace(*patch.Model)
		profile.Model = model
		if provider := providerFromModel(model); provider != "" {
			profile.Provider = provider
		}
		if normalizeRunnerID(profile.Runner) == "opencode" && model != "" {
			if err := codePatchOpenCode(targetDeviceID, map[string]string{"model": model}); err != nil {
				return nil, err
			}
		}
	}
	if patch.Provider != nil {
		profile.Provider = normalizeOpenCodeProvider(*patch.Provider)
	}
	if patch.BaseURL != nil {
		profile.BaseURL = strings.TrimSpace(*patch.BaseURL)
	}
	if patch.OrchestrationMode != nil {
		profile.OrchestrationMode = strings.ToLower(strings.TrimSpace(*patch.OrchestrationMode))
	}
	if patch.RepoPath != nil {
		repoPath := strings.TrimSpace(*patch.RepoPath)
		profile.RepoPath = repoPath
		if repoPath != "" {
			if targetDeviceID == "" {
				if err := codeSetLocalWorkDir(repoPath); err != nil {
					return nil, err
				}
			} else {
				if err := codeSetRemoteWorkDir(targetDeviceID, repoPath); err != nil {
					return nil, err
				}
			}
		}
	}
	if patch.RepoRemote != nil {
		profile.RepoRemote = *patch.RepoRemote
	}
	if patch.BYOKProvider != nil {
		req := codeBYOKApplyRequest{
			Provider:   strings.TrimSpace(*patch.BYOKProvider),
			APIKey:     stringPtrValue(patch.BYOKAPIKey),
			Model:      stringPtrValue(patch.Model),
			BaseURL:    stringPtrValue(patch.BaseURL),
			SmallModel: stringPtrValue(patch.SmallModel),
			PlanModel:  stringPtrValue(patch.PlanModel),
			BuildModel: stringPtrValue(patch.BuildModel),
		}
		if err := applyCodeBYOKConfig(cfg, profile, req); err != nil {
			return nil, err
		}
	} else if normalizeRunnerID(profile.Runner) == "opencode" {
		ocPatch := openCodeConfigPatch{}
		if patch.SmallModel != nil {
			v := strings.TrimSpace(*patch.SmallModel)
			ocPatch.SmallModel = &v
		}
		if patch.PlanModel != nil {
			v := strings.TrimSpace(*patch.PlanModel)
			ocPatch.PlanModel = &v
		}
		if patch.BuildModel != nil {
			v := strings.TrimSpace(*patch.BuildModel)
			ocPatch.BuildModel = &v
		}
		if patch.BaseURL != nil || patch.Provider != nil {
			provider := firstNonEmpty(strings.TrimSpace(profile.Provider), providerFromModel(profile.Model), "openrouter")
			ocPatch.Providers = []openCodeProviderPatch{{
				ID:      provider,
				Name:    openCodeProviderDisplayName(provider),
				BaseURL: strings.TrimSpace(profile.BaseURL),
			}}
		}
		if ocPatch.SmallModel != nil || ocPatch.PlanModel != nil || ocPatch.BuildModel != nil || len(ocPatch.Providers) > 0 {
			if err := codePatchOpenCodeConfig(targetDeviceID, ocPatch); err != nil {
				return nil, err
			}
		}
	}
	if err := saveCodeConfig(cfg); err != nil {
		return nil, err
	}
	return buildCodeConfigSummary()
}

type codeBYOKApplyRequest struct {
	Provider   string
	APIKey     string
	Model      string
	BaseURL    string
	SmallModel string
	PlanModel  string
	BuildModel string
}

func applyCodeBYOKConfig(cfg *Config, profile *CodeCLIConfig, req codeBYOKApplyRequest) error {
	provider := normalizeOpenCodeProvider(req.Provider)
	if provider == "" {
		return nil
	}
	targetDeviceID := codeAttachedDevice(profile)
	if normalizeRunnerID(profile.Runner) != "opencode" {
		if err := codeSwitchRunner(targetDeviceID, "opencode"); err != nil {
			return err
		}
		profile.Runner = "opencode"
	}
	patch := openCodeConfigPatch{
		Providers: []openCodeProviderPatch{{
			ID:      provider,
			Name:    openCodeProviderDisplayName(provider),
			BaseURL: firstNonEmpty(strings.TrimSpace(req.BaseURL), defaultOpenCodeProviderBaseURL(provider)),
			APIKey:  strings.TrimSpace(req.APIKey),
		}},
	}
	if v := strings.TrimSpace(req.Model); v != "" {
		patch.Model = &v
		profile.Model = v
	}
	if v := strings.TrimSpace(req.SmallModel); v != "" {
		patch.SmallModel = &v
	}
	if v := strings.TrimSpace(req.PlanModel); v != "" {
		patch.PlanModel = &v
	}
	if v := strings.TrimSpace(req.BuildModel); v != "" {
		patch.BuildModel = &v
	}
	if err := codePatchOpenCodeConfig(targetDeviceID, patch); err != nil {
		return err
	}
	profile.Provider = provider
	if trimmed := strings.TrimSpace(req.BaseURL); trimmed != "" {
		profile.BaseURL = trimmed
	} else if profile.BaseURL == "" {
		profile.BaseURL = defaultOpenCodeProviderBaseURL(provider)
	}
	if cfg != nil {
		return saveCodeConfig(cfg)
	}
	return nil
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func stringPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	s := strings.TrimSpace(v)
	return &s
}
