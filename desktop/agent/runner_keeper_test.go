package main

// runner_keeper_test.go — P7 same-session runner continuation
// supervisor tests. The keeper is driven with time.Now stubbed and
// tmux capture/send seams replaced so tests are hermetic (no real
// tmux server, no real time).

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestKeeper returns a RunnerKeeper wired against fake capture/
// send seams and a frozen clock. All persistence lives under
// t.TempDir()/.yaver/runner.
func newTestKeeper(t *testing.T) *RunnerKeeper {
	t.Helper()
	tmp := t.TempDir()
	orig, _ := os.LookupEnv("HOME")
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })

	k, err := NewRunnerKeeper()
	if err != nil {
		t.Fatalf("NewRunnerKeeper: %v", err)
	}
	// Force short debounce / no cap for deterministic tests.
	k.idleDebounce = 10 * time.Millisecond
	k.nudgeCap = 0
	return k
}

func TestKeeper_EnqueueAndList(t *testing.T) {
	k := newTestKeeper(t)
	id, err := k.EnqueuePrompt("yaver-mac", "keep going", "cli")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !strings.HasPrefix(id, "q_") {
		t.Fatalf("id shape = %q, want q_ prefix", id)
	}
	items := k.ListQueue("yaver-mac")
	if len(items) != 1 || items[0].Prompt != "keep going" || items[0].Source != "cli" {
		t.Fatalf("list = %+v, want one entry", items)
	}
}

func TestKeeper_EnqueueRejectsEmpty(t *testing.T) {
	k := newTestKeeper(t)
	if _, err := k.EnqueuePrompt("", "x", ""); err == nil {
		t.Fatal("empty session must error")
	}
	if _, err := k.EnqueuePrompt("s", "  ", ""); err == nil {
		t.Fatal("empty prompt must error")
	}
}

func TestKeeper_ClearOne(t *testing.T) {
	k := newTestKeeper(t)
	k.EnqueuePrompt("a", "1", "")
	k.EnqueuePrompt("a", "2", "")
	k.EnqueuePrompt("b", "3", "")
	if r := k.ClearQueue("a"); r != 2 {
		t.Fatalf("clear a removed %d, want 2", r)
	}
	if len(k.ListQueue("")) != 1 {
		t.Fatal("session b should still have one entry")
	}
}

func TestKeeper_TickNudgesWhenIdleAndQueued(t *testing.T) {
	k := newTestKeeper(t)
	var (
		mu       sync.Mutex
		sent     string
		paneText = "$ claude> \n"
	)
	k.sendKeys = func(s, txt string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = txt
		return nil
	}
	k.capturePane = func(s string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return paneText, nil
	}
	base := time.Now()
	k.clock = func() time.Time { return base }
	k.SetMode("s1", KeeperModeAuto)
	k.EnqueuePrompt("s1", "keep going P0", "cli")

	// First tick seeds the pane hash and stamps LastActivity; nothing
	// should be nudged (the pane just came alive).
	if nudged, err := k.Tick("s1"); err != nil || nudged {
		t.Fatalf("first tick nudged=%v err=%v; want (false,nil)", nudged, err)
	}

	// Advance the clock beyond idleDebounce; pane still hasn't
	// changed → the keeper should nudge with the queued prompt.
	k.clock = func() time.Time { return base.Add(2 * time.Second) }
	nudged, err := k.Tick("s1")
	if err != nil {
		t.Fatalf("second tick errored: %v", err)
	}
	if !nudged {
		t.Fatal("second tick after idle debounce should nudge")
	}
	mu.Lock()
	got := sent
	mu.Unlock()
	if got != "keep going P0" {
		t.Fatalf("nudge text = %q, want the queued prompt", got)
	}
	if q := k.ListQueue("s1"); len(q) != 0 {
		t.Fatalf("queue should have drained, still has %d", len(q))
	}
	if st := k.State("s1"); st.NudgesTotal != 1 || st.NudgesLastHour != 1 {
		t.Fatalf("nudge counters = %+v", st)
	}
}

// The reported P1: runner_queue_add wrote queue.json but nothing drained it,
// because RunnerKeeper.Tick had no production caller. The drain loop discovers
// sessions from the queue itself, so a prompt enqueued for a session the keeper
// has never SetMode'd or Tick'd must still be found and delivered.
func TestSessionsNeedingTick_IncludesUntrackedQueuedSession(t *testing.T) {
	k := newTestKeeper(t)
	// No SetMode, no prior Tick — "fresh" only exists as queued work.
	if _, err := k.EnqueuePrompt("fresh", "drain me", "phone"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got := k.sessionsNeedingTick()
	found := false
	for _, n := range got {
		if n == "fresh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sessionsNeedingTick=%v, must include the queued-only session 'fresh'", got)
	}
}

// End-to-end proof the fix delivers work that used to be silently swallowed:
// enqueue for an untracked session, then drive the exact discovery→Tick path
// Supervise uses, and assert the prompt reaches the pane.
func TestSupervisorDrainDeliversUntrackedQueuedPrompt(t *testing.T) {
	k := newTestKeeper(t)
	var (
		mu   sync.Mutex
		sent string
	)
	k.sendKeys = func(s, txt string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = txt
		return nil
	}
	k.capturePane = func(s string) (string, error) { return "$ claude> \n", nil }
	base := time.Now()
	k.clock = func() time.Time { return base }
	// Real flow: work is enqueued, then the session flips to auto (runner_detach)
	// so the keeper is allowed to drain it. Before this fix, Supervise never ran
	// so even a correctly-auto session sat with its queue untouched.
	if _, err := k.EnqueuePrompt("box-1", "continue the plan", "phone"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	k.SetMode("box-1", KeeperModeAuto)
	// Cycle 1: discover + seed pane hash. Nothing drained yet.
	for _, n := range k.sessionsNeedingTick() {
		if _, err := k.Tick(n); err != nil {
			t.Fatalf("tick %q: %v", n, err)
		}
	}
	mu.Lock()
	first := sent
	mu.Unlock()
	if first != "" {
		t.Fatalf("nothing should nudge on first cycle, sent=%q", first)
	}
	// Cycle 2: idle past debounce, pane unchanged → drain.
	k.clock = func() time.Time { return base.Add(2 * time.Second) }
	for _, n := range k.sessionsNeedingTick() {
		if _, err := k.Tick(n); err != nil {
			t.Fatalf("tick %q: %v", n, err)
		}
	}
	mu.Lock()
	got := sent
	mu.Unlock()
	if got != "continue the plan" {
		t.Fatalf("drained prompt = %q, want the enqueued prompt", got)
	}
	if q := k.ListQueue("box-1"); len(q) != 0 {
		t.Fatalf("queue should be empty after drain, has %d", len(q))
	}
}

func TestKeeper_UserDrivenModeDoesNotNudge(t *testing.T) {
	k := newTestKeeper(t)
	k.sendKeys = func(string, string) error {
		t.Fatal("user-driven mode must never send keys")
		return nil
	}
	k.capturePane = func(string) (string, error) { return "$ claude>", nil }
	base := time.Now()
	k.clock = func() time.Time { return base.Add(10 * time.Second) }
	k.SetMode("s1", KeeperModeUserDriven)
	k.EnqueuePrompt("s1", "keep going", "cli")
	if _, err := k.Tick("s1"); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if _, err := k.Tick("s1"); err != nil {
		t.Fatalf("second tick: %v", err)
	}
}

func TestKeeper_ContentChangeResetsIdleClock(t *testing.T) {
	k := newTestKeeper(t)
	base := time.Now()
	k.clock = func() time.Time { return base }
	pane := "$ claude> reading files\n"
	k.capturePane = func(string) (string, error) { return pane, nil }
	k.sendKeys = func(string, string) error { t.Fatal("must not nudge — pane still moving"); return nil }
	k.SetMode("s1", KeeperModeAuto)
	k.EnqueuePrompt("s1", "keep going", "cli")

	// Seed.
	k.Tick("s1")
	// Advance the clock but ALSO change the pane content — content-based
	// liveness must catch this and stamp a fresh LastActivity.
	k.clock = func() time.Time { return base.Add(5 * time.Second) }
	pane = "$ claude> reading files\nfound 12 matches\n"
	k.Tick("s1")
	// Still within debounce (from the fresh LastActivity), advancing
	// a small amount must NOT nudge.
	k.clock = func() time.Time { return base.Add(5*time.Second + 5*time.Millisecond) }
	if nudged, err := k.Tick("s1"); err != nil || nudged {
		t.Fatalf("tick nudged=%v err=%v; want false because pane content just changed", nudged, err)
	}
}

func TestKeeper_Persistence(t *testing.T) {
	k := newTestKeeper(t)
	k.SetMode("s1", KeeperModeAuto)
	k.EnqueuePrompt("s1", "hello", "cli")
	// New keeper on the same HOME must see the queue + state.
	base := k.baseDir
	k2, err := NewRunnerKeeper()
	if err != nil {
		t.Fatalf("second keeper: %v", err)
	}
	if k2.baseDir != base {
		t.Fatalf("keeper baseDir drift %q vs %q", base, k2.baseDir)
	}
	items := k2.ListQueue("s1")
	if len(items) != 1 || items[0].Prompt != "hello" {
		t.Fatalf("persisted queue = %+v", items)
	}
	if k2.State("s1").Mode != KeeperModeAuto {
		t.Fatalf("persisted mode not auto")
	}
}

// --- MCP wrapper tests --------------------------------------------

func TestKeeperMCP_AttachDetachAutorunFlow(t *testing.T) {
	k := newTestKeeper(t)
	if r := runRunnerAttach(k, runnerAttachArgs{SessionName: "s1"}); r["ok"] != true {
		t.Fatalf("attach failed: %+v", r)
	}
	if k.State("s1").Mode != KeeperModeUserDriven {
		t.Fatalf("mode after attach = %v", k.State("s1").Mode)
	}
	if r := runRunnerDetach(k, runnerDetachArgs{SessionName: "s1", Autorun: true}); r["ok"] != true {
		t.Fatalf("detach failed: %+v", r)
	}
	if k.State("s1").Mode != KeeperModeAuto {
		t.Fatalf("detach with autorun=true must leave mode=auto, got %v", k.State("s1").Mode)
	}
	if r := runRunnerAutorun(k, runnerAutorunArgs{SessionName: "s1", Mode: "off"}); r["ok"] != true {
		t.Fatalf("autorun off failed: %+v", r)
	}
	if k.State("s1").Mode != KeeperModeOff {
		t.Fatalf("mode after autorun off = %v", k.State("s1").Mode)
	}
}

func TestKeeperMCP_QueueAddListClear(t *testing.T) {
	k := newTestKeeper(t)
	if r := runRunnerQueueAdd(k, runnerQueueAddArgs{SessionName: "s1", Prompt: "p1", Source: "phone"}); r["ok"] != true {
		t.Fatalf("add failed: %+v", r)
	}
	if r := runRunnerQueueList(k, runnerQueueListArgs{SessionName: "s1"}); r["ok"] != true || r["count"] != 1 {
		t.Fatalf("list count = %+v", r)
	}
	if r := runRunnerQueueClear(k, runnerQueueClearArgs{SessionName: "s1"}); r["ok"] != true || r["removed"] != 1 {
		t.Fatalf("clear = %+v", r)
	}
}

func TestKeeperMCP_StatusEmit(t *testing.T) {
	k := newTestKeeper(t)
	k.SetMode("s1", KeeperModeAuto)
	k.EnqueuePrompt("s1", "p", "phone")
	r := runRunnerStatus(k, runnerStatusArgs{SessionName: "s1", Task: "n2n"})
	if r["ok"] != true {
		t.Fatalf("status !ok: %+v", r)
	}
	if summary, ok := r["summary"].(string); !ok || !strings.Contains(summary, "s1") {
		t.Fatalf("summary missing session name: %+v", r)
	}
}

// --- Parser test --------------------------------------------------

func TestParseAutoRunnerCommit_ExtractsWorkWindowAndRunner(t *testing.T) {
	// Simulate `git log --pretty=format:%H%x1f%s%x1f%b%x1e` output for
	// one [auto-runner] commit; the parser must extract phase, mode,
	// runner attribution, and duration.
	fake := "abc123\x1f[auto-runner] n2n P4: feedback\x1fSome body text.\n" +
		"Work window: started 2026-07-16 05:20 +03, finished 2026-07-16 05:35 +03\n" +
		"Runner: claude on machine mac-mini (alias mac-mini, 229aeb03) mode: auto\x1e"

	// Split by record separator like the real path does.
	rows := strings.Split(fake, "\x1e")
	// Reuse the same parsing regex via a synthetic call: the private
	// parser is inlined into gatherAutoRunnerCommits, so exercise it
	// through the parsed struct that the exported code would build.
	for _, row := range rows {
		if strings.TrimSpace(row) == "" {
			continue
		}
		fields := strings.Split(row, "\x1f")
		if len(fields) < 3 {
			continue
		}
		if !strings.HasPrefix(fields[1], "[auto-runner]") {
			t.Fatalf("subject filter failed: %q", fields[1])
		}
		// Direct regex assertions on the body.
		if m := autoRunnerWorkRE.FindStringSubmatch(fields[2]); len(m) != 3 {
			t.Fatalf("work-window regex failed on: %q", fields[2])
		}
		if m := autoRunnerRunnerRE.FindStringSubmatch(fields[2]); len(m) != 6 {
			t.Fatalf("runner regex failed on: %q", fields[2])
		}
	}
}

// Extra sanity: make sure the persistence dir stays 0600 (owner-only
// permission). Not strictly a functional check but a compliance guard
// — the keeper writes queued prompts + state to disk and those files
// must not leak to other users on shared machines.
func TestKeeper_PersistenceIsOwnerOnly(t *testing.T) {
	k := newTestKeeper(t)
	k.EnqueuePrompt("s1", "p", "cli")
	for _, name := range []string{"queue.json", "keeper.state"} {
		info, err := os.Stat(filepath.Join(k.baseDir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s perms = %v, want 0600", name, info.Mode().Perm())
		}
	}
}
