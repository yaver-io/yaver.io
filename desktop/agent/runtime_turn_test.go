package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withIsolatedRuntimeQueue points the queue at a temp file for the duration of
// one test. Without this the store would persist into the developer's REAL
// ~/.yaver/runtime-queue.json — the same class of accident that makes a broad
// `go test` in this package clobber live agent state.
func withIsolatedRuntimeQueue(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime-queue.json")
	prev := runtimeQueue
	runtimeQueue = &runtimeQueueStore{
		items:  make(map[string]*RuntimeTurnQueueItem),
		path:   path,
		loaded: true, // nothing to hydrate; never look at $HOME
	}
	t.Cleanup(func() { runtimeQueue = prev })
	return path
}

func TestRuntimeTurnCapturesIdeaWithoutRunningByDefault(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	resp := executeRuntimeTurn(OpsContext{}, RuntimeTurnRequest{
		Utterance: "idea: make the disconnected screen show the failed probe",
		Surface:   RuntimeTurnSurface{Class: "watch"},
	})
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.State != runtimeQueueStateCaptured {
		t.Fatalf("state = %q, want captured", resp.State)
	}
	if resp.Queue == nil || resp.Queue.ItemID == "" {
		t.Fatalf("missing queue item: %+v", resp)
	}
	if resp.Queue.IntentClass != "idea-capture" {
		t.Fatalf("intent = %q, want idea-capture", resp.Queue.IntentClass)
	}
}

func TestRuntimeTurnCreatesQueuedTaskWhenAskedToRun(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance: "fix the startup flicker",
		Surface:   RuntimeTurnSurface{Class: "watch"},
		Development: RuntimeTurnDevelopment{
			Queue: RuntimeTurnQueuePrefs{Mode: "enqueue-or-run"},
		},
	})
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task-backed queue item: %+v", resp)
	}
	if resp.State != runtimeQueueStateRunning {
		t.Fatalf("state = %q, want running", resp.State)
	}
	if _, ok := tm.GetTask(resp.Queue.TaskID); !ok {
		t.Fatalf("task %q not found", resp.Queue.TaskID)
	}
}

func TestRuntimeTurnEnqueuesIdeaAsTaskWhenRequested(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance: "idea: make the disconnected screen show the failed probe",
		Surface:   RuntimeTurnSurface{Class: "watch"},
		Development: RuntimeTurnDevelopment{
			Queue: RuntimeTurnQueuePrefs{Mode: "enqueue"},
		},
	})
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task-backed queue item: %+v", resp)
	}
	if resp.Queue.IntentClass != "idea-capture" {
		t.Fatalf("intent = %q, want idea-capture", resp.Queue.IntentClass)
	}
	task, ok := tm.GetTask(resp.Queue.TaskID)
	if !ok {
		t.Fatalf("task %q not found", resp.Queue.TaskID)
	}
	if !strings.Contains(task.Description, "Treat this as idea capture first") {
		t.Fatalf("task prompt did not preserve idea-capture mode:\n%s", task.Description)
	}
}

func TestRuntimeTurnStatusMapsCompletedTaskToReadyToTest(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance: "fix the startup flicker",
		Development: RuntimeTurnDevelopment{
			Queue: RuntimeTurnQueuePrefs{Mode: "enqueue-or-run"},
		},
	})
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task-backed queue item: %+v", resp)
	}
	task, ok := tm.GetTask(resp.Queue.TaskID)
	if !ok {
		t.Fatalf("task not found")
	}
	task.Status = TaskStatusFinished
	task.ResultText = "Done. Tests pass."

	status := runtimeTurnStatus(OpsContext{Server: server}, resp.Queue.ItemID)
	if !status.OK {
		t.Fatalf("status failed: %+v", status)
	}
	if status.State != runtimeQueueStateReadyToTest {
		t.Fatalf("state = %q, want ready_to_test", status.State)
	}
	if status.TestTarget == nil || status.TestTarget.Kind != "yaver-mobile-container" {
		t.Fatalf("missing test target: %+v", status.TestTarget)
	}
}

// A finished task means code changed. It does NOT mean anything reloaded on a
// phone. Selling that as test-ready is the inventory-vs-operation trap, so the
// default must stay "unverified" until a reload is actually attempted.
func TestRuntimeTurnReadyToTestIsUnverifiedUntilReloadAttempted(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance:   "fix the startup flicker",
		Development: RuntimeTurnDevelopment{Queue: RuntimeTurnQueuePrefs{Mode: "run"}},
	})
	task, _ := tm.GetTask(resp.Queue.TaskID)
	task.Status = TaskStatusFinished

	status := runtimeTurnStatus(OpsContext{Server: server}, resp.Queue.ItemID)
	if status.TestTarget.State != "unverified" {
		t.Fatalf("testTarget.state = %q, want unverified", status.TestTarget.State)
	}
	if strings.Contains(strings.ToLower(status.Spoken), "you can test it") {
		t.Fatalf("spoken line still claims test-readiness: %q", status.Spoken)
	}
}

// The whole point of verify: report the REAL delivery result. A phone that
// registered a session but is not holding the command stream must count as
// unreachable, not as success.
func TestRuntimeTurnVerifyReportsUnreachableWhenNothingIsListening(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	mgr.GetOrCreateSession("dev-silent", "ios", "AppUnderTest") // registered, not listening
	server := &HTTPServer{blackboxMgr: mgr}

	item := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "x", State: runtimeQueueStateReadyToTest})
	resp := runtimeTurnVerify(OpsContext{Server: server}, item.ItemID)
	if resp.OK {
		t.Fatalf("verify reported success with zero listeners: %+v", resp)
	}
	if resp.TestTarget == nil || resp.TestTarget.State != "unreachable" {
		t.Fatalf("testTarget = %+v, want state=unreachable", resp.TestTarget)
	}
	if resp.TestTarget.Listeners != 0 {
		t.Fatalf("listeners = %d, want 0", resp.TestTarget.Listeners)
	}
}

func TestRuntimeTurnVerifyReportsDeliveredWhenADeviceIsListening(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	dev := mgr.GetOrCreateSession("dev-live", "ios", "AppUnderTest")
	ch := dev.SubscribeCommands()
	defer dev.UnsubscribeCommands(ch)
	server := &HTTPServer{blackboxMgr: mgr}

	item := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "x", State: runtimeQueueStateReadyToTest})
	resp := runtimeTurnVerify(OpsContext{Server: server}, item.ItemID)
	if !resp.OK {
		t.Fatalf("verify failed with a live listener: %+v", resp)
	}
	if resp.TestTarget.State != "delivered" || resp.TestTarget.Listeners != 1 {
		t.Fatalf("testTarget = %+v, want delivered/1", resp.TestTarget)
	}
}

// `captured` used to be a black hole: nothing could ever move an item out of
// it, so "I'll attach it to the current app" was never true.
func TestRuntimeTurnRunPromotesCapturedIdeaKeepingTheSameTurnID(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	c := OpsContext{Ctx: context.Background(), Server: server}

	captured := executeRuntimeTurn(c, RuntimeTurnRequest{
		Utterance: "idea: show the failed probe on the disconnected screen",
		Surface:   RuntimeTurnSurface{Class: "watch"},
	})
	if captured.State != runtimeQueueStateCaptured {
		t.Fatalf("state = %q, want captured", captured.State)
	}

	ran := runtimeTurnRun(c, captured.Queue.ItemID)
	if !ran.OK {
		t.Fatalf("promote failed: %+v", ran)
	}
	if ran.TurnID != captured.TurnID {
		t.Fatalf("turnId changed on promote: %q -> %q (surfaces would show a duplicate)", captured.TurnID, ran.TurnID)
	}
	if ran.Queue.TaskID == "" {
		t.Fatalf("promote did not create a task: %+v", ran.Queue)
	}
	if ran.State != runtimeQueueStateRunning {
		t.Fatalf("state = %q, want running", ran.State)
	}
	// A promoted idea must take the coding path, not stay capture-only.
	task, _ := tm.GetTask(ran.Queue.TaskID)
	if strings.Contains(task.Description, "Do not edit code") {
		t.Fatalf("promoted idea still carries capture-only instructions:\n%s", task.Description)
	}
}

func TestRuntimeTurnRunIsIdempotentForAlreadyStartedWork(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	c := OpsContext{Ctx: context.Background(), Server: &HTTPServer{taskMgr: tm}}

	started := executeRuntimeTurn(c, RuntimeTurnRequest{
		Utterance:   "fix the startup flicker",
		Development: RuntimeTurnDevelopment{Queue: RuntimeTurnQueuePrefs{Mode: "run"}},
	})
	again := runtimeTurnRun(c, started.Queue.ItemID)
	if again.Queue.TaskID != started.Queue.TaskID {
		t.Fatalf("second run forked a new task: %q -> %q", started.Queue.TaskID, again.Queue.TaskID)
	}
}

func TestRuntimeTurnRejectsEmptyInput(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	resp := executeRuntimeTurn(OpsContext{}, RuntimeTurnRequest{Utterance: "   "})
	if resp.OK {
		t.Fatalf("empty input unexpectedly succeeded: %+v", resp)
	}
	if resp.Code != "bad_payload" {
		t.Fatalf("code = %q, want bad_payload", resp.Code)
	}
}

func TestRuntimeTurnNormalizesSimplePayloadAliases(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	req := RuntimeTurnRequest{
		Text:    "fix the login button",
		Run:     true,
		Surface: RuntimeTurnSurface{ID: "watch"},
	}
	normalizeRuntimeTurnRequest(&req)
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, req)
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task from text/run aliases: %+v", resp.Queue)
	}
	if resp.Queue.Utterance != "fix the login button" {
		t.Fatalf("utterance = %q", resp.Queue.Utterance)
	}
	if resp.Queue.Surface.Class != "watch" {
		t.Fatalf("surface class = %q, want watch", resp.Queue.Surface.Class)
	}
}

func TestRuntimeTurnsListsRecentItems(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	a := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "first", State: runtimeQueueStateCaptured})
	time.Sleep(time.Millisecond)
	b := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "second", State: runtimeQueueStateRunning})

	items := runtimeQueue.list("", 10)
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].ItemID != b.ItemID || items[1].ItemID != a.ItemID {
		t.Fatalf("items not newest-first: %+v", items)
	}
}

// A shared agent must never render one tenant's spoken utterances to another.
func TestRuntimeQueueScopesItemsToTheirOwner(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	mine := runtimeQueue.add(&RuntimeTurnQueueItem{OwnerUserID: "user-a", Utterance: "my secret idea"})
	theirs := runtimeQueue.add(&RuntimeTurnQueueItem{OwnerUserID: "user-b", Utterance: "their idea"})

	items := runtimeQueue.list("user-a", 10)
	if len(items) != 1 || items[0].ItemID != mine.ItemID {
		t.Fatalf("owner filter leaked: %+v", items)
	}
	// Cross-owner get must be indistinguishable from not-found so a caller
	// cannot probe which turn IDs exist on the box.
	if _, ok := runtimeQueue.get("user-a", theirs.ItemID); ok {
		t.Fatalf("user-a read user-b's item %q", theirs.ItemID)
	}
}

// The list's sort key is UpdatedAt. A refresh that changes nothing must not
// touch it, or a polling phone reshuffles the list on every tick and captured
// items sink out of view forever.
func TestRuntimeQueueNoOpRefreshDoesNotReorderTheList(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	older := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "older", State: runtimeQueueStateCaptured})
	time.Sleep(time.Millisecond)
	runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "newer", State: runtimeQueueStateCaptured})

	before, _ := runtimeQueue.get("", older.ItemID)
	// A refresh that assigns identical values — exactly what polling does.
	runtimeQueue.update(older.ItemID, func(i *RuntimeTurnQueueItem) {
		i.State = runtimeQueueStateCaptured
	})
	after, _ := runtimeQueue.get("", older.ItemID)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("no-op refresh bumped UpdatedAt: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}
	if items := runtimeQueue.list("", 10); items[0].Utterance != "newer" {
		t.Fatalf("list order churned; head = %q, want newer", items[0].Utterance)
	}

	// A real change still advances the clock and moves the item to the head.
	runtimeQueue.update(older.ItemID, func(i *RuntimeTurnQueueItem) {
		i.State = runtimeQueueStateRunning
	})
	if items := runtimeQueue.list("", 10); items[0].Utterance != "older" {
		t.Fatalf("real change did not resort; head = %q", items[0].Utterance)
	}
}

// An idea spoken into a watch has to survive the agent restarting.
func TestRuntimeQueueSurvivesRestart(t *testing.T) {
	path := withIsolatedRuntimeQueue(t)
	captured := runtimeQueue.add(&RuntimeTurnQueueItem{
		OwnerUserID: "user-a", Utterance: "idea worth keeping", State: runtimeQueueStateCaptured,
	})
	inflight := runtimeQueue.add(&RuntimeTurnQueueItem{
		OwnerUserID: "user-a", Utterance: "was running", State: runtimeQueueStateRunning,
	})

	// Simulate a restart: brand-new store over the same file.
	runtimeQueue = &runtimeQueueStore{items: make(map[string]*RuntimeTurnQueueItem), path: path}

	got, ok := runtimeQueue.get("user-a", captured.ItemID)
	if !ok {
		t.Fatalf("captured idea did not survive restart")
	}
	if got.Utterance != "idea worth keeping" || got.State != runtimeQueueStateCaptured {
		t.Fatalf("captured item came back wrong: %+v", got)
	}
	// Work cannot still be running after the process died — showing a spinner
	// forever is a lie the user can't act on.
	revived, _ := runtimeQueue.get("user-a", inflight.ItemID)
	if revived.State != runtimeQueueStateQueued {
		t.Fatalf("in-flight item state = %q, want queued after restart", revived.State)
	}
}

func TestRuntimeQueueIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool, 2000)
	for i := 0; i < 2000; i++ {
		id := newRuntimeQueueID()
		if seen[id] {
			t.Fatalf("duplicate queue id %q — a collision silently overwrites queued work", id)
		}
		seen[id] = true
	}
}

func TestRuntimeQueueEvictsTerminalItemsFirst(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	live := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "live work", State: runtimeQueueStateRunning})
	for i := 0; i < runtimeQueueMaxItems+10; i++ {
		runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "old", State: runtimeQueueStateDone})
	}
	if _, ok := runtimeQueue.get("", live.ItemID); !ok {
		t.Fatalf("eviction dropped in-flight work while terminal items remained")
	}
	if n := len(runtimeQueue.list("", 100)); n > 100 {
		t.Fatalf("list returned %d items, over the cap", n)
	}
}
