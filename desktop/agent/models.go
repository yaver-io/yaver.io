package main

// models.go — ModelManager: wraps the Ollama HTTP API for local LLM management.
//
// Provides list, pull, remove, run, serve, ps, status, and recommend operations
// against a locally running Ollama instance (http://localhost:11434).
// Follows the same manager pattern as BrowserManager and DevServerManager.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// detectGPUNameForOllama returns a short GPU name string used to
// pick a sensible Ollama model recommendation. Best-effort: returns
// "" when no obvious GPU detection succeeds.
func detectGPUNameForOllama() string {
	// NVIDIA — fastest path
	if out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); err == nil {
		if name := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); name != "" {
			return name
		}
	}
	// Apple Silicon — system_profiler is slow, but cheap to fall back on
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			if name := strings.TrimSpace(string(out)); strings.Contains(name, "Apple") {
				return name + " (MPS)"
			}
		}
	}
	return ""
}

const (
	ollamaBaseURL = "http://localhost:11434"
	ollamaPort    = 11434
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// ModelInfo describes a locally available Ollama model.
type ModelInfo struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Digest     string    `json:"digest"`
}

// RunningModel describes a model currently loaded in memory by Ollama.
type RunningModel struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SizeVRAM  int64  `json:"size_vram"`
	ExpiresAt string `json:"expires_at"`
}

// OllamaStatus summarises the current state of the Ollama daemon.
type OllamaStatus struct {
	Running bool   `json:"running"`
	Port    int    `json:"port"`
	Version string `json:"version"`
	Models  int    `json:"models"`
	GPU     string `json:"gpu"`
}

// ModelRecommendation is a model the system suggests based on available RAM/VRAM.
type ModelRecommendation struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Size        string `json:"size"`
	Fits        bool   `json:"fits"`
	Reason      string `json:"reason"`
}

// ---------------------------------------------------------------------------
// ModelManager
// ---------------------------------------------------------------------------

// ModelManager wraps the Ollama HTTP API and manages the local ollama process.
type ModelManager struct {
	mu     sync.Mutex
	client *http.Client
}

// NewModelManager creates a ModelManager with a reasonable HTTP timeout.
func NewModelManager() *ModelManager {
	return &ModelManager{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// ---------------------------------------------------------------------------
// List — GET /api/tags
// ---------------------------------------------------------------------------

type ollamaTagsResponse struct {
	Models []struct {
		Name       string    `json:"name"`
		ModifiedAt time.Time `json:"modified_at"`
		Size       int64     `json:"size"`
		Digest     string    `json:"digest"`
	} `json:"models"`
}

// List returns all models available locally in Ollama.
func (m *ModelManager) List() ([]ModelInfo, error) {
	resp, err := m.client.Get(ollamaBaseURL + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("ollama list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama list: unexpected status %d", resp.StatusCode)
	}

	var result ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama list: decode: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Models))
	for _, raw := range result.Models {
		models = append(models, ModelInfo{
			Name:       raw.Name,
			Size:       raw.Size,
			ModifiedAt: raw.ModifiedAt,
			Digest:     raw.Digest,
		})
	}
	return models, nil
}

// ---------------------------------------------------------------------------
// Pull — POST /api/pull (streaming)
// ---------------------------------------------------------------------------

// Pull downloads a model from the Ollama registry.
// Progress lines (JSON status strings) are sent on the progress channel if non-nil.
// The channel is closed when the pull completes or fails.
func (m *ModelManager) Pull(name string, progress chan<- string) error {
	if progress != nil {
		defer close(progress)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"name":   name,
		"stream": true,
	})

	resp, err := m.client.Post(ollamaBaseURL+"/api/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama pull %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama pull %s: unexpected status %d", name, resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err == nil {
			status, _ := event["status"].(string)
			if progress != nil && status != "" {
				progress <- status
			}
			if errMsg, ok := event["error"].(string); ok && errMsg != "" {
				return fmt.Errorf("ollama pull %s: %s", name, errMsg)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ollama pull %s: reading response: %w", name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Remove — DELETE /api/delete
// ---------------------------------------------------------------------------

// Remove deletes a model from the local Ollama store.
func (m *ModelManager) Remove(name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequest(http.MethodDelete, ollamaBaseURL+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama remove %s: build request: %w", name, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama remove %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama remove %s: status %d: %s", name, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Run — POST /api/generate
// ---------------------------------------------------------------------------

type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream bool   `json:"stream"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

// Run sends a prompt to a model and returns the complete response text.
// It uses the non-streaming mode for simplicity.
func (m *ModelManager) Run(model, prompt, system string) (string, error) {
	reqBody := ollamaGenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: system,
		Stream: false,
	}
	body, _ := json.Marshal(reqBody)

	// Non-streaming generate can take a while for large models.
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Post(ollamaBaseURL+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama run %s: %w", model, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama run %s: status %d: %s", model, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var result ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("ollama run %s: decode: %w", model, err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("ollama run %s: %s", model, result.Error)
	}
	return result.Response, nil
}

// ---------------------------------------------------------------------------
// Serve — start ollama serve if not already running
// ---------------------------------------------------------------------------

// Serve starts the ollama daemon if it is not already running.
// If ollama is already listening on port 11434, this is a no-op.
func (m *ModelManager) Serve() error {
	// Check if already running with a lightweight ping.
	if m.ping() {
		return nil
	}

	// Locate the ollama binary.
	ollamaPath, err := exec.LookPath("ollama")
	if err != nil {
		return fmt.Errorf("ollama not found in PATH — install from https://ollama.com")
	}

	// Start in background; ollama serve forks itself, so we don't need to
	// track the child process explicitly.
	cmd := exec.Command(ollamaPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ollama serve: %w", err)
	}

	// Wait up to 10 seconds for ollama to become ready.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if m.ping() {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("ollama serve started but did not become ready within 10s")
}

// ping returns true if the ollama HTTP API responds.
func (m *ModelManager) ping() bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(ollamaBaseURL + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ---------------------------------------------------------------------------
// PS — GET /api/ps
// ---------------------------------------------------------------------------

type ollamaPSResponse struct {
	Models []struct {
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		SizeVRAM  int64  `json:"size_vram"`
		ExpiresAt string `json:"expires_at"`
	} `json:"models"`
}

// PS returns the list of models currently loaded in memory by Ollama.
func (m *ModelManager) PS() ([]RunningModel, error) {
	resp, err := m.client.Get(ollamaBaseURL + "/api/ps")
	if err != nil {
		return nil, fmt.Errorf("ollama ps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama ps: unexpected status %d", resp.StatusCode)
	}

	var result ollamaPSResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama ps: decode: %w", err)
	}

	running := make([]RunningModel, 0, len(result.Models))
	for _, r := range result.Models {
		running = append(running, RunningModel{
			Name:      r.Name,
			Size:      r.Size,
			SizeVRAM:  r.SizeVRAM,
			ExpiresAt: r.ExpiresAt,
		})
	}
	return running, nil
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status checks whether Ollama is running and returns high-level metadata.
func (m *ModelManager) Status() (*OllamaStatus, error) {
	status := &OllamaStatus{Port: ollamaPort}

	if !m.ping() {
		return status, nil
	}
	status.Running = true

	// Version — GET /api/version
	if ver, err := m.version(); err == nil {
		status.Version = ver
	}

	// Model count
	if models, err := m.List(); err == nil {
		status.Models = len(models)
	}

	// GPU info — best-effort; empty string if not detected.
	status.GPU = detectGPUNameForOllama()

	return status, nil
}

type ollamaVersionResponse struct {
	Version string `json:"version"`
}

func (m *ModelManager) version() (string, error) {
	resp, err := m.client.Get(ollamaBaseURL + "/api/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v ollamaVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// ---------------------------------------------------------------------------
// Recommend
// ---------------------------------------------------------------------------

// totalSystemRAMBytes returns the total physical RAM in bytes.
// Uses sysctl on macOS/BSD and /proc/meminfo on Linux.
// Falls back to runtime.MemStats (heap only) when the OS-specific path fails.
func totalSystemRAMBytes() uint64 {
	switch runtime.GOOS {
	case "darwin", "freebsd":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			if n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil {
				return n
			}
		}
	case "linux":
		f, err := os.Open("/proc/meminfo")
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "MemTotal:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
							return kb * 1024
						}
					}
				}
			}
		}
	}

	// Fallback: use runtime.MemStats (reflects Go heap, not total RAM).
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}

// Recommend returns a list of models suited to the current machine's RAM.
func (m *ModelManager) Recommend() ([]ModelRecommendation, error) {
	ramBytes := totalSystemRAMBytes()
	ramGB := float64(ramBytes) / (1 << 30)

	gpuName := detectGPUNameForOllama()

	type candidate struct {
		name        string
		description string
		displaySize string
		minRAMGB    float64 // minimum RAM required (GB)
	}

	// Ordered from smallest to largest.
	candidates := []candidate{
		// Embedding models (tiny, always useful)
		{"nomic-embed-text", "Text embedding model for RAG / semantic search", "274 MB", 2},
		{"all-minilm", "Lightweight sentence embeddings", "46 MB", 2},

		// Small generalist (1-3B)
		{"qwen2.5:1.5b", "Qwen 2.5 1.5B — fast, low memory", "986 MB", 2},
		{"phi3:mini", "Microsoft Phi-3 Mini 3.8B — compact and capable", "2.3 GB", 4},
		{"gemma2:2b", "Google Gemma 2 2B — efficient small model", "1.6 GB", 3},

		// Small coding (1-3B)
		{"deepseek-coder:1.3b", "DeepSeek Coder 1.3B — code completion", "776 MB", 2},
		{"codegemma:2b", "Google CodeGemma 2B — code generation", "1.6 GB", 3},

		// Medium generalist (7-8B)
		{"llama3.2:3b", "Meta Llama 3.2 3B — strong reasoning", "2.0 GB", 4},
		{"mistral:7b", "Mistral 7B — fast general-purpose model", "4.1 GB", 8},
		{"llama3.1:8b", "Meta Llama 3.1 8B — excellent all-rounder", "4.7 GB", 8},
		{"qwen2.5:7b", "Qwen 2.5 7B — multilingual and code-capable", "4.4 GB", 8},

		// Medium coding (7-8B)
		{"codellama:7b", "Code Llama 7B — code generation and completion", "3.8 GB", 8},
		{"deepseek-coder:6.7b", "DeepSeek Coder 6.7B — strong code model", "3.8 GB", 8},
		{"codegemma:7b", "Google CodeGemma 7B — instruction-tuned for code", "5.0 GB", 8},

		// Large (13-34B)
		{"codellama:13b", "Code Llama 13B — better code understanding", "7.4 GB", 16},
		{"llama3.1:70b", "Meta Llama 3.1 70B — frontier open model", "39 GB", 64},
		{"deepseek-coder:33b", "DeepSeek Coder 33B — top open coding model", "19 GB", 32},
		{"qwen2.5-coder:32b", "Qwen 2.5 Coder 32B — state-of-the-art coding", "19 GB", 32},
	}

	var recs []ModelRecommendation
	for _, c := range candidates {
		fits := ramGB >= c.minRAMGB

		var reason string
		if fits {
			if gpuName != "" {
				reason = fmt.Sprintf("fits in %.0f GB RAM; GPU acceleration available (%s)", ramGB, gpuName)
			} else {
				reason = fmt.Sprintf("fits in %.0f GB RAM; will run on CPU", ramGB)
			}
		} else {
			reason = fmt.Sprintf("requires %.0f GB RAM (system has %.0f GB)", c.minRAMGB, ramGB)
		}

		recs = append(recs, ModelRecommendation{
			Name:        c.name,
			Description: c.description,
			Size:        c.displaySize,
			Fits:        fits,
			Reason:      reason,
		})
	}
	return recs, nil
}
