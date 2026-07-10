package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A turn carries exactly one intent. "text plus choice" is ambiguous, and
// neither is nothing to do — both must be refused before we touch a pane.
func TestRunnerSessionTurnRejectsAmbiguousIntent(t *testing.T) {
	srv := &HTTPServer{}
	cases := map[string]string{
		"neither text nor choice": `{"session":"yaver-codex"}`,
		"both at once":            `{"session":"yaver-codex","text":"go","choice":"1"}`,
		"choice is not a number":  `{"session":"yaver-codex","choice":"yes"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/runner/session/turn", strings.NewReader(body))
			srv.handleRunnerSessionTurn(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, body)
			}
		})
	}
}

func TestRunnerSessionTurnRejectsNonPost(t *testing.T) {
	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	srv.handleRunnerSessionTurn(rec, httptest.NewRequest(http.MethodGet, "/runner/session/turn", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// A voice surface says "the ubuntu session" and means "the only one". When it
// is not the only one, guessing would type a prompt into the wrong agent — so
// the caller is told to name it.
func TestResolveRunnerSessionErrors(t *testing.T) {
	// No tmux sessions exist under `go test`, so every lookup must fail with a
	// message that tells the caller what to do rather than a bare "not found".
	if _, _, err := resolveRunnerSession("", ""); err == nil {
		t.Fatal("expected an error when no sessions are live")
	} else if !strings.Contains(err.Error(), "no live runner sessions") {
		t.Errorf("unhelpful error: %v", err)
	}

	_, _, err := resolveRunnerSession("", "codex")
	if err == nil || !strings.Contains(err.Error(), "yaver codex --machine") {
		t.Errorf("a missing runner session must suggest how to start one, got %v", err)
	}

	_, _, err = resolveRunnerSession("yaver-nope", "")
	if err == nil || !strings.Contains(err.Error(), "no live session named") {
		t.Errorf("an unknown session name must say so, got %v", err)
	}
}

// Hostile session names must not reach `tmux -t`.
func TestResolveRunnerSessionSanitizesName(t *testing.T) {
	if _, _, err := resolveRunnerSession("../../etc/passwd", ""); err == nil {
		t.Fatal("expected an error for a hostile session name")
	}
	// A name that sanitizes to empty falls through to runner/single resolution
	// rather than being passed to tmux verbatim.
	if _, _, err := resolveRunnerSession("$(rm -rf /)", ""); err == nil {
		t.Fatal("expected an error, never a shell-expandable target")
	}
}

func TestCapturePaneTailOnMissingSession(t *testing.T) {
	if got := capturePaneTail("definitely-not-a-session", 5); got != "" {
		t.Errorf("a missing pane must yield empty, got %q", got)
	}
}

// The wire shape is a contract for six clients (watchOS, Wear OS, CarPlay,
// Android Auto, tvOS, Android TV). Pin the field names.
func TestRunnerSessionTurnResponseShape(t *testing.T) {
	body, err := json.Marshal(runnerSessionTurnResponse{
		OK: true, Session: "yaver-codex", Runner: "codex", Sent: "prompt",
		AwaitingChoice: true, Options: []string{"1. Yes"}, Pane: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"ok"`, `"session"`, `"runner"`, `"sent"`, `"awaitingChoice"`, `"options"`, `"pane"`} {
		if !strings.Contains(string(body), key) {
			t.Errorf("response is missing %s: %s", key, body)
		}
	}
}

// The prompt guard and the choice guard must be symmetric. A digit sent at a
// prompt is not harmless: tmux types it into the composer as literal text, and
// it silently prefixes the next thing the user says ("2reply with exactly ...").
// Observed against claude on a real box.
func TestRunnerSessionTurnChoiceIsAValidIntent(t *testing.T) {
	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/runner/session/turn",
		strings.NewReader(`{"session":"yaver-codex","choice":"2"}`))
	srv.handleRunnerSessionTurn(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("a bare choice is well-formed; it must be rejected on pane state, not parsing (got 400: %s)", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no such live session under go test)", rec.Code)
	}
}
