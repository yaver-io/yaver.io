package main

// runner_provider_http.go — runtime preflight for the local-model / on-prem /
// Salad-hosted-model lane. The company AI resolver (Convex) can advertise a
// provider, but it cannot prove the endpoint is reachable from a given on-prem
// runtime. This route does that check ON the runtime and returns a secret-free
// result, so a UI can show "provider: reachable / unreachable / needs key"
// before dispatching a task.
//
//   GET /runner-provider/preflight?runner=claude|codex|opencode
//
// Reads the runner-provider config from the local vault (BASE_URL/API_KEY,
// per-runner overrides) and probes <baseUrl>/v1/models. Never echoes the key.

import (
	"net/http"
	"strings"
	"time"
)

func (s *HTTPServer) handleRunnerProviderPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	runner := strings.TrimSpace(r.URL.Query().Get("runner"))
	if runner == "" {
		runner = s.taskMgr.runner.RunnerID
	}
	client := &http.Client{Timeout: 6 * time.Second}
	res := runnerProviderPreflight(runner, client)
	jsonReply(w, http.StatusOK, res)
}
