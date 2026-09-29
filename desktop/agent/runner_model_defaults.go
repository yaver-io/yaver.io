package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// RunnerModelDefault is the Yaver-level fallback for one runner. User and
// device choices remain authoritative; this fills an empty selection and is
// the one-time recovery target after an operation rejects an explicit model.
type RunnerModelDefault struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

var (
	runnerModelDefaultsMu sync.RWMutex
	runnerModelDefaults   = builtinRunnerModelDefaults()
)

func builtinRunnerModelDefaults() map[string]RunnerModelDefault {
	return map[string]RunnerModelDefault{
		"claude":   {Model: "claude-opus-4-8"},
		"codex":    {Model: "gpt-5.6-sol", ReasoningEffort: "medium"},
		"opencode": {Model: "deepseek/deepseek-flash"},
	}
}

func LoadYaverModelDefaults(in map[string]RunnerModelDefault) {
	next := builtinRunnerModelDefaults()
	for rawID, value := range in {
		id := normalizeRunnerID(rawID)
		if _, supported := next[id]; !supported {
			continue
		}
		if model := strings.TrimSpace(value.Model); model != "" && runnerModelCompatible(id, model) {
			value.Model = model
			if id == "codex" {
				value.ReasoningEffort = normalizeCodexReasoningEffort(value.ReasoningEffort)
				if value.ReasoningEffort == "" {
					value.ReasoningEffort = "medium"
				}
			} else {
				value.ReasoningEffort = ""
			}
			next[id] = value
		}
	}
	runnerModelDefaultsMu.Lock()
	runnerModelDefaults = next
	runnerModelDefaultsMu.Unlock()

}

func yaverModelDefault(runnerID string) RunnerModelDefault {
	runnerModelDefaultsMu.RLock()
	defer runnerModelDefaultsMu.RUnlock()
	return runnerModelDefaults[normalizeRunnerID(runnerID)]
}

func yaverDefaultModelForRunner(runnerID string) string {
	return strings.TrimSpace(yaverModelDefault(runnerID).Model)
}

func yaverDefaultReasoningEffortForRunner(runnerID string) string {
	return normalizeCodexReasoningEffort(yaverModelDefault(runnerID).ReasoningEffort)
}

// The global default is a recovery target, never another member of a retry
// roulette. It is attempted at most once and never retries itself.
func modelFallbackForRefusal(runnerID, refusedModel string, alreadyAttempted bool) (RunnerModelDefault, bool) {
	if alreadyAttempted {
		return RunnerModelDefault{}, false
	}
	fallback := yaverModelDefault(runnerID)
	if fallback.Model == "" || strings.EqualFold(strings.TrimSpace(refusedModel), fallback.Model) {
		return RunnerModelDefault{}, false
	}
	return fallback, true
}

func normalizeBackendModelsWithYaverDefaults(models []BackendModel) []BackendModel {
	result := append([]BackendModel(nil), models...)
	seen := make(map[string]bool)
	for i := range result {
		id := normalizeRunnerID(result[i].RunnerID)
		fallback := yaverDefaultModelForRunner(id)
		if fallback == "" {
			continue
		}
		result[i].IsDefault = result[i].ModelID == fallback
		seen[id+"\x00"+result[i].ModelID] = true
	}
	for _, id := range []string{"claude", "codex", "opencode"} {
		fallback := yaverDefaultModelForRunner(id)
		if fallback == "" || seen[id+"\x00"+fallback] {
			continue
		}
		backendID := id
		if id == "claude" {
			backendID = "claude-code"
		}
		result = append(result, BackendModel{
			ModelID: fallback, RunnerID: backendID, Name: fallback,
			Description: "Yaver global default", IsDefault: true, SortOrder: 0,
		})
	}
	return result
}

func openCodeHasUserModelConfiguration() bool {
	config, err := loadOpenCodeConfigSummary()
	if err != nil {
		return false
	}
	if strings.TrimSpace(config.Model) != "" || strings.TrimSpace(config.BuildModel) != "" || strings.TrimSpace(config.PlanModel) != "" {
		return true
	}
	for _, agent := range config.Agents {
		if strings.TrimSpace(agent.Model) != "" {
			return true
		}
	}
	return false
}

func refreshYaverModelDefaultsLoop(ctx context.Context, convexURL string) {
	if strings.TrimSpace(convexURL) == "" {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			config, err := FetchPlatformConfig(convexURL)
			if err != nil {
				log.Printf("[models] Convex default refresh unavailable: %v", err)
				continue
			}
			LoadYaverModelDefaults(config.ModelDefaults)
			LoadModelsFromBackend(config.Models)
		}
	}
}
