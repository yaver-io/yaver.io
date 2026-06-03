package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterpolateCompanion(t *testing.T) {
	env := map[string]string{"CRON_AUTH_UUID": "tok123"}
	got := interpolateCompanion("${base_url}/rest/x?token=${CRON_AUTH_UUID}", env, "https://h/functions/v1")
	want := "https://h/functions/v1/rest/x?token=tok123"
	if got != want {
		t.Fatalf("interpolate = %q, want %q", got, want)
	}
	// Unknown tokens are left visible, not blanked.
	if got := interpolateCompanion("${UNKNOWN}", env, ""); got != "${UNKNOWN}" {
		t.Fatalf("unknown token should be preserved, got %q", got)
	}
}

func TestCompanionEngineUpArmsCronAndSkipsProposed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(home, "e-back")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
project: e-back
runtime:
  bind: device
  base_url_from: env:SUPABASE_FUNCTIONS_URL
env_from:
  - file: .env.companion
crons:
  - name: auto-mail
    schedule: "*/15 * * * *"
    idempotent: true
    request:
      method: POST
      url: "${base_url}/rest/autoMailSenderDirect?token=${CRON_AUTH_UUID}"
  - name: future-thing
    schedule: "0 3 * * *"
    status: proposed
    request:
      url: "${base_url}/rest/notYet"
`
	if err := os.WriteFile(filepath.Join(repo, CompanionManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dotenv := "SUPABASE_FUNCTIONS_URL=https://proj.supabase.co/functions/v1\nCRON_AUTH_UUID=secret-token-123\n"
	if err := os.WriteFile(filepath.Join(repo, ".env.companion"), []byte(dotenv), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadCompanionManifest(repo)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	sched := NewScheduler(nil)
	eng := &CompanionEngine{sched: sched, deviceID: "test-device"}

	status, err := eng.Up(m)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// The proposed cron must NOT be armed.
	for _, c := range status.Crons {
		if c.Name == "future-thing" {
			if !c.Proposed || c.Status != "proposed" {
				t.Fatalf("future-thing should be proposed-not-armed, got %+v", c)
			}
		}
	}

	// The real cron must be a Verb-mode schedule firing companion_http with the
	// fully interpolated URL (secret resolved on-device into OpsPayload).
	var armed *ScheduledTask
	for _, st := range sched.ListSchedules() {
		if st.Title == "companion:e-back:auto-mail" {
			armed = st
		}
		if st.Title == "companion:e-back:future-thing" {
			t.Fatalf("proposed cron must never be armed")
		}
	}
	if armed == nil {
		t.Fatalf("auto-mail cron was not armed; schedules=%d", len(sched.ListSchedules()))
	}
	if armed.Verb != companionHTTPVerb || armed.Machine != "local" || armed.Cron != "*/15 * * * *" {
		t.Fatalf("armed cron wrong shape: %+v", armed)
	}
	var payload struct {
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(armed.OpsPayload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	wantURL := "https://proj.supabase.co/functions/v1/rest/autoMailSenderDirect?token=secret-token-123"
	if payload.URL != wantURL {
		t.Fatalf("interpolated url = %q, want %q", payload.URL, wantURL)
	}
	if payload.Method != "POST" {
		t.Fatalf("method = %q, want POST", payload.Method)
	}

	// Re-running Up must be idempotent: update in place, not duplicate.
	if _, err := eng.Up(m); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	count := 0
	for _, st := range sched.ListSchedules() {
		if strings.HasPrefix(st.Title, "companion:e-back:auto-mail") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("idempotent Up produced %d auto-mail schedules, want 1", count)
	}

	// Reboot durability: a brand-new Scheduler reading the same persisted
	// store (simulating an agent restart after reboot) must re-arm the cron
	// without re-running Up — this is why companion crons survive reboot for
	// free (the agent's own OS unit restarts it; the scheduler reloads).
	reloaded := NewScheduler(nil)
	var survived bool
	for _, st := range reloaded.ListSchedules() {
		if st.Title == "companion:e-back:auto-mail" && st.Verb == companionHTTPVerb {
			survived = true
		}
	}
	if !survived {
		t.Fatalf("companion cron did not survive a scheduler reload (reboot durability)")
	}

	// Down removes the armed schedule.
	if err := eng.Down("e-back"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, st := range sched.ListSchedules() {
		if st.Title == "companion:e-back:auto-mail" {
			t.Fatalf("Down did not remove the armed cron")
		}
	}
}
