package main

// hybrid_preflight.go — dependency check for `yaver hybrid`.
//
// Surfaces the three failure modes users hit first:
//   1. aider not on PATH
//   2. ollama daemon not running
//   3. chosen model not pulled
//
// Called from the CLI (`yaver hybrid --check`) and from the HTTP
// handler before a run starts, so the caller gets a fast, actionable
// error instead of a cryptic aider stack trace three minutes in.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// HybridPreflight reports which dependencies are ready for a hybrid
// run using (implementer, model, baseURL).
type HybridPreflight struct {
	AiderOK       bool   `json:"aiderOk"`
	AiderVersion  string `json:"aiderVersion,omitempty"`
	OllamaOK      bool   `json:"ollamaOk"`
	OllamaURL     string `json:"ollamaUrl"`
	ModelOK       bool   `json:"modelOk"`
	ModelName     string `json:"modelName,omitempty"`
	Hint          string `json:"hint,omitempty"`
}

// checkHybrid runs the three probes. Any failing probe sets a single
// "next command to run" hint so the user can copy/paste a fix rather
// than decode three separate error strings.
func checkHybrid(implementer, model, baseURL string) HybridPreflight {
	pf := HybridPreflight{OllamaURL: baseURL}
	if pf.OllamaURL == "" {
		pf.OllamaURL = "http://127.0.0.1:11434"
	}

	// Aider probe — implementer is effectively aider for both the
	// "aider" and "aider-ollama" runner IDs today. A future
	// non-aider implementer would plug in here.
	if _, err := exec.LookPath("aider"); err == nil {
		pf.AiderOK = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, "aider", "--version").Output()
		pf.AiderVersion = strings.TrimSpace(strings.ReplaceAll(string(out), "aider ", ""))
	}

	// Ollama daemon probe via /api/tags — succeeds only if the
	// daemon is actually serving HTTP, which is what aider needs.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, pf.OllamaURL+"/api/tags", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			pf.OllamaOK = true
			// Is the model available? `ollama_chat/qwen2.5-coder:14b`
			// is the litellm identifier; ollama itself just calls
			// it `qwen2.5-coder:14b`, so strip any prefix.
			wanted := strings.TrimPrefix(model, "ollama_chat/")
			wanted = strings.TrimPrefix(wanted, "ollama/")
			pf.ModelName = wanted
			var payload struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
				for _, m := range payload.Models {
					if m.Name == wanted {
						pf.ModelOK = true
						break
					}
				}
			}
		}
	}

	switch {
	case !pf.AiderOK:
		pf.Hint = "Install aider: yaver install aider"
	case !pf.OllamaOK:
		pf.Hint = "Start Ollama: `ollama serve` (or `brew services start ollama`)"
	case !pf.ModelOK && pf.ModelName != "":
		pf.Hint = fmt.Sprintf("Pull the model: ollama pull %s", pf.ModelName)
	}
	return pf
}

func (pf HybridPreflight) AllOK() bool {
	return pf.AiderOK && pf.OllamaOK && pf.ModelOK
}
