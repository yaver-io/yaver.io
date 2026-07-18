package main

// sandbox_remote.go — Mobile Sandbox → remote runner (OpenCode + z.ai GLM).
//
// The phone-only Mobile Sandbox (see mobile/src/lib/phoneSandbox*.ts) edits a
// project whose source lives in the phone's local filesystem/SQLite — there is
// NO checkout of that project on this box. So the usual remote-coding paths
// (/tasks, /agent/graphs) don't apply: they operate on a workDir that already
// exists on the agent. This endpoint closes that gap.
//
// Flow: the mobile client ships the sandbox's source files + a natural-language
// prompt → we materialize them into a throwaway workdir → run OpenCode against
// z.ai's coding-plan GLM model → diff the workdir against the input → return an
// EditPlan-shaped result the phone applies to its local project (apply-with-
// preview, reversible — identical to the on-device / BYO-key backends).
//
// OpenCode-only by design for now: the runner is fixed to "opencode" and the
// request is rejected for any other runner id. The GLM credential lives only on
// this box (ZAI_API_KEY / GLM_API_KEY) — the phone never has to hold it.
//
// The GLM exec is isolated behind sandboxRunnerFn so the file-write / snapshot /
// diff logic (where the real correctness risk is) is fully unit-tested without a
// network or the runner binary. See sandbox_remote_test.go.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sandboxFile is one source file the phone ships up (or that we read back).
type sandboxFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// sandboxRunRequest is the POST /sandbox/run body.
type sandboxRunRequest struct {
	Prompt    string          `json:"prompt"`
	Files     []sandboxFile   `json:"files"`
	Framework string          `json:"framework,omitempty"`
	Schema    json.RawMessage `json:"schema,omitempty"` // phone-project backend schema, forwarded into the prompt
	Runner    string          `json:"runner,omitempty"` // only "opencode" (or empty → opencode) is accepted
	TimeoutMs int             `json:"timeoutMs,omitempty"`
}

// sandboxEdit mirrors the mobile FileEdit (llmClient.ts) so applyEditPlan can
// consume the response unchanged.
type sandboxEdit struct {
	Action  string `json:"action"` // create | update | delete
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// sandboxRunResponse is the EditPlan-shaped result.
type sandboxRunResponse struct {
	OK        bool          `json:"ok"`
	Rationale string        `json:"rationale,omitempty"`
	Edits     []sandboxEdit `json:"edits"`
	Runner    string        `json:"runner"`
	Model     string        `json:"model,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// sandboxRunMeta is what a runner reports back beyond the on-disk edits.
type sandboxRunMeta struct {
	rationale string
	model     string
}

// sandboxRunnerFn runs a coding agent over workDir to satisfy prompt, editing
// files in place. Implementations must NOT touch anything outside workDir.
type sandboxRunnerFn func(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error)

const (
	maxSandboxFiles       = 400
	maxSandboxTotalBytes  = 2_000_000 // 2 MB of source — generous for a phone sandbox
	maxSandboxFileBytes   = 512 * 1024
	defaultSandboxTimeout = 180 * time.Second
	maxSandboxTimeout     = 600 * time.Second
)

// sandboxSafeRelPath validates + normalizes a posix-relative path so a file from
// the phone can't escape the throwaway workdir. Mirrors the mobile source store
// (normaliseSourceRelPath in phoneSandboxSource.ts): no absolute paths, no "..",
// no backslashes, no empty.
func sandboxSafeRelPath(p string) (string, error) {
	s := strings.TrimSpace(p)
	if s == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.ContainsRune(s, '\\') {
		return "", fmt.Errorf("backslash not allowed in path %q", p)
	}
	if strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("absolute path not allowed: %q", p)
	}
	// Reject Windows-style volume prefixes (C:\ already caught by backslash, but
	// C:/ is not) before cleaning.
	if len(s) >= 2 && s[1] == ':' {
		return "", fmt.Errorf("absolute path not allowed: %q", p)
	}
	clean := path.Clean(s)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." || clean == "" {
		return "", fmt.Errorf("path escapes project root: %q", p)
	}
	if strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("absolute path not allowed: %q", p)
	}
	return clean, nil
}

// sandboxIgnoredPath reports whether a path discovered in the workdir should be
// excluded from the diff. The agent (or its tooling) may drop scratch/config
// dirs (.claude, .git), dependency trees, or lockfiles into the workdir; those
// are not edits to the phone's source tree.
func sandboxIgnoredPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	first := rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		first = rel[:i]
	}
	if strings.HasPrefix(first, ".") { // .git, .claude, .config, .cache …
		return true
	}
	switch first {
	case "node_modules", "vendor", "dist", "build", ".expo":
		return true
	}
	return false
}

// writeSandboxFiles materializes a sanitized (relpath → content) map into root,
// creating parent dirs as needed. Keys are assumed already validated by
// sandboxSafeRelPath.
func writeSandboxFiles(root string, files map[string]string) error {
	for rel, content := range files {
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// snapshotSandboxDir reads every non-ignored, in-budget text file under root
// into a relpath → content map for diffing.
func snapshotSandboxDir(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, rerr := filepath.Rel(root, p)
			if rerr == nil && rel != "." && sandboxIgnoredPath(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if sandboxIgnoredPath(rel) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Size() > maxSandboxFileBytes {
			return nil // oversized — almost certainly not a phone source edit
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// diffSandboxSnapshots compares before/after relpath→content maps and returns
// the create/update/delete edits, sorted by path for determinism.
func diffSandboxSnapshots(before, after map[string]string) []sandboxEdit {
	var edits []sandboxEdit
	for relPath, content := range after {
		prev, existed := before[relPath]
		if !existed {
			edits = append(edits, sandboxEdit{Action: "create", Path: relPath, Content: content})
			continue
		}
		if prev != content {
			edits = append(edits, sandboxEdit{Action: "update", Path: relPath, Content: content})
		}
	}
	for relPath := range before {
		if _, stillThere := after[relPath]; !stillThere {
			edits = append(edits, sandboxEdit{Action: "delete", Path: relPath})
		}
	}
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].Path == edits[j].Path {
			return edits[i].Action < edits[j].Action
		}
		return edits[i].Path < edits[j].Path
	})
	return edits
}

// buildSandboxRemotePrompt wraps the user's instruction with the context the
// agent needs to edit the materialized sandbox in place.
func buildSandboxRemotePrompt(req sandboxRunRequest) string {
	var b strings.Builder
	framework := strings.TrimSpace(req.Framework)
	if framework == "" {
		framework = "React Native (Expo)"
	}
	b.WriteString("You are editing a phone-authored ")
	b.WriteString(framework)
	b.WriteString(" project. Its source files are in the CURRENT WORKING DIRECTORY.\n")
	b.WriteString("Make the requested change by creating, editing, or deleting files in place using your file tools. ")
	b.WriteString("Do not run dev servers, install dependencies, or initialize git. Only change source files.\n\n")
	if len(req.Schema) > 0 && strings.TrimSpace(string(req.Schema)) != "null" {
		b.WriteString("Backend schema (CRUD endpoints already exist for these tables) — wire to them, don't invent tables:\n")
		b.Write(req.Schema)
		b.WriteString("\n\n")
	}
	b.WriteString("Request:\n")
	b.WriteString(strings.TrimSpace(req.Prompt))
	b.WriteString("\n")
	return b.String()
}

// processSandboxRun is the testable core: materialize files, run the agent, diff
// the result. runFn is injected so tests can substitute a fake editor.
func processSandboxRun(ctx context.Context, req sandboxRunRequest, runFn sandboxRunnerFn) sandboxRunResponse {
	resp := sandboxRunResponse{Runner: "opencode", Edits: []sandboxEdit{}}

	before := make(map[string]string, len(req.Files))
	for _, f := range req.Files {
		rel, err := sandboxSafeRelPath(f.Path)
		if err != nil {
			resp.Error = fmt.Sprintf("rejected input file %q: %v", f.Path, err)
			return resp
		}
		before[rel] = f.Content
	}

	tmp, err := os.MkdirTemp("", "yaver-sandbox-remote-*")
	if err != nil {
		resp.Error = fmt.Sprintf("could not create workdir: %v", err)
		return resp
	}
	defer os.RemoveAll(tmp)

	if err := writeSandboxFiles(tmp, before); err != nil {
		resp.Error = fmt.Sprintf("could not stage files: %v", err)
		return resp
	}

	meta, runErr := runFn(ctx, tmp, buildSandboxRemotePrompt(req))
	resp.Rationale = meta.rationale
	resp.Model = meta.model

	after, snapErr := snapshotSandboxDir(tmp)
	if snapErr != nil {
		resp.Error = fmt.Sprintf("could not read result: %v", snapErr)
		return resp
	}
	resp.Edits = diffSandboxSnapshots(before, after)

	if runErr != nil {
		resp.Error = runErr.Error()
		// Surface any partial edits the agent made before it failed.
		resp.OK = len(resp.Edits) > 0
		return resp
	}
	resp.OK = true
	return resp
}

// runGLMSandbox is the default sandboxRunnerFn: it runs OpenCode against
// z.ai's bundled coding-plan provider over workDir.
func runGLMSandbox(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error) {
	rc := GetRunnerConfig("opencode")
	meta := sandboxRunMeta{model: "zai-coding-plan/glm-4.7"}

	if value, _ := hostSecretValue("ZAI_API_KEY"); strings.TrimSpace(value) == "" {
		return meta, fmt.Errorf("GLM via OpenCode is not configured on this box — add ZAI_API_KEY (or GLM_API_KEY) so OpenCode can use zai-coding-plan/glm-4.7")
	}

	bin, err := exec.LookPath(rc.Command)
	if err != nil {
		if bin = findInExpandedPath(rc.Command); bin == "" {
			return meta, fmt.Errorf("%s binary not found — install OpenCode on this box", rc.Command)
		}
	}

	args := []string{
		"run",
		"--model", meta.model,
		"--dangerously-skip-permissions",
		prompt,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	env := append(os.Environ(), "PATH="+expandedPath())
	if isRootProcess() {
		env = append(env, "IS_SANDBOX=1")
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	meta.rationale = parseStreamJSONResult(stdout.Bytes())
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if len(detail) > 600 {
			detail = detail[:600] + "…"
		}
		if ctx.Err() == context.DeadlineExceeded {
			return meta, fmt.Errorf("opencode runner timed out")
		}
		return meta, fmt.Errorf("opencode runner failed: %v: %s", runErr, detail)
	}
	return meta, nil
}

// parseStreamJSONResult extracts a human-readable rationale from stream-json-ish
// output: prefer the final {"type":"result"} event's text, else the last
// assistant text. Best-effort — the diff is the source of truth for edits, so
// an empty rationale is fine.
func parseStreamJSONResult(out []byte) string {
	var resultText, lastAssistant string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Result  string `json:"result"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "result":
			if strings.TrimSpace(ev.Result) != "" {
				resultText = ev.Result
			}
		case "assistant":
			var parts []string
			for _, c := range ev.Message.Content {
				if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
					parts = append(parts, c.Text)
				}
			}
			if len(parts) > 0 {
				lastAssistant = strings.Join(parts, "\n")
			}
		}
	}
	if strings.TrimSpace(resultText) != "" {
		return strings.TrimSpace(resultText)
	}
	return strings.TrimSpace(lastAssistant)
}

// handleSandboxRun serves POST /sandbox/run. Auth is the standard same-user
// bearer (s.auth) — the runner executes on this box as the signed-in user.
func (s *HTTPServer) handleSandboxRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, sandboxRunResponse{Error: "method not allowed", Edits: []sandboxEdit{}})
		return
	}
	var req sandboxRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSandboxTotalBytes+1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, sandboxRunResponse{Error: "invalid request body: " + err.Error(), Edits: []sandboxEdit{}})
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, sandboxRunResponse{Error: "prompt is required", Edits: []sandboxEdit{}})
		return
	}
	if len(req.Files) == 0 {
		writeJSON(w, http.StatusBadRequest, sandboxRunResponse{Error: "files is required (the sandbox source tree)", Edits: []sandboxEdit{}})
		return
	}
	if len(req.Files) > maxSandboxFiles {
		writeJSON(w, http.StatusBadRequest, sandboxRunResponse{Error: fmt.Sprintf("too many files (%d > %d)", len(req.Files), maxSandboxFiles), Edits: []sandboxEdit{}})
		return
	}
	if req.Runner != "" && normalizeRunnerID(req.Runner) != "opencode" {
		writeJSON(w, http.StatusBadRequest, sandboxRunResponse{Error: "only the opencode runner is supported for sandbox remote runs", Edits: []sandboxEdit{}})
		return
	}
	total := 0
	for _, f := range req.Files {
		total += len(f.Content) + len(f.Path)
	}
	if total > maxSandboxTotalBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, sandboxRunResponse{Error: fmt.Sprintf("sandbox too large (%d > %d bytes) — trim files", total, maxSandboxTotalBytes), Edits: []sandboxEdit{}})
		return
	}

	timeout := defaultSandboxTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
		if timeout > maxSandboxTimeout {
			timeout = maxSandboxTimeout
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resp := processSandboxRun(ctx, req, runGLMSandbox)
	writeJSON(w, http.StatusOK, resp)
}
