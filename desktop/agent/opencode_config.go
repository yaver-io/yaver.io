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
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// HasAPIKey is true when the provider entry in opencode.json has a
	// non-empty `options.apiKey`. Used by the web/mobile composer to
	// render "✓ Key configured · Change" instead of forcing the user
	// to re-paste the key every time they pick that provider chip.
	// We never expose the key value itself — just the boolean — so
	// the same data round-trips through Convex sync without leaking
	// the secret. P2P-friendly: the summary is read straight off the
	// agent's opencode.json via /runner/opencode/config.
	HasAPIKey bool                   `json:"hasApiKey,omitempty"`
	BaseURL   string                 `json:"baseUrl,omitempty"`
	Models    []OpenCodeModelSummary `json:"models,omitempty"`
}

// OpenCodeAgentSummary is one entry under `agent.` (or legacy `mode.`)
// in opencode.json. The two stock entries are "build" and "plan", but
// users can define arbitrary additional agents — chat / review /
// research / etc. — each with its own model + provider override. Both
// the web composer's agent dropdown and the mobile chat picker should
// list every entry here so a custom agent isn't a hidden CLI-only
// power-user feature.
type OpenCodeAgentSummary struct {
	Name        string `json:"name"`            // "build", "plan", "review", …
	Model       string `json:"model,omitempty"` // e.g. "anthropic/claude-sonnet-4-6"
	Description string `json:"description,omitempty"`
	IsBuiltin   bool   `json:"isBuiltin,omitempty"` // true for build + plan
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
	// Agents is the full list of agent entries (built-ins + customs).
	// Surfaced so the web + mobile chat pickers can offer the custom
	// ones, not just hardcoded build/plan.
	Agents []OpenCodeAgentSummary `json:"agents,omitempty"`
	// Diagnostics flag config inconsistencies the user should fix
	// before running a kick — e.g. a provider with no API key, an API
	// key with no matching provider, an unknown model id. Each entry
	// is one human-readable line with a fixit hint.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

type openCodeConfigPatch struct {
	DefaultAgent *string `json:"defaultAgent,omitempty"`
	Model        *string `json:"model,omitempty"`
	SmallModel   *string `json:"smallModel,omitempty"`
	BuildModel   *string `json:"buildModel,omitempty"`
	PlanModel    *string `json:"planModel,omitempty"`
	// Providers is an optional list of provider upserts. Each entry
	// either creates a new provider in opencode.json or merges into an
	// existing one. Common use case: pointing a remote Yaver machine
	// at its own Tailscale-reachable Ollama instance — pass
	// {id: "ollama", baseUrl: "http://100.x.x.x:11434", models: {...}}
	// from the dashboard and the agent rewrites the provider section
	// without touching anything else in the file (custom agents,
	// MCP-server entries, etc.). Pass `delete: true` to remove a
	// provider entirely. Any other top-level keys the user has in
	// their opencode.json are preserved as-is.
	Providers []openCodeProviderPatch `json:"providers,omitempty"`
}

// openCodeProviderPatch is a single provider mutation. ID is required.
// BaseURL / Models / APIKey / Name are optional — empty means "leave
// existing value alone" except when Delete=true, which wipes the
// provider entry.
type openCodeProviderPatch struct {
	ID      string         `json:"id"`
	Name    string         `json:"name,omitempty"`
	BaseURL string         `json:"baseUrl,omitempty"`
	APIKey  string         `json:"apiKey,omitempty"`
	Models  map[string]any `json:"models,omitempty"`
	Delete  bool           `json:"delete,omitempty"`
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
	authPatches := openCodeAuthProviderPatches(patch.Providers)
	for _, p := range patch.Providers {
		applyOpenCodeProviderPatch(cfg, p)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return OpenCodeConfigSummary{}, err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return OpenCodeConfigSummary{}, err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return OpenCodeConfigSummary{}, err
	}
	if len(authPatches) > 0 {
		if err := applyOpenCodeAuthPatches(authPatches); err != nil {
			return OpenCodeConfigSummary{}, err
		}
	}
	return summarizeOpenCodeConfig(path, cfg, true), nil
}

func openCodeAuthProviderPatches(providers []openCodeProviderPatch) map[string]string {
	out := map[string]string{}
	for _, p := range providers {
		id := strings.TrimSpace(p.ID)
		key := strings.TrimSpace(p.APIKey)
		if id == "" || key == "" || p.Delete {
			continue
		}
		out[id] = key
	}
	return out
}

func applyOpenCodeAuthPatches(keysByProvider map[string]string) error {
	path := preferredOpenCodeAuthPath()
	auth := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &auth); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	for id, key := range keysByProvider {
		auth[id] = map[string]any{
			"type": "api",
			"key":  key,
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o600)
}

// applyOpenCodeProviderPatch upserts a single provider entry on the
// in-memory config map. Empty ID is a no-op. Delete=true removes the
// provider; otherwise non-empty fields overwrite the corresponding
// keys (name, baseURL under options, apiKey under options, models).
// Existing keys we don't touch (e.g. user-defined custom options or
// per-model metadata not passed in this patch) are preserved.
func applyOpenCodeProviderPatch(cfg map[string]any, p openCodeProviderPatch) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return
	}
	if isOpenCodeBuiltinAuthProvider(id) && strings.TrimSpace(p.BaseURL) == "" && len(p.Models) == 0 {
		// Built-in providers such as zai-coding-plan should keep using
		// OpenCode's bundled endpoint/model catalogue. The API key is
		// synchronized to auth.json by applyOpenCodeAuthPatches; writing
		// a provider block here would override the built-in provider.
		return
	}
	providersNode, _ := cfg["provider"].(map[string]any)
	if providersNode == nil {
		if p.Delete {
			return
		}
		providersNode = map[string]any{}
		cfg["provider"] = providersNode
	}
	if p.Delete {
		delete(providersNode, id)
		if len(providersNode) == 0 {
			delete(cfg, "provider")
		}
		return
	}
	entry, _ := providersNode[id].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		providersNode[id] = entry
	}
	if name := strings.TrimSpace(p.Name); name != "" {
		entry["name"] = name
	}
	options, _ := entry["options"].(map[string]any)
	if (strings.TrimSpace(p.BaseURL) != "" || strings.TrimSpace(p.APIKey) != "") && options == nil {
		options = map[string]any{}
		entry["options"] = options
	}
	if base := strings.TrimSpace(p.BaseURL); base != "" {
		options["baseURL"] = base
		// Some opencode releases (and downstream tools that read the
		// same file) use `baseUrl` instead of `baseURL`. Mirror so
		// either spelling works without us picking a winner. Drop the
		// alternate when the canonical form is set, to avoid drift.
		delete(options, "baseUrl")
	}
	if key := strings.TrimSpace(p.APIKey); key != "" {
		options["apiKey"] = key
	}
	if len(p.Models) > 0 {
		modelsNode, _ := entry["models"].(map[string]any)
		if modelsNode == nil {
			modelsNode = map[string]any{}
			entry["models"] = modelsNode
		}
		for k, v := range p.Models {
			modelsNode[strings.TrimSpace(k)] = v
		}
	}
}

func isOpenCodeBuiltinAuthProvider(id string) bool {
	switch strings.TrimSpace(id) {
	case "zai-coding-plan":
		return true
	default:
		return false
	}
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
	summary.Agents = openCodeAgentsFromConfig(cfg)
	summary.Diagnostics = openCodeDiagnostics(cfg, summary)
	return summary
}

// openCodeAgentsFromConfig walks both the new `agent.<name>` keys and
// the legacy `mode.<name>` keys, dedups by name, and returns a sorted
// list with build + plan always first (in that order) so UIs can
// render the stock entries above customs without a separate sort.
func openCodeAgentsFromConfig(cfg map[string]any) []OpenCodeAgentSummary {
	out := map[string]OpenCodeAgentSummary{}
	collect := func(node map[string]any) {
		for name, raw := range node {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			model, _ := stringFromMap(entry, "model")
			desc, _ := stringFromMap(entry, "description")
			out[name] = OpenCodeAgentSummary{
				Name:        name,
				Model:       model,
				Description: desc,
				IsBuiltin:   name == "build" || name == "plan",
			}
		}
	}
	if node, ok := cfg["agent"].(map[string]any); ok {
		collect(node)
	}
	// Legacy `mode.<name>` shape — Yaver keeps reading it for users
	// who haven't migrated. Builtins win on collision.
	if node, ok := cfg["mode"].(map[string]any); ok {
		for name, raw := range node {
			if _, exists := out[name]; exists {
				continue
			}
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			model, _ := stringFromMap(entry, "model")
			desc, _ := stringFromMap(entry, "description")
			out[name] = OpenCodeAgentSummary{
				Name:        name,
				Model:       model,
				Description: desc,
				IsBuiltin:   name == "build" || name == "plan",
			}
		}
	}
	// Always-on stock entries even when opencode.json is empty — so
	// the UI never shows a literally empty dropdown.
	if _, ok := out["build"]; !ok {
		out["build"] = OpenCodeAgentSummary{Name: "build", IsBuiltin: true}
	}
	if _, ok := out["plan"]; !ok {
		out["plan"] = OpenCodeAgentSummary{Name: "plan", IsBuiltin: true}
	}

	list := make([]OpenCodeAgentSummary, 0, len(out))
	// build first, plan second, custom agents alphabetically.
	if a, ok := out["build"]; ok {
		list = append(list, a)
		delete(out, "build")
	}
	if a, ok := out["plan"]; ok {
		list = append(list, a)
		delete(out, "plan")
	}
	customNames := make([]string, 0, len(out))
	for name := range out {
		customNames = append(customNames, name)
	}
	sort.Strings(customNames)
	for _, name := range customNames {
		list = append(list, out[name])
	}
	return list
}

// openCodeDiagnostics surfaces actionable misconfigurations. Catches
// the common GLM "key without baseURL" trap and the inverse — keys
// in env / vault that the user clearly intended to use, but no
// matching provider entry exists.
//
// Best-effort: false positives are worse than false negatives here
// because users dismiss noisy banners. We only flag clear-cut errors:
//   - provider with no `api_key` and no env var lookup that resolves
//   - provider with no `baseUrl` AND `id` is not one of the known
//     defaults (anthropic / openai / openai-compat with no base URL
//     would never work)
//   - non-empty buildModel / planModel that points at a provider
//     id not present in providers[]
func openCodeDiagnostics(cfg map[string]any, sum OpenCodeConfigSummary) []string {
	var out []string
	provIDs := map[string]bool{}
	for _, p := range sum.Providers {
		provIDs[p.ID] = true
	}

	// Stock providers that ship their own base URL via opencode's
	// built-in config — users shouldn't have to set baseUrl for these.
	stockProviders := map[string]bool{
		"anthropic":       true,
		"openai":          true,
		"google":          true,
		"groq":            true,
		"zai-coding-plan": true,
	}

	for _, p := range sum.Providers {
		if !stockProviders[p.ID] && p.BaseURL == "" {
			out = append(out,
				fmt.Sprintf(
					"Provider %q has no baseUrl. For non-default providers (Z.ai/GLM, OpenRouter, custom Ollama, etc.) opencode needs a base URL — set it in the provider card.",
					p.ID,
				),
			)
		}
	}

	check := func(label, model string) {
		if model == "" {
			return
		}
		// Models are written as "<providerId>/<model>".
		parts := strings.SplitN(model, "/", 2)
		if len(parts) < 2 {
			return
		}
		providerID := parts[0]
		if !provIDs[providerID] && !stockProviders[providerID] {
			out = append(out,
				fmt.Sprintf(
					"%s model %q references provider %q which is not in providers[]. Add it (with apiKey + baseUrl) or change the model.",
					label, model, providerID,
				),
			)
		}
	}
	check("default", sum.Model)
	check("build", sum.BuildModel)
	check("plan", sum.PlanModel)
	check("small", sum.SmallModel)
	for _, a := range sum.Agents {
		if a.IsBuiltin {
			continue
		}
		check(fmt.Sprintf("agent %q", a.Name), a.Model)
	}

	return out
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
	authKeys := openCodeAuthProviderKeySet()
	providersNode, ok := cfg["provider"].(map[string]any)
	if !ok || providersNode == nil {
		if authKeys["zai-coding-plan"] {
			return []OpenCodeProviderSummary{{
				ID:        "zai-coding-plan",
				Name:      "Zai Coding Plan",
				HasAPIKey: true,
				Models: []OpenCodeModelSummary{{
					ID:       "zai-coding-plan/glm-4.7",
					Name:     "GLM 4.7 Coding Plan",
					Provider: "zai-coding-plan",
					Source:   "builtin",
				}},
			}}
		}
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
			if key, _ := stringFromMap(options, "apiKey"); strings.TrimSpace(key) != "" {
				row.HasAPIKey = true
			}
		}
		if authKeys[id] {
			row.HasAPIKey = true
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

func openCodeAuthProviderKeySet() map[string]bool {
	path := preferredOpenCodeAuthPath()
	raw, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	var auth map[string]any
	if err := json.Unmarshal(raw, &auth); err != nil {
		return nil
	}
	out := map[string]bool{}
	for id, rawEntry := range auth {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		if key, _ := stringFromMap(entry, "key"); strings.TrimSpace(key) != "" {
			out[id] = true
			continue
		}
		if key, _ := stringFromMap(entry, "apiKey"); strings.TrimSpace(key) != "" {
			out[id] = true
		}
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

func preferredOpenCodeAuthPath() string {
	if file := strings.TrimSpace(os.Getenv("OPENCODE_AUTH")); file != "" {
		return file
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "opencode", "auth.json")
	}
	return filepath.Join(".", "auth.json")
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
