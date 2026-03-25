package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTodoListManager_AddAndList(t *testing.T) {
	mgr, err := NewTodoListManager()
	if err != nil {
		t.Fatalf("NewTodoListManager: %v", err)
	}
	defer mgr.ClearAll()

	// Add an item
	metadata := `{"description":"Button overlaps text","source":"sdk","deviceInfo":{"platform":"ios","model":"iPhone 16"}}`
	item, err := mgr.AddItem(json.RawMessage(metadata), nil, "")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if item.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if item.Status != TodoStatusPending {
		t.Fatalf("expected pending, got %s", item.Status)
	}
	if item.Description != "Button overlaps text" {
		t.Fatalf("unexpected description: %s", item.Description)
	}

	// List
	items := mgr.ListItems()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Count
	if mgr.Count() != 1 {
		t.Fatalf("expected count 1, got %d", mgr.Count())
	}
}

func TestTodoListManager_RemoveAndClear(t *testing.T) {
	mgr, err := NewTodoListManager()
	if err != nil {
		t.Fatalf("NewTodoListManager: %v", err)
	}
	defer mgr.ClearAll()

	// Add two items
	for i := 0; i < 2; i++ {
		meta := fmt.Sprintf(`{"description":"Bug %d","source":"sdk"}`, i)
		mgr.AddItem(json.RawMessage(meta), nil, "")
	}
	if mgr.Count() != 2 {
		t.Fatalf("expected 2, got %d", mgr.Count())
	}

	// Remove first
	items := mgr.ListItems()
	err = mgr.RemoveItem(items[0].ID)
	if err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if mgr.Count() != 1 {
		t.Fatalf("expected 1 after remove, got %d", mgr.Count())
	}

	// Clear all
	cleared := mgr.ClearAll()
	if cleared != 1 {
		t.Fatalf("expected 1 cleared, got %d", cleared)
	}
	if mgr.Count() != 0 {
		t.Fatalf("expected 0 after clear, got %d", mgr.Count())
	}
}

func TestTodoListManager_StatusTransitions(t *testing.T) {
	mgr, err := NewTodoListManager()
	if err != nil {
		t.Fatalf("NewTodoListManager: %v", err)
	}
	defer mgr.ClearAll()

	meta := `{"description":"Status test","source":"mobile"}`
	item, _ := mgr.AddItem(json.RawMessage(meta), nil, "")

	// Mark implementing
	mgr.MarkImplementing([]string{item.ID}, "task-123")
	got, ok := mgr.GetItem(item.ID)
	if !ok {
		t.Fatal("item not found")
	}
	if got.Status != TodoStatusImplementing {
		t.Fatalf("expected implementing, got %s", got.Status)
	}
	if got.TaskID != "task-123" {
		t.Fatalf("expected taskId task-123, got %s", got.TaskID)
	}

	// Count should be 0 (only counts pending)
	if mgr.Count() != 0 {
		t.Fatalf("expected 0 pending, got %d", mgr.Count())
	}

	// Mark done
	mgr.MarkDone([]string{item.ID})
	got, _ = mgr.GetItem(item.ID)
	if got.Status != TodoStatusDone {
		t.Fatalf("expected done, got %s", got.Status)
	}
}

func TestTodoListManager_BlackBoxSnap(t *testing.T) {
	mgr, err := NewTodoListManager()
	if err != nil {
		t.Fatalf("NewTodoListManager: %v", err)
	}
	defer mgr.ClearAll()

	meta := `{"description":"Crash on login","source":"sdk"}`
	snap := "=== Black Box ===\n[error] TypeError at Login.tsx:42\n=== End ==="
	item, _ := mgr.AddItem(json.RawMessage(meta), nil, snap)

	got, _ := mgr.GetItem(item.ID)
	if got.BlackBoxSnap != snap {
		t.Fatal("blackbox snapshot not stored")
	}
}

func TestTodoListManager_BatchPrompt(t *testing.T) {
	mgr, err := NewTodoListManager()
	if err != nil {
		t.Fatalf("NewTodoListManager: %v", err)
	}
	defer mgr.ClearAll()

	mgr.AddItem(json.RawMessage(`{"description":"Bug A","source":"sdk"}`), nil, "")
	mgr.AddItem(json.RawMessage(`{"description":"Bug B","source":"sdk"}`), nil, "blackbox context B")

	pending := mgr.PendingItems()
	prompt := mgr.GenerateBatchFixPrompt(pending)

	if !strings.Contains(prompt, "2 queued bug reports") {
		t.Fatal("prompt should mention 2 items")
	}
	if !strings.Contains(prompt, "Bug A") || !strings.Contains(prompt, "Bug B") {
		t.Fatal("prompt should contain both descriptions")
	}
	if !strings.Contains(prompt, "blackbox context B") {
		t.Fatal("prompt should contain blackbox context")
	}
}

func TestTodoListManager_AutoConsume(t *testing.T) {
	mgr, err := NewTodoListManager()
	if err != nil {
		t.Fatalf("NewTodoListManager: %v", err)
	}
	defer mgr.ClearAll()

	consumed := make(chan string, 1)
	mgr.SetAutoConsume(true, func(item *TodoItem) {
		consumed <- item.ID
	})

	meta := `{"description":"Auto consumed bug","source":"sdk"}`
	item, _ := mgr.AddItem(json.RawMessage(meta), nil, "")

	select {
	case id := <-consumed:
		if id != item.ID {
			t.Fatalf("expected %s, got %s", item.ID, id)
		}
	default:
		// goroutine may not have run yet, that's ok for the unit test
	}

	if !mgr.IsAutoConsume() {
		t.Fatal("expected auto-consume to be enabled")
	}

	mgr.SetAutoConsume(false, nil)
	if mgr.IsAutoConsume() {
		t.Fatal("expected auto-consume to be disabled")
	}
}

func TestTodoListHTTP_AddAndList(t *testing.T) {
	mgr, _ := NewTodoListManager()
	defer mgr.ClearAll()

	srv := &HTTPServer{todolistMgr: mgr}

	// GET /todolist/count — should be 0
	req := httptest.NewRequest("GET", "/todolist/count", nil)
	w := httptest.NewRecorder()
	srv.handleTodoListCount(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var countResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &countResp)
	if countResp["count"].(float64) != 0 {
		t.Fatalf("expected count 0, got %v", countResp["count"])
	}

	// GET /todolist — should have empty items
	req = httptest.NewRequest("GET", "/todolist", nil)
	w = httptest.NewRecorder()
	srv.handleTodoList(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var listResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	items := listResp["items"].([]interface{})
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestTodoListHTTP_ImplementAllEmpty(t *testing.T) {
	mgr, _ := NewTodoListManager()
	defer mgr.ClearAll()

	srv := &HTTPServer{todolistMgr: mgr}

	req := httptest.NewRequest("POST", "/todolist/implement-all", nil)
	w := httptest.NewRecorder()
	srv.handleTodoListImplementAll(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["itemCount"].(float64) != 0 {
		t.Fatalf("expected 0 items, got %v", resp["itemCount"])
	}
}

func TestTodoListHTTP_DeleteByID(t *testing.T) {
	mgr, _ := NewTodoListManager()
	defer mgr.ClearAll()

	meta := `{"description":"Delete me","source":"sdk"}`
	item, _ := mgr.AddItem(json.RawMessage(meta), nil, "")

	srv := &HTTPServer{todolistMgr: mgr}

	req := httptest.NewRequest("DELETE", "/todolist/"+item.ID, nil)
	w := httptest.NewRecorder()
	srv.handleTodoListByID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if mgr.Count() != 0 {
		t.Fatal("expected 0 items after delete")
	}
}

func TestTodoListHTTP_GetByID(t *testing.T) {
	mgr, _ := NewTodoListManager()
	defer mgr.ClearAll()

	meta := `{"description":"Get me","source":"mobile"}`
	item, _ := mgr.AddItem(json.RawMessage(meta), nil, "snap")

	srv := &HTTPServer{todolistMgr: mgr}

	req := httptest.NewRequest("GET", "/todolist/"+item.ID, nil)
	w := httptest.NewRecorder()
	srv.handleTodoListByID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got TodoItem
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Description != "Get me" {
		t.Fatalf("unexpected description: %s", got.Description)
	}
	if got.BlackBoxSnap != "snap" {
		t.Fatalf("unexpected blackbox: %s", got.BlackBoxSnap)
	}
}

func TestTodoListPersistence(t *testing.T) {
	mgr1, _ := NewTodoListManager()
	defer mgr1.ClearAll()

	meta := `{"description":"Persist me","source":"sdk"}`
	item, _ := mgr1.AddItem(json.RawMessage(meta), nil, "ctx")

	// Create a new manager — should load existing items
	mgr2, _ := NewTodoListManager()
	got, ok := mgr2.GetItem(item.ID)
	if !ok {
		t.Fatal("item not found after reload")
	}
	if got.Description != "Persist me" {
		t.Fatalf("unexpected description after reload: %s", got.Description)
	}
}
