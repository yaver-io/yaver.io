// stubagent — a stand-in Yaver agent that speaks the real /ops wire protocol,
// so the visionOS app can be driven end-to-end in the simulator without a
// signed-in daemon (the real agent refuses every call without Convex auth, and
// authing here would raise a keychain prompt).
//
// Shapes are copied from the agent's real responses: ops returns
// {ok, initial:{...}} and typed refusals come back as HTTP 200 {ok:false,error}.
// That last detail is the whole reason the "dead button" class of bug exists,
// so the stub reproduces it exactly rather than answering 4xx.
//
// POST /__scenario {"name":"..."} flips what `reload` does, which is how the UI
// test reaches paths a healthy machine never produces (nothing listening;
// backend refusal).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
)

var (
	mu       sync.RWMutex
	scenario = "delivered"
)

type opsReq struct {
	Verb    string                 `json:"verb"`
	Payload map[string]interface{} `json:"payload"`
	Machine string                 `json:"machine"`
}

func okResult(initial interface{}) map[string]interface{} {
	return map[string]interface{}{"ok": true, "initial": initial}
}

// refusal mirrors the agent: HTTP 200, ok:false, message in `error`.
func refusal(msg string) map[string]interface{} {
	return map[string]interface{}{"ok": false, "code": "not_running", "error": msg}
}

func main() {
	port := "18099"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/__scenario", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		scenario = body.Name
		mu.Unlock()
		log.Printf("[stub] scenario -> %s", body.Name)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "scenario": body.Name})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	})

	mux.HandleFunc("/runner/session/turn", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sess, _ := body["session"].(string)
		log.Printf("[stub] /runner/session/turn session=%q text=%v choice=%v", sess, body["text"], body["choice"])

		// The point of the named-session work: refuse to guess. A turn with no
		// session named is exactly what the old client sent.
		if sess == "" {
			json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "several runner sessions are live — name the one you mean",
			})
			return
		}
		if _, isChoice := body["choice"]; isChoice {
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "session": sess, "runner": "codex", "sent": "choice",
				"awaitingChoice": false,
				"pane":           "> option accepted\nrunning tests...\nall green.",
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "session": sess, "runner": "codex", "sent": "prompt",
			"awaitingChoice": true,
			"options":        []string{"Yes, apply the patch", "Show me the diff first"},
			"pane":           "> " + fmt.Sprint(body["text"]) + "\nI can apply this change. Proceed?",
		})
	})

	mux.HandleFunc("/ops", func(w http.ResponseWriter, r *http.Request) {
		var req opsReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.RLock()
		sc := scenario
		mu.RUnlock()
		log.Printf("[stub] /ops verb=%s payload=%v (scenario=%s)", req.Verb, req.Payload, sc)

		w.Header().Set("Content-Type", "application/json")
		var out interface{}

		switch req.Verb {
		case "info":
			out = okResult(map[string]any{
				"hostname": "vision-stub", "platform": "darwin", "arch": "arm64",
				"agentVersion": "1.99.304", "deviceId": "stub-device-01", "cpuPercent": 12.0,
			})
		case "status":
			out = okResult(map[string]any{
				"agentVersion": "1.99.304", "authExpired": false,
				"tasks": map[string]any{"total": 7, "running": 1},
				"devServer": map[string]any{
					"running": true, "framework": "expo", "port": 8081,
					"project": "sfmg", "workDir": "/Users/dev/Workspace/sfmg",
				},
			})
		case "runner_sessions":
			out = okResult(map[string]any{
				"count": 2,
				"sessions": []map[string]any{
					{"name": "yaver-codex", "runner": "codex", "attached": true},
					{"name": "yaver-claude", "runner": "claude", "attached": false},
				},
			})
		case "mobile_platform_matrix":
			out = okResult(map[string]any{
				"ok": true,
				"matrix": map[string]any{
					"device_platform": "darwin", "device_arch": "arm64",
					"surfaces": []map[string]any{
						{"id": "ios", "label": "iOS", "family": "apple", "status": "ready"},
						{"id": "visionos", "label": "visionOS", "family": "apple", "status": "ready"},
						{"id": "tvos", "label": "tvOS", "family": "apple", "status": "needs_setup"},
					},
				},
			})
		case "reload":
			mode, _ := req.Payload["mode"].(string)
			switch sc {
			case "refused":
				// The dead-button case: 200 OK carrying a refusal.
				out = refusal("no dev server is currently running — start one with /dev/start first")
			case "nobody":
				// Build/reload succeeded; nothing was listening.
				out = okResult(map[string]any{"mode": mode, "deliveredTo": 0, "changeClass": "js_only"})
			default:
				out = okResult(map[string]any{"mode": mode, "deliveredTo": 2, "changeClass": "js_only"})
			}
		default:
			out = map[string]any{"ok": false, "code": "unknown_verb", "error": "unknown verb " + req.Verb}
		}
		json.NewEncoder(w).Encode(out)
	})

	log.Printf("[stub] listening on 127.0.0.1:%s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, mux))
}
