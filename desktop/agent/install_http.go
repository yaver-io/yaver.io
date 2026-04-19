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

	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	tool := rest
	plan, ok := lookupIntegration(tool)
	if !ok {
		jsonError(w, http.StatusNotFound, "unknown tool: "+tool+" — see GET /install/list")
		return
	}

	if s.streams == nil {
		jsonError(w, http.StatusServiceUnavailable, "log streams not enabled on this agent")
		return
	}

	streamName := "install:" + tool
	stream := s.streams.Get(streamName)

	// Run the installer in a goroutine — phone subscribes to the
	// stream for live progress and a final {"type":"result", ...}
	// event signals success or failure.
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
