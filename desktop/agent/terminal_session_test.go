package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTerminalSessionResumeAfterReconnect(t *testing.T) {
	srv := &HTTPServer{
		token:       "owner-token",
		ownerUserID: "owner-user",
	}
	server := httptest.NewServer(http.HandlerFunc(srv.auth(srv.handleTerminalWS)))
	defer server.Close()

	baseURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/terminal?shell=" + url.QueryEscape("/bin/sh")
	header := http.Header{}
	header.Set("Authorization", "Bearer owner-token")

	conn1, _, err := websocket.DefaultDialer.Dial(baseURL, header)
	if err != nil {
		t.Fatalf("dial first terminal session: %v", err)
	}

	sessionID, resumed := readTerminalSessionMeta(t, conn1)
	if resumed {
		t.Fatalf("first terminal session unexpectedly resumed")
	}
	if sessionID == "" {
		t.Fatalf("expected terminal session id")
	}

	if err := conn1.WriteMessage(websocket.BinaryMessage, []byte("echo __TERM_RESUME_ONE__\n")); err != nil {
		t.Fatalf("write first terminal command: %v", err)
	}
	if !readTerminalOutputContains(t, conn1, "__TERM_RESUME_ONE__") {
		t.Fatalf("expected first terminal output marker before reconnect")
	}
	_ = conn1.Close()

	time.Sleep(200 * time.Millisecond)

	resumeURL := baseURL + "&session_id=" + url.QueryEscape(sessionID)
	conn2, _, err := websocket.DefaultDialer.Dial(resumeURL, header)
	if err != nil {
		t.Fatalf("dial resumed terminal session: %v", err)
	}
	defer conn2.Close()

	resumedSessionID, resumed := readTerminalSessionMeta(t, conn2)
	if !resumed {
		t.Fatalf("expected resumed terminal session metadata")
	}
	if resumedSessionID != sessionID {
		t.Fatalf("resumed session id = %q, want %q", resumedSessionID, sessionID)
	}
	if !readTerminalOutputContains(t, conn2, "__TERM_RESUME_ONE__") {
		t.Fatalf("expected replay output after reconnect")
	}

	if err := conn2.WriteMessage(websocket.BinaryMessage, []byte("echo __TERM_RESUME_TWO__\nexit\n")); err != nil {
		t.Fatalf("write resumed terminal command: %v", err)
	}
	if !readTerminalOutputContains(t, conn2, "__TERM_RESUME_TWO__") {
		t.Fatalf("expected second terminal output marker after reconnect")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.terminalSessionByID(sessionID); !ok {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("terminal session %s still registered after shell exit", sessionID)
}

func readTerminalSessionMeta(t *testing.T, conn *websocket.Conn) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
		mt, payload, err := conn.ReadMessage()
		if err != nil {
			continue
		}
		if mt != websocket.TextMessage {
			continue
		}
		var frame struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Resumed   bool   `json:"resumed"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			continue
		}
		if frame.Type == "terminal_session" {
			return frame.SessionID, frame.Resumed
		}
	}
	t.Fatalf("timed out waiting for terminal session metadata")
	return "", false
}

func readTerminalOutputContains(t *testing.T, conn *websocket.Conn, needle string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
		mt, payload, err := conn.ReadMessage()
		if err != nil {
			continue
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		seen.Write(payload)
		if strings.Contains(seen.String(), needle) {
			return true
		}
	}
	return false
}
