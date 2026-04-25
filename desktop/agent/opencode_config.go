package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type OpenCodeModelSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
	Source    string `json:"source,omitempty"`
}

type OpenCodeProviderSummary struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name,omitempty"`
	BaseURL string                 `json:"baseUrl,omitempty"`
	Models  []OpenCodeModelSummary `json:"models,omitempty"`
}

type OpenCodeConfigSummary struct {
	Path         string                    `json:"path"`
	Exists       bool                      `json:"exists"`
	DefaultAgent string                    `json:"defaultAgent,omitempty"`
	Model        string                    `json:"model,omitempty"`
	SmallModel   string                    `json:"smallModel,omitempty"`
	BuildModel   string                    `json:"buildModel,omitempty"`
	PlanModel    string                    `json:"planModel,omitempty"`
	Providers    []OpenCodeProviderSummary `json:"providers,omitempty"`
	Models       []OpenCodeModelSummary    `json:"models,omitempty"`
}

type openCodeConfigPatch struct {
	DefaultAgent *string `json:"defaultAgent,omitempty"`
	Model        *string `json:"model,omitempty"`
	SmallModel   *string `json:"smallModel,omitempty"`
	BuildModel   *string `json:"buildModel,omitempty"`
	PlanModel    *string `json:"planModel,omitempty"`
}

func (s *HTTPServer) handleOpenCodeConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := loadOpenCodeConfigSummary()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "opencode config: "+err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "config": cfg})
	case http.MethodPost, http.MethodPatch:
		var patch openCodeConfigPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		cfg, err := applyOpenCodeConfigPatch(patch)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "opencode config update: "+err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "config": cfg})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func loadOpenCodeConfigSummary() (OpenCodeConfigSummary, error) {
	path, cfg, exists, err := loadOpenCodeGlobalConfigMap()
	if err != nil {
		return OpenCodeConfigSummary{}, err
	}
	return summarizeOpenCodeConfig(path, cfg, exists), nil
}

func applyOpenCodeConfigPatch(patch openCodeConfigPatch) (OpenCodeConfigSummary, error) {
	path, cfg, _, err := loadOpenCodeGlobalConfigMap()
	if err != nil {
		return OpenCodeConfigSummary{}, err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["$schema"]; !ok {
		cfg["$schema"] = "https://opencode.ai/config.json"
	}
	apply := func(key string, value *string) {
		if value == nil {
			return
		}
		v := strings.TrimSpace(*value)
		if v == "" {
			delete(cfg, key)
			return
		}
		cfg[key] = v
	}
	apply("default_agent", patch.DefaultAgent)
	apply("model", patch.Model)
	apply("small_model", patch.SmallModel)
	if patch.BuildModel != nil {
		setOpenCodeAgentModel(cfg, "build", strings.TrimSpace(*patch.BuildModel))
	}
	if patch.PlanModel != nil {
		setOpenCodeAgentModel(cfg, "plan", strings.TrimSpace(*patch.PlanModel))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return OpenCodeConfigSummary{}, err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return OpenCodeConfigSummary{}, err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return OpenCodeConfigSummary{}, err
	}
	return summarizeOpenCodeConfig(path, cfg, true), nil
}

func setOpenCodeAgentModel(cfg map[string]any, agentName, model string) {
	agentNode, ok := cfg["agent"].(map[string]any)
	if !ok || agentNode == nil {
		agentNode = map[string]any{}
		cfg["agent"] = agentNode
	}
	entry, ok := agentNode[agentName].(map[string]any)
	if !ok || entry == nil {
		entry = map[string]any{}
		agentNode[agentName] = entry
	}
	if model == "" {
		delete(entry, "model")
		if len(entry) == 0 {
			delete(agentNode, agentName)
		}
		if len(agentNode) == 0 {
			delete(cfg, "agent")
		}
		return
	}
	entry["model"] = model
}

func summarizeOpenCodeConfig(path string, cfg map[string]any, exists bool) OpenCodeConfigSummary {
	summary := OpenCodeConfigSummary{
		Path:   path,
		Exists: exists,
	}
	summary.DefaultAgent, _ = stringFromMap(cfg, "default_agent")
	summary.Model, _ = stringFromMap(cfg, "model")
	summary.SmallModel, _ = stringFromMap(cfg, "small_model")
	summary.BuildModel = openCodeAgentModel(cfg, "build")
	summary.PlanModel = openCodeAgentModel(cfg, "plan")
	summary.Providers = openCodeProvidersFromConfig(cfg)
	summary.Models = openCodeModelsFromConfig(cfg)
	return summary
}

func openCodeAgentModel(cfg map[string]any, agentName string) string {
	agentNode, ok := cfg["agent"].(map[string]any)
	if !ok || agentNode == nil {
		// Legacy mode docs still show mode.build/model, keep reading it.
		modeNode, ok := cfg["mode"].(map[string]any)
		if !ok || modeNode == nil {
			return ""
		}
		entry, ok := modeNode[agentName].(map[string]any)
		if !ok || entry == nil {
			return ""
		}
		model, _ := stringFromMap(entry, "model")
		return model
	}
	entry, ok := agentNode[agentName].(map[string]any)
	if !ok || entry == nil {
		return ""
	}
	model, _ := stringFromMap(entry, "model")
	return model
}

func openCodeProvidersFromConfig(cfg map[string]any) []OpenCodeProviderSummary {
	providersNode, ok := cfg["provider"].(map[string]any)
	if !ok || providersNode == nil {
		return nil
	}
	ids := make([]string, 0, len(providersNode))
	for id := range providersNode {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]OpenCodeProviderSummary, 0, len(ids))
	for _, id := range ids {
		entry, ok := providersNode[id].(map[string]any)
		if !ok {
			continue
		}
		row := OpenCodeProviderSummary{ID: id}
		row.Name, _ = stringFromMap(entry, "name")
		if options, ok := entry["options"].(map[string]any); ok {
			row.BaseURL, _ = stringFromMap(options, "baseURL")
			if row.BaseURL == "" {
				row.BaseURL, _ = stringFromMap(options, "baseUrl")
			}
		}
		if modelsNode, ok := entry["models"].(map[string]any); ok {
			modelIDs := make([]string, 0, len(modelsNode))
			for modelID := range modelsNode {
				modelIDs = append(modelIDs, modelID)
			}
			sort.Strings(modelIDs)
			row.Models = make([]OpenCodeModelSummary, 0, len(modelIDs))
			for _, modelID := range modelIDs {
				name := modelID
				if modelCfg, ok := modelsNode[modelID].(map[string]any); ok {
					if modelName, _ := stringFromMap(modelCfg, "name"); modelName != "" {
						name = modelName
					}
				}
				row.Models = append(row.Models, OpenCodeModelSummary{
					ID:       id + "/" + modelID,
					Name:     name,
					Provider: id,
					Source:   "provider",
				})
			}
		}
		out = append(out, row)
	}
	return out
}

func openCodeModelsFromConfig(cfg map[string]any) []OpenCodeModelSummary {
	seen := map[string]OpenCodeModelSummary{}
	appendModel := func(model OpenCodeModelSummary) {
		if strings.TrimSpace(model.ID) == "" {
			return
		}
		if existing, ok := seen[model.ID]; ok {
			if !existing.IsDefault && model.IsDefault {
				existing.IsDefault = true
				seen[model.ID] = existing
			}
			return
		}
		seen[model.ID] = model
	}
	for _, provider := range openCodeProvidersFromConfig(cfg) {
		for _, model := range provider.Models {
			appendModel(model)
		}
	}
	defaults := []string{
		openCodeAgentModel(cfg, "build"),
		openCodeAgentModel(cfg, "plan"),
	}
	if model, _ := stringFromMap(cfg, "model"); model != "" {
		defaults = append([]string{model}, defaults...)
	}
	if smallModel, _ := stringFromMap(cfg, "small_model"); smallModel != "" {
		defaults = append(defaults, smallModel)
	}
	for i, id := range defaults {
		if strings.TrimSpace(id) == "" {
			continue
		}
		row := seen[id]
		if row.ID == "" {
			row = OpenCodeModelSummary{ID: id, Name: id, Source: "config"}
		}
		if i == 0 {
			row.IsDefault = true
		}
		if row.Provider == "" {
			if prefix, _, ok := strings.Cut(id, "/"); ok {
				row.Provider = prefix
			}
		}
		appendModel(row)
	}
	out := make([]OpenCodeModelSummary, 0, len(seen))
	for _, row := range seen {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func loadOpenCodeGlobalConfigMap() (string, map[string]any, bool, error) {
	path := preferredOpenCodeConfigPath()
	if !runnerFileExists(path) {
		return path, map[string]any{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, nil, true, err
	}
	clean := stripJSONC(raw)
	cfg := map[string]any{}
	if strings.TrimSpace(string(clean)) == "" {
		return path, cfg, true, nil
	}
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return path, nil, true, fmt.Errorf("%s: %w", path, err)
	}
	return path, cfg, true, nil
}

func preferredOpenCodeConfigPath() string {
	if file := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); file != "" {
		return file
	}
	paths := openCodeGlobalConfigPaths()
	for _, path := range paths {
		if runnerFileExists(path) {
			return path
		}
	}
	if len(paths) == 0 {
		return filepath.Join(".", "opencode.json")
	}
	return paths[0]
}

func openCodeGlobalConfigPaths() []string {
	var out []string
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); dir != "" {
		out = append(out,
			filepath.Join(dir, "opencode.jsonc"),
			filepath.Join(dir, "opencode.json"),
		)
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		out = append(out,
			filepath.Join(xdg, "opencode", "opencode.jsonc"),
			filepath.Join(xdg, "opencode", "opencode.json"),
		)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
			filepath.Join(home, ".config", "opencode", "opencode.json"),
			filepath.Join(home, ".opencode.jsonc"),
			filepath.Join(home, ".opencode.json"),
		)
	}
	return uniqStrings(out)
}

func stringFromMap(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return "", false
	}
	if s, ok := raw.(string); ok {
		return s, true
	}
	return fmt.Sprintf("%v", raw), true
}

func stripJSONC(raw []byte) []byte {
	noComments := stripJSONComments(raw)
	return stripJSONTrailingCommas(noComments)
}

func stripJSONComments(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inString := false
	escape := false
	inLine := false
	inBlock := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inLine {
			if c == '\n' {
				inLine = false
				out = append(out, c)
			}
			continue
		}
		if inBlock {
			if c == '*' && i+1 < len(raw) && raw[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		if inString {
			out = append(out, c)
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(raw) {
			switch raw[i+1] {
			case '/':
				inLine = true
				i++
				continue
			case '*':
				inBlock = true
				i++
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func stripJSONTrailingCommas(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inString := false
	escape := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			out = append(out, c)
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\n' || raw[j] == '\r' || raw[j] == '\t') {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}
