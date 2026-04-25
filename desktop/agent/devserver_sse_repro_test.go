package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// HTTP-level repro of the bug seen in the screen recording. Boots the
// real /dev/events SSE handler in-process, fires EmitLog at well-defined
// timing relative to subscriber arrival, and asserts what each subscriber
// receives. This is the "headless agent" version of what the browser
// dashboard does — same SSE contract.

func TestDevEventsSSE_LateSubscriberGetsReplayedBanner(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Wire a DevServerManager into the HTTP server (startTestServer
	// doesn't do this by default). This is the same field /dev/events
	// reads from in production.
	currentTestHTTPServer.devServerMgr = NewDevServerManager()

	// Simulate Metro emitting its banner ~immediately after spawn,
	// before the dashboard has had a chance to open the SSE.
	// In production this race window is ~1–2s while React mounts
	// + agentClient.connect() resolves baseUrl.
	currentTestHTTPServer.devServerMgr.EmitLog("› Metro waiting on http://0.0.0.0:8082")
	currentTestHTTPServer.devServerMgr.EmitLog("› Logs for your project will appear below")
	currentTestHTTPServer.devServerMgr.EmitLog("› Bundling: index.bundle 100%")

	// Now the browser opens the SSE. With the ring-buffer fix in
	// place the replay is delivered immediately; bound the wait
	// loosely so a buggy build fails fast.
	ctx, ssecCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer ssecCancel()

	got := streamDevEventsHTTP(t, ctx, baseURL, "tok")

	logCount := 0
	for _, ev := range got {
		if ev.Type == "log" {
			logCount++
		}
	}
	if logCount != 3 {
		t.Fatalf("late subscriber got %d/3 replayed log events — ring buffer or SSE plumbing broken; raw events=%d", logCount, len(got))
	}
	t.Logf("CONFIRMED via /dev/events SSE: late subscriber receives 3/3 banner lines via replay (CONSOLE pane will populate immediately)")
}

func TestDevEventsSSE_EarlySubscriberSeesEverything(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	currentTestHTTPServer.devServerMgr = NewDevServerManager()

	// Open the SSE first (this is what the dashboard SHOULD do —
	// it is what mobile does because it subscribes from a
	// connected-state effect, not from useEffect with deps `[]`).
	ctx, ssecCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer ssecCancel()

	collected := make(chan []DevServerEvent, 1)
	go func() {
		collected <- streamDevEventsHTTP(t, ctx, baseURL, "tok")
	}()

	// Give the SSE handshake a moment, then emit.
	time.Sleep(150 * time.Millisecond)
	currentTestHTTPServer.devServerMgr.EmitLog("line 1")
	currentTestHTTPServer.devServerMgr.EmitLog("line 2")
	currentTestHTTPServer.devServerMgr.EmitLog("line 3")

	got := <-collected
	logCount := 0
	for _, ev := range got {
		if ev.Type == "log" {
			logCount++
		}
	}
	if logCount != 3 {
		t.Fatalf("early subscriber: got %d log events, want 3 — pipeline broken", logCount)
	}
	t.Logf("CONFIRMED: early subscriber receives 3/3 log events via /dev/events")
}

// streamDevEventsHTTP opens a real GET /dev/events and returns every
// DevServerEvent it parses. Returns when ctx expires. Also returns
// early if the stream goes idle for idleGrace after delivering any
// events (so the replay-only late-subscriber test doesn't have to
// wait the full ctx deadline).
func streamDevEventsHTTP(t *testing.T, ctx context.Context, baseURL, token string) []DevServerEvent {
	t.Helper()

	connCtx, connCancel := context.WithCancel(context.Background())
	defer connCancel()
	req, err := http.NewRequestWithContext(connCtx, "GET", baseURL+"/dev/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		t.Fatalf("GET /dev/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /dev/events: status %d", resp.StatusCode)
	}

	type lineMsg struct {
		s   string
		err error
	}
	lines := make(chan lineMsg, 32)
	reader := bufio.NewReader(resp.Body)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			lines <- lineMsg{s: line, err: err}
			if err != nil {
				return
			}
		}
	}()

	var (
		events  []DevServerEvent
		dataBuf strings.Builder
	)
	const idleGrace = 250 * time.Millisecond
	idleTimer := time.NewTimer(idleGrace)
	defer idleTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			connCancel()
			return events
		case <-idleTimer.C:
			if len(events) > 0 {
				connCancel()
				return events
			}
			idleTimer.Reset(idleGrace)
		case msg := <-lines:
			if msg.err != nil {
				connCancel()
				return events
			}
			line := strings.TrimRight(msg.s, "\r\n")
			if line == "" {
				if dataBuf.Len() > 0 {
					var ev DevServerEvent
					if jerr := decodeDevServerEvent([]byte(dataBuf.String()), &ev); jerr == nil {
						events = append(events, ev)
					}
					dataBuf.Reset()
				}
			} else if strings.HasPrefix(line, "data:") {
				dataBuf.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleGrace)
		}
	}
}

func decodeDevServerEvent(b []byte, out *DevServerEvent) error {
	return json.Unmarshal(b, out)
}
