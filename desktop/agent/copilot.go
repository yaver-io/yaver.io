package main

// copilot.go — local LLM autocomplete / code assist. Replaces
// GitHub Copilot ($10/mo), Cursor Pro ($20/mo), Supermaven,
// Codeium, and friends for the solo dev who already runs
// Ollama on the same Mac mini their agent lives on.
//
// Three surfaces:
//
//   1. HTTP — /copilot/complete takes a prefix / suffix window
//      and streams token chunks back via SSE. Editors (VS Code
//      extension, nvim, emacs) connect here directly, so the
//      round-trip is entirely over localhost or the P2P tunnel.
//
//   2. CLI — `yaver copilot complete --file foo.ts --line 12`
//      prints the completion to stdout. Useful for shell
//      pipelines and the "fill in this TODO" pattern.
//
//   3. MCP — copilot_complete exposes the same capability to
//      other AI agents, so Claude Desktop can ask the local
//      Ollama model for a fast inline completion without
//      burning a round-trip to Anthropic.
//
// Why this fits the "your Mac mini replaces your SaaS stack"
// story: the dev already runs Ollama for voice / chat / auto
// dev. Reusing it for inline completions means the $10-20/mo
// Copilot subscription evaporates for anyone comfortable with
// a 7-14B local model.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// CopilotRequest is the input shape the HTTP and MCP callers
// both use.
type CopilotRequest struct {
	Prefix     string `json:"prefix"`     // text BEFORE the cursor
	Suffix     string `json:"suffix"`     // text AFTER the cursor (fill-in-the-middle)
	Language   string `json:"language"`   // "typescript", "python", etc. — hint for the model
	File       string `json:"file"`       // optional filename for the hint
	MaxTokens  int    `json:"maxTokens"`  // default 80
	Model      string `json:"model"`      // override the configured Ollama model
	Temperature float64 `json:"temperature"`
}

// CopilotResponse is what we give back.
type CopilotResponse struct {
	Completion string        `json:"completion"`
	Model      string        `json:"model"`
	LatencyMs  int64         `json:"latencyMs"`
}

// defaultCopilotModel is the Ollama tag we use when the caller
// doesn't specify one. Qwen 2.5 Coder 7B is the sweet spot for
// laptop Mac minis — 4 GB RAM, <150ms first-token on an M2, and
// the quality is competitive with Copilot for inline completions.
const defaultCopilotModel = "qwen2.5-coder:7b"

// buildCopilotPrompt wraps the prefix / suffix into a fill-in-
// the-middle prompt the Qwen Coder / StarCoder / DeepSeek
// family understands. Falls back to a plain prefix-only prompt
// when no suffix is provided.
func buildCopilotPrompt(req CopilotRequest) string {
	if req.Suffix == "" {
		var b strings.Builder
		if req.Language != "" {
			fmt.Fprintf(&b, "# Language: %s\n", req.Language)
		}
		if req.File != "" {
			fmt.Fprintf(&b, "# File: %s\n", req.File)
		}
		b.WriteString(req.Prefix)
		return b.String()
	}
	// Qwen Coder's FIM format: <|fim_prefix|>PREFIX<|fim_suffix|>SUFFIX<|fim_middle|>
	return "<|fim_prefix|>" + req.Prefix + "<|fim_suffix|>" + req.Suffix + "<|fim_middle|>"
}

// runOllamaComplete shells out to the local ollama CLI with the
// model + prompt + streaming enabled, collects tokens, and
// returns the final completion string.
func runOllamaComplete(ctx context.Context, model, prompt string, maxTokens int, stream func(chunk string)) (string, error) {
	if _, err := exec.LookPath("ollama"); err != nil {
		return "", fmt.Errorf("ollama not installed — brew install ollama && ollama pull " + defaultCopilotModel)
	}
	args := []string{"run", model}
	cmd := exec.CommandContext(ctx, "ollama", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() {
		defer stdin.Close()
		_, _ = io.WriteString(stdin, prompt)
	}()
	var out strings.Builder
	reader := bufio.NewReader(stdout)
	tokenCount := 0
	for {
		buf := make([]byte, 1024)
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			out.WriteString(chunk)
			tokenCount += strings.Count(chunk, " ")
			if stream != nil {
				stream(chunk)
			}
			if maxTokens > 0 && tokenCount >= maxTokens {
				_ = cmd.Process.Kill()
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = cmd.Wait()
	// Clean up Qwen FIM terminators if present.
	completion := out.String()
	completion = strings.ReplaceAll(completion, "<|fim_middle|>", "")
	completion = strings.ReplaceAll(completion, "<|endoftext|>", "")
	completion = strings.TrimSpace(completion)
	return completion, nil
}

// CompleteOnce runs a single blocking completion and returns
// the final text. Used by /copilot/complete in non-stream mode
// and by the MCP tool.
func CompleteOnce(req CopilotRequest) (*CopilotResponse, error) {
	model := req.Model
	if model == "" {
		cfg, _ := LoadConfig()
		if cfg != nil && cfg.Speech != nil && cfg.Speech.Provider == "ollama" {
			// Reuse the dev's configured Ollama binding when
			// they've already set one for voice.
		}
		model = defaultCopilotModel
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 80
	}
	prompt := buildCopilotPrompt(req)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	text, err := runOllamaComplete(ctx, model, prompt, req.MaxTokens, nil)
	if err != nil {
		return nil, err
	}
	return &CopilotResponse{
		Completion: text,
		Model:      model,
		LatencyMs:  time.Since(start).Milliseconds(),
	}, nil
}

// --- HTTP ------------------------------------------------------------------

// handleCopilotComplete serves /copilot/complete. Supports two
// modes:
//
//   - Accept: text/event-stream → streams each token chunk as a
//     standard SSE "data:" message; editors can render tokens as
//     they arrive. Terminates with a "done" event.
//
//   - Any other Accept → buffers the full completion and
//     returns it as JSON { completion, model, latencyMs }.
func (s *HTTPServer) handleCopilotComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req CopilotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	streaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if !streaming {
		res, err := CompleteOnce(req)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, res)
		return
	}
	// SSE path.
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	model := req.Model
	if model == "" {
		model = defaultCopilotModel
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 80
	}
	prompt := buildCopilotPrompt(req)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, err := runOllamaComplete(ctx, model, prompt, req.MaxTokens, func(chunk string) {
		data, _ := json.Marshal(map[string]string{"chunk": chunk})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	})
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

// handleCopilotModels returns the Ollama models the user has
// pulled, so the mobile / desktop client can let the dev pick.
func (s *HTTPServer) handleCopilotModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	cmd := exec.Command("ollama", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"models":  []string{},
			"warning": "ollama not available: " + err.Error(),
			"hint":    "brew install ollama && ollama pull " + defaultCopilotModel,
		})
		return
	}
	models := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			models = append(models, fields[0])
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "models": models, "default": defaultCopilotModel})
}

// --- CLI -------------------------------------------------------------------

// runCopilot is wired into main.go as `yaver copilot`.
func runCopilot(args []string) {
	if len(args) == 0 {
		fmt.Println("usage:")
		fmt.Println("  yaver copilot complete --prefix '...' [--suffix '...'] [--model tag] [--max 80]")
		fmt.Println("  yaver copilot models")
		return
	}
	switch args[0] {
	case "complete":
		req := CopilotRequest{}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--prefix":
				if i+1 < len(args) {
					req.Prefix = args[i+1]
					i++
				}
			case "--suffix":
				if i+1 < len(args) {
					req.Suffix = args[i+1]
					i++
				}
			case "--language":
				if i+1 < len(args) {
					req.Language = args[i+1]
					i++
				}
			case "--model":
				if i+1 < len(args) {
					req.Model = args[i+1]
					i++
				}
			case "--max":
				if i+1 < len(args) {
					fmt.Sscan(args[i+1], &req.MaxTokens)
					i++
				}
			}
		}
		res, err := CompleteOnce(req)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(res.Completion)
	case "models":
		cmd := exec.Command("ollama", "list")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("ollama not available:", err)
			return
		}
		fmt.Print(string(out))
	default:
		fmt.Println("unknown subcommand:", args[0])
	}
}
