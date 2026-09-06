package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The wire shape of the 500. `error` must survive untouched (shipped clients
// render it), and `capabilityGap` must be the SAME object the preview lane
// ships, so mobile/src/lib/capabilityGap.ts parses it without a second parser.
func TestTaskCreateFailureBodyCarriesTheRouteAndKeepsTheError(t *testing.T) {
	// This test owns the Tasks HTTP wire contract, not the host running the
	// suite. Pin enough headroom so a populated Go cache on the 4 GB dogfood
	// worker cannot turn the expected install route into a disk-space route.
	// capability_resources_test.go separately breaks the resource guard with a
	// measured full disk and proves that the install button disappears there.
	capabilityGapTestWithHeadroom(t)

	raw := "failed to create task: runner not ready: claude not found in PATH or common locations"
	body := taskCreateFailureBody("claude", raw)

	if body["ok"] != false {
		t.Errorf("ok = %v, want false — the legacy shape must not change", body["ok"])
	}
	if body["error"] != raw {
		t.Errorf("error = %v, want the raw text unchanged — a shipped client must not lose the detail it already shows", body["error"])
	}
	gap, ok := body["capabilityGap"].(*CapabilityGap)
	if !ok || gap == nil {
		t.Fatal("no capabilityGap on the 500 — the Tasks lane is back to a dead end with a sentence")
	}
	if gap.Fix == nil || gap.Fix.Path != "/install/claude" || gap.Fix.Stream != "install:claude" {
		t.Fatalf("Fix = %+v, want POST /install/claude streaming install:claude", gap.Fix)
	}

	// It has to survive encoding/json exactly as the twins expect: lowercase
	// `code`, `summary`, `fix.{path,stream}`. A struct that marshals to
	// something parseCapabilityGap rejects is a button that never renders.
	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(blob, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wire, _ := round["capabilityGap"].(map[string]any)
	if wire == nil {
		t.Fatal("capabilityGap did not survive JSON")
	}
	for _, key := range []string{"code", "capability", "summary", "fix"} {
		if _, present := wire[key]; !present {
			t.Errorf("wire object is missing %q — parseCapabilityGap() would drop this", key)
		}
	}
	fix, _ := wire["fix"].(map[string]any)
	if fix == nil || fix["path"] != "/install/claude" || fix["stream"] != "install:claude" {
		t.Errorf("wire fix = %v — the twins key off path+stream and render nothing without both", wire["fix"])
	}
}

// A failure with a different remedy must NOT grow an Install button.
func TestTaskCreateFailureBodyStaysQuietWhenTheRemedyIsNotAnInstall(t *testing.T) {
	body := taskCreateFailureBody("codex", "failed to create task: title is required")
	if _, present := body["capabilityGap"]; present {
		t.Fatal("a validation error must not advertise an install — that is 'yaver lies' with extra steps")
	}
	if _, present := body["errorSummary"]; present {
		t.Fatal("no gap means no summary override")
	}
}

// The 201-with-status-failed lane: the reason lives in task.Output and the
// chat renders it. The tap goes next to it.
func TestFailedTaskResponseCarriesTheRoute(t *testing.T) {
	capabilityGapTestWithHeadroom(t)

	resp := map[string]interface{}{"ok": true, "taskId": "t1", "status": TaskStatusFailed}
	out := decorateTaskResponseWithGap(resp, "opencode",
		"Could not start OpenCode: runner not ready: opencode not found in PATH or common locations\n")
	gap, ok := out["capabilityGap"].(*CapabilityGap)
	if !ok || gap == nil || gap.Fix == nil {
		t.Fatal("the failed bubble must carry the Install route beside the reason it already prints")
	}
	if gap.Fix.Path != "/install/opencode" {
		t.Errorf("Fix.Path = %q, want /install/opencode", gap.Fix.Path)
	}
	// Untouched keys stay untouched.
	if out["taskId"] != "t1" || out["ok"] != true {
		t.Error("decoration must be additive")
	}
}

// THE WIRING GUARD. A producer with no call site is not shipped — the audit's
// rule 11, learned from recoverKind and capture_error, both of which are
// correct code that nothing ever reads. This test fails the moment someone
// removes the two call sites in httpserver.go, which is the only way this
// whole file can silently stop mattering.
func TestTasksHandlerActuallyCallsTheGapProducers(t *testing.T) {
	src, err := os.ReadFile("httpserver.go")
	if err != nil {
		t.Fatalf("read httpserver.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "taskCreateFailureBody(body.Runner") {
		t.Error("the /tasks 500 no longer calls taskCreateFailureBody — the Tasks lane lost its Install route")
	}
	if !strings.Contains(text, "decorateTaskResponseWithGap(resp, task.RunnerID, task.Output)") {
		t.Error("the /tasks 201-failed response no longer calls decorateTaskResponseWithGap — the failed bubble lost its Install route")
	}
	// And it must not have regressed to jsonError, which cannot carry a gap.
	if strings.Contains(text, `jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create task: %v", err))`) {
		t.Error("the /tasks 500 is back on jsonError, whose {ok,error} shape has no room for a route")
	}
}
