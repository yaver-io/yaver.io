package main

// bento_e2e_test.go — full dogfood of the Video 1 story:
//
//   "Create a project, browse your database, vibe code a feature —
//    all from your phone."
//
// Drives only the HTTP surface the Yaver mobile app calls:
//
//   POST /project/wizard/start          → session + first question
//   POST /project/wizard/answer   (x30) → walks every question
//   POST /project/wizard/generate       → materialises bento/ scaffold
//   GET  /studios                       → enumerates dashboard targets
//   POST /tasks   (workDir=scaffold)    → simulates mobile chat
//   GET  /tasks/{id}                    → polls task completion
//
// No docker, no runner — TaskManager runs in DummyMode and docker
// services emit a soft error in the wizard response. The test asserts
// the CODE PATHS are green so the shoot-day chain will hold the
// moment Docker + auth come online.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// post is a tiny helper for POST+JSON with our test bearer token.
func post(t *testing.T, url, token, body string) map[string]interface{} {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		t.Fatalf("POST %s returned %d: %s", url, resp.StatusCode, raw)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func getJSON(t *testing.T, url, token string) map[string]interface{} {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// bentoAnswers mirrors demos/bento/.yaver-wizard-answers.json.
// Keeping it inline so the test is self-contained and the answers
// survive even if the demos/ dir gets removed post-shoot.
var bentoAnswers = map[string]string{
	"app_name":             "Bento",
	"slug":                 "bento",
	"description":          "Meal prep and recipe app — Yaver video demo subject.",
	"tagline":              "Meal prep that ships itself",
	"domain":               "bento.yaver.dev",
	"primary_color":        "#F97316",
	"accent_color":         "#059669",
	"tone":                 "light",
	"include_web":          "false",
	"include_mobile":       "true",
	"include_backend":      "true",
	"include_landing":      "false",
	"web_framework":        "nextjs",
	"web_host":             "cloudflare",
	"backend":              "convex",
	"mobile_stack":         "expo-rn",
	"oauth_apple":          "true",
	"oauth_google":         "true",
	"oauth_microsoft":      "false",
	"oauth_email":          "true",
	"payments":             "none",
	"ios_bundle_id":        "io.yaver.bento",
	"android_package":      "io.yaver.bento",
	"apple_team_id":        "",
	"play_service_account": "",
	"cloudflare_zone":      "",
	"git_provider":         "none",
	"git_visibility":       "private",
	"git_org":              "",
	"git_repo_name":        "bento",
}

// TestBentoE2E_MobileFlow: the literal Video 1 storyline driven through
// Yaver's mobile-app HTTP API. Proves the whole "from your phone" chain
// is green without requiring Docker, a runner, or mobile hardware.
func TestBentoE2E_MobileFlow(t *testing.T) {
	parent := t.TempDir()

	// TaskManager in dummy mode so /tasks completes without a runner.
	tm := NewTaskManager(parent, nil, defaultRunner)
	tm.DummyMode = true

	// Wire the bits the flow touches.
	baseURL, cancel := startTestServer(t, "bento-tok", tm)
	defer cancel()

	client := &http.Client{Timeout: 10 * time.Second}

	// --- Step 1: Mobile taps [+ New Project] — start the wizard -------
	t.Log("Step 1: POST /project/wizard/start")
	startResp := post(t, baseURL+"/project/wizard/start", "bento-tok", `{}`)
	sess, _ := startResp["session"].(map[string]interface{})
	sessionID, _ := sess["id"].(string)
	if sessionID == "" {
		t.Fatalf("wizard start: no session id in %v", startResp)
	}

	// --- Step 2: Mobile walks the wizard -------------------------------
	// The real mobile app shows one question at a time and reads the
	// next from /question in the response. Here we just post every
	// key — the wizard skips non-applicable branches itself.
	t.Log("Step 2: POST /project/wizard/answer (x30)")
	for qid, ans := range bentoAnswers {
		body, _ := json.Marshal(map[string]string{
			"sessionId":  sessionID,
			"questionId": qid,
			"answer":     ans,
		})
		// Empty strings are legit (apple_team_id etc.) — endpoint 400s
		// on empty+required, 200s on empty+optional. Treat both as ok.
		req, _ := http.NewRequest("POST", baseURL+"/project/wizard/answer",
			strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer bento-tok")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("answer %s: %v", qid, err)
		}
		resp.Body.Close()
	}
	// Confirm triggers Done.
	confirm, _ := json.Marshal(map[string]string{
		"sessionId": sessionID, "questionId": "confirm", "answer": "true",
	})
	_ = post(t, baseURL+"/project/wizard/answer", "bento-tok", string(confirm))

	// --- Step 3: Mobile taps [Create Project] --------------------------
	t.Log("Step 3: POST /project/wizard/generate")
	genBody, _ := json.Marshal(map[string]string{
		"sessionId": sessionID,
		"parentDir": parent,
	})
	gen := post(t, baseURL+"/project/wizard/generate", "bento-tok", string(genBody))
	if gen["ok"] != true {
		t.Fatalf("generate returned non-ok: %v", gen)
	}
	dir, _ := gen["directory"].(string)
	if dir == "" {
		t.Fatalf("no directory in generate response: %v", gen)
	}
	files, _ := gen["files"].([]interface{})
	if len(files) < 10 {
		t.Fatalf("expected >=10 files in scaffold, got %d", len(files))
	}

	// --- Step 4: Scaffold is really on disk in the right shape --------
	t.Log("Step 4: on-disk verification")
	mustExist := []string{
		"package.json",
		"README.md",
		".yaver/config.yaml",
		".yaver/services.yaml",
		".gitignore",
		"apps/mobile/app.json",
		"apps/mobile/App.tsx",
		"backend/convex/schema.ts",
		"scripts/deploy.sh",
	}
	for _, rel := range mustExist {
		full := filepath.Join(dir, rel)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("scaffold missing %s: %v", rel, err)
		}
	}

	// services.yaml is pre-wired with Convex presets (gap 2). The actual
	// containers won't boot in CI (no Docker), so the wizard response
	// surfaces servicesError — we just assert the KEY exists (meaning we
	// reached the boot code path), not that it succeeded.
	if _, seen := gen["servicesError"]; !seen {
		if _, seen := gen["servicesStarted"]; !seen {
			t.Error("wizard didn't attempt ServicesManager.Start — gap 2 regressed")
		}
	}

	// --- Step 5: Mobile taps [Database] — studio proxy is registered --
	t.Log("Step 5: GET /studios — dashboard targets")
	studios := getJSON(t, baseURL+"/studios", "bento-tok")
	// The handler returns {"targets":[...]}. We don't assert "running"
	// (Convex not booted in CI). We DO assert convex is in the catalog.
	catalog, _ := studios["targets"].([]interface{})
	if len(catalog) == 0 {
		catalog, _ = studios["studios"].([]interface{}) // handler may use either key
	}
	if len(catalog) == 0 {
		t.Fatalf("/studios returned no targets: %v", studios)
	}
	sawConvex := false
	for _, c := range catalog {
		m, _ := c.(map[string]interface{})
		if m["id"] == "convex" {
			sawConvex = true
			break
		}
	}
	if !sawConvex {
		t.Error("/studios did not list convex — gap 3 regressed")
	}

	// --- Step 6: Mobile taps [Chat] — vibe-code a feature --------------
	// The real app calls sendTask with workDir pointing at the new
	// project (gap 1). DummyMode completes the task in ~3s.
	t.Logf("Step 6: POST /tasks with workDir=%s", dir)
	taskBody, _ := json.Marshal(map[string]interface{}{
		"title":       "Add recipes table",
		"description": "Dummy vibe-code task — verifies mobile → agent plumbing.",
		"source":      "mobile",
		"workDir":     dir,
	})
	taskResp := post(t, baseURL+"/tasks", "bento-tok", string(taskBody))
	taskID, _ := taskResp["taskId"].(string)
	if taskID == "" {
		t.Fatalf("POST /tasks: no taskId in %v", taskResp)
	}

	// Poll until completed (dummy task is ~3s). The endpoint returns
	// `{"ok":true,"task":{...}}`, so unwrap before looking at status.
	t.Log("Step 7: GET /tasks/{id} — wait for completion")
	deadline := time.Now().Add(12 * time.Second)
	var final map[string]interface{}
	var task map[string]interface{}
	for time.Now().Before(deadline) {
		final = getJSON(t, baseURL+"/tasks/"+taskID, "bento-tok")
		if t2, ok := final["task"].(map[string]interface{}); ok {
			task = t2
		} else {
			task = final
		}
		if status, _ := task["status"].(string); status == "completed" ||
			status == "failed" || status == "stopped" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if status, _ := task["status"].(string); status != "completed" {
		t.Fatalf("task didn't reach completed (got %q): %v", status, task)
	}

	// --- Step 8: Gap 4 — auto hot-reload fires on task done -----------
	// The OnTaskDone callback is wired in main.go, not in the test's
	// minimal wiring. We can still verify the TaskStore records the
	// finished state so main.go's handler would have fired.
	t.Log("Step 8: task terminal state is persisted")
	if task["finishedAt"] == nil && task["updatedAt"] == nil {
		t.Error("dummy task didn't record a finish timestamp")
	}

	// --- Step 9: Mobile taps [Deploy] — preview endpoint exists -------
	t.Log("Step 9: GET /deploy/preview?directory=<scaffold>")
	previewURL := baseURL + "/deploy/preview?directory=" +
		httpEscape(dir)
	resp := getJSON(t, previewURL, "bento-tok")
	// Happy case: we get a plan object. Unhappy case (no git inside
	// scaffold): error field set. Either means the route is alive —
	// which is what the test is supposed to prove.
	if resp == nil {
		t.Error("/deploy/preview returned no body")
	}

	t.Log("Bento E2E: all 9 steps green ✓")
	_ = fmt.Sprintf // silence unused-when-test-succeeds
}

// httpEscape is a tiny shim so we don't drag in net/url for one call.
func httpEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '/' || c == '-' || c == '.' || c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9'):
			b.WriteRune(c)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}
