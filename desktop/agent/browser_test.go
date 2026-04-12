package main

import (
	"testing"
	"time"
)

func TestBrowserManagerLifecycle(t *testing.T) {
	bm := NewBrowserManager()
	defer bm.Stop()

	// List should be empty initially.
	sessions := bm.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}

	// Open a headless session.
	err := bm.OpenSession("test-1", false)
	if err != nil {
		t.Skipf("Chrome not available, skipping: %v", err)
	}

	// List should have 1 session.
	sessions = bm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "test-1" {
		t.Fatalf("expected session ID 'test-1', got %q", sessions[0].ID)
	}

	// Duplicate open should fail.
	err = bm.OpenSession("test-1", false)
	if err == nil {
		t.Fatal("expected error for duplicate session ID")
	}

	// Navigate to a data URL (no network needed).
	result, err := bm.Navigate("test-1", "data:text/html,<h1>Hello Yaver</h1>")
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if result.ScreenshotB64 == "" {
		t.Fatal("expected non-empty screenshot")
	}
	t.Logf("Navigate screenshot: %d bytes base64", len(result.ScreenshotB64))

	// Extract text.
	text, err := bm.ExtractText("test-1", "h1")
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	if text != "Hello Yaver" {
		t.Fatalf("expected 'Hello Yaver', got %q", text)
	}

	// Screenshot.
	shot, err := bm.Screenshot("test-1")
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if shot.ScreenshotB64 == "" {
		t.Fatal("expected non-empty screenshot")
	}

	// Get DOM.
	html, err := bm.GetDOM("test-1")
	if err != nil {
		t.Fatalf("get DOM: %v", err)
	}
	if html == "" {
		t.Fatal("expected non-empty DOM")
	}
	t.Logf("DOM length: %d chars", len(html))

	// Evaluate JS.
	evalResult, err := bm.Evaluate("test-1", "1 + 2")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evalResult != float64(3) {
		t.Fatalf("expected 3, got %v", evalResult)
	}

	// Navigate to a page with an input and test typing.
	_, err = bm.Navigate("test-1", `data:text/html,<input id="name" type="text"><button id="btn">Click</button>`)
	if err != nil {
		t.Fatalf("navigate to form: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Type into input.
	typeResult, err := bm.Type("test-1", "#name", "Yaver Test", false)
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	if typeResult.ScreenshotB64 == "" {
		t.Fatal("expected screenshot after typing")
	}

	// Click button.
	clickResult, err := bm.Click("test-1", "#btn")
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if clickResult.ScreenshotB64 == "" {
		t.Fatal("expected screenshot after click")
	}

	// Scroll.
	scrollResult, err := bm.Scroll("test-1", 0, 100)
	if err != nil {
		t.Fatalf("scroll: %v", err)
	}
	if scrollResult.ScreenshotB64 == "" {
		t.Fatal("expected screenshot after scroll")
	}

	// WaitFor on existing element.
	err = bm.WaitFor("test-1", "#btn", 2000)
	if err != nil {
		t.Fatalf("wait for: %v", err)
	}

	// Extract attribute.
	attrVal, err := bm.ExtractAttribute("test-1", "#name", "type")
	if err != nil {
		t.Fatalf("extract attribute: %v", err)
	}
	if attrVal != "text" {
		t.Fatalf("expected attribute 'text', got %q", attrVal)
	}

	// Close.
	err = bm.CloseSession("test-1")
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Verify closed.
	sessions = bm.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after close, got %d", len(sessions))
	}

	// Operations on closed session should fail.
	_, err = bm.Navigate("test-1", "https://example.com")
	if err == nil {
		t.Fatal("expected error for closed session")
	}
}

func TestBrowserManagerNonexistentSession(t *testing.T) {
	bm := NewBrowserManager()
	defer bm.Stop()

	_, err := bm.Navigate("does-not-exist", "https://example.com")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}

	_, err = bm.Click("does-not-exist", "#foo")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}

	_, err = bm.Screenshot("does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}

	err = bm.CloseSession("does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}
