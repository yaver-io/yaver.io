package main

// runner_test.go — Phase 1 coverage for the unified Runner (see
// RUNNER_DEV.md + runner.go). Pins behaviour the HTTP/MCP/CLI layers
// depend on so a refactor that breaks the contract trips here first.
//
// What's covered:
//
//   - PoolMatches: any-match, label-match, mismatch, case-insensitive.
//   - RunnerStore: AddJob upsert + CreatedAt preservation, ListJobs
//     by pool, RemoveJob, SetPaused.
//   - RunLifecycle: Start/Append/Finish updates OutputTail + duration
//     and the ring buffer evicts the oldest run + removes its on-disk
//     dir. Run is reachable via GetRun and ListRuns.
//   - Disk persistence: Start writes meta.json; reload via a fresh
//     RunnerStore picks the run back up; in-progress flag is reset.
//   - Guest filter: GetRun and ListRuns hide runs from a different
//     guestUserID.
//   - Shell exec: runJobShell against /bin/sh -c "echo hi" returns OK
//     with the line on the tail; non-zero exit is reported; timeout
//     fires within bounds.
//   - composeRunnerEnv: jobEnv overrides parent env, guest sandbox
//     drops arbitrary vars but keeps PATH.
//   - LocalCapabilities: emits the canonical labels (any, os:*, arch:*).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *RunnerStore {
	t.Helper()
	dir := t.TempDir()
	// ConfigDir() reads $HOME via os.UserHomeDir on POSIX; redirect
	// it so the runner store lands under the temp dir and tests
	// don't pollute the user's real ~/.yaver/runner.
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // Windows
	store := NewRunnerStore(3)
	if store.runRoot == "" {
		t.Skip("config dir unavailable in this env — disk path tests skipped")
	}
	return store
}

func TestPoolMatches(t *testing.T) {
	caps := []string{"any", "os:linux", "arch:amd64", "host:dev-box"}
	for _, tc := range []struct {
		pool string
		want bool
	}{
		{"", true},
		{"any", true},
		{"os:linux", true},
		{"OS:Linux", true}, // case-insensitive
		{"arch:amd64", true},
		{"host:DEV-BOX", true},
		{"linux-arm64", false},
		{"darwin-arm64", false},
	} {
		if got := PoolMatches(tc.pool, caps); got != tc.want {
			t.Errorf("PoolMatches(%q) = %v; want %v", tc.pool, got, tc.want)
		}
	}
}

func TestRunnerStoreCRUD(t *testing.T) {
	s := newTestStore(t)

	// Initial empty.
	if jobs := s.ListJobs(""); len(jobs) != 0 {
		t.Fatalf("expected empty job list, got %d", len(jobs))
	}

	// Add upserts.
	first, err := s.AddJob(RunnerJob{Name: "hello", Command: "echo hi"})
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if first.CreatedAt == 0 {
		t.Fatalf("CreatedAt should be set on first insert")
	}
	createdAt := first.CreatedAt
	time.Sleep(2 * time.Millisecond)

	updated, err := s.AddJob(RunnerJob{Name: "hello", Command: "echo bye"})
	if err != nil {
		t.Fatalf("AddJob upsert: %v", err)
	}
	if updated.CreatedAt != createdAt {
		t.Errorf("CreatedAt regressed on upsert: was %d, now %d", createdAt, updated.CreatedAt)
	}
	if updated.UpdatedAt <= updated.CreatedAt {
		t.Errorf("UpdatedAt should advance on upsert")
	}
	if updated.Command != "echo bye" {
		t.Errorf("upsert did not replace command")
	}

	// Empty name rejected.
	if _, err := s.AddJob(RunnerJob{Command: "echo x"}); err == nil {
		t.Error("expected error on empty job name")
	}
	// Shell job without command rejected.
	if _, err := s.AddJob(RunnerJob{Name: "broken"}); err == nil {
		t.Error("expected error on shell job without command")
	}

	// List by pool.
	if _, err := s.AddJob(RunnerJob{Name: "linuxonly", Command: "echo l", Pool: "os:linux"}); err != nil {
		t.Fatalf("AddJob linuxonly: %v", err)
	}
	if got := s.ListJobs("os:linux"); len(got) != 1 || got[0].Name != "linuxonly" {
		t.Errorf("ListJobs by pool = %#v", got)
	}

	// Pause toggle.
	if err := s.SetPaused("hello", true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	hello, _ := s.GetJob("hello")
	if !hello.Paused {
		t.Errorf("SetPaused did not stick")
	}

	// Remove.
	if err := s.RemoveJob("hello"); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}
	if _, ok := s.GetJob("hello"); ok {
		t.Errorf("RemoveJob did not delete the entry")
	}
	if err := s.RemoveJob("nonexistent"); err == nil {
		t.Error("expected error removing unknown job")
	}
}

func TestRunnerStoreRunLifecycle(t *testing.T) {
	s := newTestStore(t)
	rec := s.Start(RunnerRun{JobName: "x", Kind: RunnerJobShell})
	if rec.ID == "" || !rec.InProgress {
		t.Fatalf("Start should mint ID and mark InProgress: %+v", rec)
	}
	if rec.LogPath == "" {
		t.Fatalf("LogPath should be set when runRoot exists")
	}
	if _, err := os.Stat(rec.LogPath); err != nil {
		t.Fatalf("log file missing on disk: %v", err)
	}

	s.Append(rec.ID, "first line")
	s.Append(rec.ID, "second line")
	s.Finish(rec.ID, 0, false)

	final, ok := s.GetRun(rec.ID, "")
	if !ok {
		t.Fatalf("GetRun lost the record")
	}
	if final.InProgress {
		t.Error("Finish should clear InProgress")
	}
	if !final.OK || final.ExitCode != 0 {
		t.Errorf("expected OK / exitCode=0, got OK=%v exitCode=%d", final.OK, final.ExitCode)
	}
	if !strings.Contains(final.OutputTail, "first line") || !strings.Contains(final.OutputTail, "second line") {
		t.Errorf("OutputTail missing lines: %q", final.OutputTail)
	}
	if final.DurationMs < 0 {
		t.Errorf("DurationMs negative: %d", final.DurationMs)
	}

	// meta.json was written and contains the same id.
	meta, err := os.ReadFile(filepath.Join(s.runRoot, rec.ID, "meta.json"))
	if err != nil {
		t.Fatalf("meta.json read: %v", err)
	}
	var parsed RunnerRun
	if err := json.Unmarshal(meta, &parsed); err != nil {
		t.Fatalf("meta.json parse: %v", err)
	}
	if parsed.ID != rec.ID {
		t.Errorf("meta.json id mismatch: got %q want %q", parsed.ID, rec.ID)
	}
	if parsed.LogPath != "" {
		t.Errorf("LogPath leaked into on-disk meta.json: %q (must be host-private)", parsed.LogPath)
	}
}

func TestRunnerStoreRingBufferEvicts(t *testing.T) {
	s := newTestStore(t) // maxRuns = 3
	var ids []string
	for i := 0; i < 5; i++ {
		r := s.Start(RunnerRun{JobName: "j"})
		s.Finish(r.ID, 0, false)
		ids = append(ids, r.ID)
	}
	if got := len(s.ListRuns("", "", 0)); got != 3 {
		t.Fatalf("ListRuns size after eviction = %d; want 3", got)
	}
	// Oldest two evicted; their on-disk dirs gone.
	for _, id := range ids[:2] {
		if _, err := os.Stat(filepath.Join(s.runRoot, id)); !os.IsNotExist(err) {
			t.Errorf("evicted run %s still on disk: err=%v", id, err)
		}
	}
	// Newest three still present in memory + disk.
	for _, id := range ids[2:] {
		if _, ok := s.GetRun(id, ""); !ok {
			t.Errorf("expected run %s to be present", id)
		}
	}
}

func TestRunnerStoreReloadFromDisk(t *testing.T) {
	s := newTestStore(t)
	r := s.Start(RunnerRun{JobName: "persistent"})
	s.Append(r.ID, "tail content")
	s.Finish(r.ID, 0, false)

	// Build a fresh store. newTestStore set HOME to a tempdir, and
	// t.Setenv survives across calls within the same test, so the
	// fresh store opens the same on-disk root and rehydrates the run
	// via loadRuns() in NewRunnerStore.
	fresh := NewRunnerStore(10)
	loaded, ok := fresh.GetRun(r.ID, "")
	if !ok {
		t.Fatalf("reloaded store lost the run (runRoot=%s)", fresh.runRoot)
	}
	if loaded.JobName != "persistent" {
		t.Errorf("JobName wrong after reload: %q", loaded.JobName)
	}
	if loaded.InProgress {
		t.Errorf("reload should clear InProgress")
	}
}

func TestRunnerStoreGuestFilter(t *testing.T) {
	s := newTestStore(t)
	rA := s.Start(RunnerRun{JobName: "a", TriggeredBy: "guestA"})
	s.Finish(rA.ID, 0, false)
	rB := s.Start(RunnerRun{JobName: "b", TriggeredBy: "guestB"})
	s.Finish(rB.ID, 0, false)
	rOwner := s.Start(RunnerRun{JobName: "c", TriggeredBy: "owner"})
	s.Finish(rOwner.ID, 0, false)

	// Owner sees everything.
	if got := s.ListRuns("", "", 0); len(got) != 3 {
		t.Errorf("owner ListRuns size = %d; want 3", len(got))
	}

	// guestA sees only their own.
	got := s.ListRuns("", "guestA", 0)
	if len(got) != 1 || got[0].JobName != "a" {
		t.Errorf("guestA ListRuns = %#v", got)
	}

	// guestB cannot fetch guestA's run by id.
	if _, ok := s.GetRun(rA.ID, "guestB"); ok {
		t.Error("guestB should not see guestA's run")
	}
	// guestA can fetch their own run by id.
	if _, ok := s.GetRun(rA.ID, "guestA"); !ok {
		t.Error("guestA should see their own run")
	}
}

func TestRunJobShellEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test relies on POSIX sh")
	}
	s := newTestStore(t)
	job := RunnerJob{
		Name:    "echo-test",
		Kind:    RunnerJobShell,
		Command: "echo hello-runner && echo on-stderr 1>&2",
		Pool:    "any",
	}
	if _, err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	final, err := runJobShell(context.Background(), s, job, "owner", false, nil)
	if err != nil {
		t.Fatalf("runJobShell: %v", err)
	}
	if !final.OK || final.ExitCode != 0 {
		t.Errorf("echo job not OK: %+v", final)
	}
	if !strings.Contains(final.OutputTail, "hello-runner") {
		t.Errorf("missing stdout in tail: %q", final.OutputTail)
	}
	if !strings.Contains(final.OutputTail, "on-stderr") {
		t.Errorf("missing stderr in tail: %q", final.OutputTail)
	}
}

func TestRunJobShellNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test relies on POSIX sh")
	}
	s := newTestStore(t)
	job := RunnerJob{Name: "fail", Kind: RunnerJobShell, Command: "exit 7"}
	final, err := runJobShell(context.Background(), s, job, "owner", false, nil)
	if err != nil {
		t.Fatalf("runJobShell: %v", err)
	}
	if final.OK {
		t.Errorf("expected non-OK for exit 7, got %+v", final)
	}
	if final.ExitCode != 7 {
		t.Errorf("expected exit code 7, got %d", final.ExitCode)
	}
}

func TestRunJobShellTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test relies on POSIX sh")
	}
	s := newTestStore(t)
	job := RunnerJob{Name: "slow", Kind: RunnerJobShell, Command: "sleep 5", TimeoutSec: 1}
	start := time.Now()
	final, err := runJobShell(context.Background(), s, job, "owner", false, nil)
	if err != nil {
		t.Fatalf("runJobShell: %v", err)
	}
	if !final.TimedOut {
		t.Errorf("expected TimedOut=true on timeout, got %+v", final)
	}
	if final.OK {
		t.Errorf("timed-out run must not be OK")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestComposeRunnerEnvJobOverridesEverything(t *testing.T) {
	// No vault here — we only assert the explicit job env and the
	// guest sandbox of parent env. Vault overlay is exercised by the
	// vault tests already.
	t.Setenv("MY_ALREADY_SET", "from-parent")
	jobEnv := map[string]string{"MY_JOB_VAR": "from-job", "MY_ALREADY_SET": "from-job"}

	got := composeRunnerEnv(nil, "", jobEnv, false)
	have := map[string]string{}
	for _, kv := range got {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		have[kv[:eq]] = kv[eq+1:]
	}
	if have["MY_JOB_VAR"] != "from-job" {
		t.Errorf("job env not propagated: %q", have["MY_JOB_VAR"])
	}
	if have["MY_ALREADY_SET"] != "from-job" {
		t.Errorf("job env should override parent: %q", have["MY_ALREADY_SET"])
	}
}

func TestComposeRunnerEnvGuestSandboxesParentEnv(t *testing.T) {
	t.Setenv("UNSAFE_TOKEN", "leaky-secret")
	t.Setenv("PATH", "/usr/bin")
	got := composeRunnerEnv(nil, "", nil, true /* isGuest */)
	have := map[string]string{}
	for _, kv := range got {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		have[kv[:eq]] = kv[eq+1:]
	}
	if _, leaked := have["UNSAFE_TOKEN"]; leaked {
		t.Error("guest env must not inherit arbitrary parent vars")
	}
	if have["PATH"] != "/usr/bin" {
		t.Errorf("PATH should be inherited for guests, got %q", have["PATH"])
	}
}

func TestLocalCapabilitiesShape(t *testing.T) {
	caps := LocalCapabilities()
	want := map[string]bool{
		"any":                               true,
		"os:" + runtime.GOOS:                true,
		"arch:" + runtime.GOARCH:            true,
		runtime.GOOS + "-" + runtime.GOARCH: true,
	}
	for _, c := range caps {
		want[c] = true
	}
	for k := range want {
		found := false
		for _, c := range caps {
			if c == k || (strings.HasPrefix(k, "host:") && strings.HasPrefix(c, "host:")) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected capability %q in %v", k, caps)
		}
	}
}
