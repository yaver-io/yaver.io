package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// handleInstallTool runs `yaver install <tool>` from the agent on
// behalf of a phone or web client. Owner-auth only. Output is
// streamed to a "install:<tool>" log stream the caller subscribes
// to via GET /streams/install:<tool>.
//
// POST /install/<tool>
//   202 Accepted with {ok, tool, stream}
//
// GET /install/list
//   200 OK with [{name, installed, description}, ...]
func (s *HTTPServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/install/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || rest == "list" {
		s.handleInstallList(w, r)
		return
	}
	// POST /install/sudo — phone submits the password to an in-flight
	// install that asked for one. Handled inline so it doesn't get
	// misrouted as a tool named "sudo".
	if rest == "sudo" {
		s.handleInstallSudo(w, r)
		return
	}

	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	tool := rest
	if s.streams == nil {
		jsonError(w, http.StatusServiceUnavailable, "log streams not enabled on this agent")
		return
	}
	streamName := "install:" + tool
	stream := s.streams.Get(streamName)

	// Fast path: built-in integration. Keeps the existing behaviour
	// for tools with curated, agent-signed install plans (runtimes,
	// sandbox images, etc.).
	if plan, ok := lookupIntegration(tool); ok {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			stream.Append("Starting install: " + tool)
			err := runInstallPlan(ctx, plan, func(line string) { stream.Append(line) })
			if err != nil {
				stream.AppendEvent(map[string]interface{}{
					"type":   "result",
					"tool":   tool,
					"status": "error",
					"error":  err.Error(),
				})
				return
			}
			stream.AppendEvent(map[string]interface{}{
				"type":   "result",
				"tool":   tool,
				"status": "ok",
			})
		}()
		jsonReply(w, http.StatusAccepted, map[string]interface{}{
			"ok":     true,
			"tool":   tool,
			"stream": streamName,
		})
		return
	}

	// Slow path: look up the tool in the public package registry and
	// pick the best install step for the package managers we have.
	// Runs inside a PTY so sudo prompts can be surfaced to the UI.
	convexSiteURL := ""
	if s != nil {
		convexSiteURL = s.convexURL
	}
	var entry *PackageRegistryEntry
	for _, candidate := range PackageRegistry(convexSiteURL) {
		if candidate.Name == tool {
			c := candidate
			entry = &c
			break
		}
	}
	if entry == nil {
		jsonError(w, http.StatusNotFound, "unknown tool: "+tool+" — see GET /install/list")
		return
	}
	step := ResolveInstallStep(*entry, AvailablePackageManagersSet())
	if step == nil {
		jsonError(w, http.StatusBadRequest, "no compatible install step for "+tool+" — host is missing every advertised package manager for this tool")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		stream.Append("Starting install: " + tool)
		err := runRegistryInstall(ctx, tool, step, stream)
		if err != nil {
			stream.AppendEvent(map[string]interface{}{
				"type":   "result",
				"tool":   tool,
				"status": "error",
				"error":  err.Error(),
			})
			return
		}
		stream.AppendEvent(map[string]interface{}{
			"type":   "result",
			"tool":   tool,
			"status": "ok",
		})
	}()

	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":     true,
		"tool":   tool,
		"stream": streamName,
		"source": "registry",
		"step":   step,
	})
}

// handleInstallSudo receives the password for an in-flight install
// that emitted a sudo_prompt event. Body: {"tool":"<name>",
// "password":"<secret>"} or {"tool":"<name>","cancel":true} to send
// a ^C instead. The password is forwarded to the PTY stdin once and
// never persisted.
func (s *HTTPServer) handleInstallSudo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Tool     string `json:"tool"`
		Password string `json:"password"`
		Cancel   bool   `json:"cancel,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	tool := strings.TrimSpace(body.Tool)
	if tool == "" {
		jsonError(w, http.StatusBadRequest, "missing tool")
		return
	}
	if body.Cancel {
		if err := cancelInstallSudo(tool); err != nil {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		jsonReply(w, http.StatusAccepted, map[string]any{"ok": true, "cancelled": true})
		return
	}
	if err := respondToInstallSudo(tool, body.Password); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	// Zero the password so it doesn't sit in the request struct any
	// longer than needed.
	body.Password = ""
	jsonReply(w, http.StatusAccepted, map[string]any{"ok": true})
}

// handleInstallList returns the integrations catalogue with current
// installed status. Merges the agent's built-in list with the public
// Convex package registry so new tools ship without a CLI release.
func (s *HTTPServer) handleInstallList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	type entry struct {
		Name        string `json:"name"`
		Installed   bool   `json:"installed"`
		Description string `json:"description"`
		// New fields — UIs are free to ignore them.
		Path    string `json:"path,omitempty"`    // absolute binary path when installed
		Manager string `json:"manager,omitempty"` // best-guess install manager: brew, snap, cargo, …
		Kind    string `json:"kind,omitempty"`    // ai-runner / model-runtime / devtool / language / system
		Source  string `json:"source,omitempty"`  // "builtin" | "registry"
	}

	// Start with the agent's built-in catalogue — never fails even if
	// Convex is unreachable.
	seen := map[string]bool{}
	out := make([]entry, 0, len(integrations)+24)
	for _, p := range integrations {
		path := DiscoverBinary(p.name)
		out = append(out, entry{
			Name:        p.name,
			Installed:   path != "",
			Description: p.description,
			Path:        path,
			Manager:     guessManagerForPath(path),
			Source:      "builtin",
		})
		seen[p.name] = true
	}

	// Merge the public Convex registry — add anything we don't
	// already have. Kind comes from the registry; `Installed` checks
	// the binary on disk (faster than running the registry's
	// CheckCommand on every list call).
	convexSiteURL := ""
	if s != nil {
		convexSiteURL = s.convexURL
	}
	for _, rp := range PackageRegistry(convexSiteURL) {
		if seen[rp.Name] {
			continue
		}
		path := DiscoverBinary(rp.Name)
		out = append(out, entry{
			Name:        rp.Name,
			Installed:   path != "",
			Description: rp.Description,
			Path:        path,
			Manager:     guessManagerForPath(path),
			Kind:        rp.Kind,
			Source:      "registry",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
