package main

import "testing"

// Command-card events (command_events.go) must stay P2P-only: they ride
// Task.eventCh and the task SSE stream, never a Convex mutation. This
// pins two invariants so a future "nice" sync path can't quietly leak
// shell commands / cwd / output into Convex.

func drainOneEvent(t *testing.T, ch chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	default:
		t.Fatal("expected an event on eventCh, got none")
		return nil
	}
}

func TestCommandEvents_GoToEventChOnly(t *testing.T) {
	task := &Task{eventCh: make(chan map[string]interface{}, 8)}

	emitCommandStart(task, "t1-c1", "git push origin main", []string{"origin", "main"}, "/Users/someone/proj", "claude")
	emitCommandOutput(task, "t1-c1", "stdout", "Enumerating objects: 5\n", 0)
	emitCommandEnd(task, "t1-c1", 0, true, 1234, false)

	start := drainOneEvent(t, task.eventCh)
	if start["type"] != "command_start" || start["command"] != "git push origin main" {
		t.Fatalf("bad command_start: %#v", start)
	}
	if start["schema"] != CommandEventSchema {
		t.Fatalf("command_start missing/!= schema %d: %#v", CommandEventSchema, start)
	}
	out := drainOneEvent(t, task.eventCh)
	if out["type"] != "command_output" || out["stream"] != "stdout" {
		t.Fatalf("bad command_output: %#v", out)
	}
	end := drainOneEvent(t, task.eventCh)
	if end["type"] != "command_end" || end["exitCode"] != 0 {
		t.Fatalf("bad command_end: %#v", end)
	}

	// Guard rails: empty/nil inputs are no-ops (no panic, no event).
	emitCommandStart(task, "", "x", nil, "", "")
	emitCommandOutput(task, "t1-c1", "stdout", "", 1)
	emitCommandEnd(nil, "x", 0, true, 0, false)
	select {
	case ev := <-task.eventCh:
		t.Fatalf("expected no event from guard-rail calls, got %#v", ev)
	default:
	}
}

// exitKnown=false must omit exitCode (client renders neutral "done"),
// and durationMs<=0 must be omitted (unknown), so the card doesn't show
// a misleading success/0ms.
func TestCommandEnd_UnknownExitOmitsFields(t *testing.T) {
	task := &Task{eventCh: make(chan map[string]interface{}, 4)}
	emitCommandEnd(task, "c", 0, false, 0, false)
	end := drainOneEvent(t, task.eventCh)
	if _, ok := end["exitCode"]; ok {
		t.Fatalf("exitKnown=false must omit exitCode: %#v", end)
	}
	if _, ok := end["durationMs"]; ok {
		t.Fatalf("durationMs<=0 must be omitted: %#v", end)
	}
}

// The forbidden-key walker (TestConvex... in convex_privacy_test.go)
// only protects us if the command-event keys are actually on the list.
// This fails loudly if someone removes them.
func TestCommandEventKeys_AreConvexForbidden(t *testing.T) {
	want := []string{"command", "cwd", "chunk", "stdout", "stderr", "output"}
	have := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		have[k] = true
	}
	for _, k := range want {
		if !have[k] {
			t.Errorf("command-event key %q must be in fieldsWeForbidInAnyConvexPayload "+
				"so a Convex leak trips the walker", k)
		}
	}
}
