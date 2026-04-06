package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// handleBlackBoxStream handles POST /blackbox/stream — persistent SSE connection
// from a device SDK that streams all app events (logs, errors, navigation, etc.)
// in real-time. This is the "flight recorder" ingest endpoint.
//
// Request body: newline-delimited JSON events.
// Response: SSE stream for bidirectional communication (agent can push commands back).
func (s *HTTPServer) handleBlackBoxStream(w http.ResponseWriter, r *http.Request) {
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "blackbox not available"})
		return
	}

	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Device identification from headers
	deviceID := r.Header.Get("X-Device-ID")
	platform := r.Header.Get("X-Platform")
	appName := r.Header.Get("X-App-Name")
	if deviceID == "" {
		deviceID = "unknown"
	}

	session := s.blackboxMgr.GetOrCreateSession(deviceID, platform, appName)

	// Set up SSE response for bidirectional communication
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Send initial ack
	fmt.Fprintf(w, "data: {\"type\":\"connected\",\"message\":\"Black box streaming active for %s\"}\n\n", deviceID)
	flusher.Flush()

	log.Printf("[blackbox] Device %s (%s/%s) connected — streaming started", deviceID, platform, appName)

	// Subscribe to commands so the agent can push reload/etc. to this SDK device
	cmdCh := session.SubscribeCommands()
	defer session.UnsubscribeCommands(cmdCh)

	// Forward agent commands to the SSE stream in a goroutine
	ctx := r.Context()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case cmd, ok := <-cmdCh:
				if !ok {
					return
				}
				cmdJSON, _ := json.Marshal(cmd)
				fmt.Fprintf(w, "data: {\"type\":\"command\",\"command\":%s}\n\n", cmdJSON)
				flusher.Flush()
				log.Printf("[blackbox] Sent command %q to device %s", cmd.Command, deviceID)
			}
		}
	}()

	// Read incoming events
	decoder := json.NewDecoder(r.Body)
	for decoder.More() {
		var event BlackBoxEvent
		if err := decoder.Decode(&event); err != nil {
			break
		}

		session.PushEvent(event)

		// For fatal errors, send immediate ack back to SDK
		if event.Type == "error" && event.IsFatal {
			fmt.Fprintf(w, "data: {\"type\":\"fatal_ack\",\"message\":\"Fatal error captured: %s\"}\n\n",
				strings.ReplaceAll(event.Message, "\"", "'"))
			flusher.Flush()

			// Auto-create a task for fatal crashes if task manager is available
			if s.taskMgr != nil {
				prompt := fmt.Sprintf("FATAL CRASH on %s device:\n\n%s\n\nStack:\n", platform, event.Message)
				for _, frame := range event.Stack {
					prompt += fmt.Sprintf("  %s\n", frame)
				}
				if ctx := session.GenerateBlackBoxContext(50); ctx != "" {
					prompt += "\nApp log context leading up to the crash:\n" + ctx
				}
				prompt += "\nPlease investigate and fix this crash immediately."

				if task, err := s.taskMgr.CreateTask(prompt, "", "", "blackbox-crash", "", "", nil); err == nil {
					fmt.Fprintf(w, "data: {\"type\":\"task_created\",\"taskId\":\"%s\",\"message\":\"Auto-created fix task for crash\"}\n\n", task.ID)
					flusher.Flush()
					log.Printf("[blackbox] Auto-created crash task %s for device %s", task.ID, deviceID)
				}
			}
		}
	}

	log.Printf("[blackbox] Device %s disconnected", deviceID)
}

// handleBlackBoxCommandStream handles GET /blackbox/command-stream — SSE-only
// endpoint for SDK devices to receive commands (reload, reload_bundle) from the agent.
// This is the lightweight counterpart to /blackbox/stream: no event ingestion,
// just command delivery. SDK devices use this + batch POST /blackbox/events.
func (s *HTTPServer) handleBlackBoxCommandStream(w http.ResponseWriter, r *http.Request) {
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "blackbox not available"})
		return
	}

	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	deviceID := r.URL.Query().Get("device")
	if deviceID == "" {
		deviceID = r.Header.Get("X-Device-ID")
	}
	if deviceID == "" {
		deviceID = "unknown"
	}
	platform := r.Header.Get("X-Platform")
	appName := r.Header.Get("X-App-Name")

	session := s.blackboxMgr.GetOrCreateSession(deviceID, platform, appName)

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Send initial ack
	fmt.Fprintf(w, "data: {\"type\":\"connected\",\"message\":\"Command stream active for %s\"}\n\n", deviceID)
	flusher.Flush()

	log.Printf("[blackbox] Device %s (%s/%s) connected to command stream", deviceID, platform, appName)

	cmdCh := session.SubscribeCommands()
	defer session.UnsubscribeCommands(cmdCh)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[blackbox] Device %s command stream disconnected", deviceID)
			return
		case cmd, ok := <-cmdCh:
			if !ok {
				return
			}
			cmdJSON, _ := json.Marshal(cmd)
			fmt.Fprintf(w, "data: {\"type\":\"command\",\"command\":%s}\n\n", cmdJSON)
			flusher.Flush()
			log.Printf("[blackbox] Sent command %q to device %s (command-stream)", cmd.Command, deviceID)
		}
	}
}

// handleBlackBoxEvents handles POST /blackbox/events — batch event push.
// For SDKs that can't maintain a persistent SSE connection, this accepts
// a JSON array of events in a single POST.
func (s *HTTPServer) handleBlackBoxEvents(w http.ResponseWriter, r *http.Request) {
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "blackbox not available"})
		return
	}

	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	deviceID := r.Header.Get("X-Device-ID")
	platform := r.Header.Get("X-Platform")
	appName := r.Header.Get("X-App-Name")
	if deviceID == "" {
		deviceID = "unknown"
	}

	var events []BlackBoxEvent
	if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	session := s.blackboxMgr.GetOrCreateSession(deviceID, platform, appName)
	for _, e := range events {
		session.PushEvent(e)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"received": len(events),
		"total":    len(session.Events),
	})
}

// handleBlackBoxLogs handles GET /blackbox/logs — retrieve buffered events.
// Query params: device=ID, type=error|log|navigation, last=N (default 100)
func (s *HTTPServer) handleBlackBoxLogs(w http.ResponseWriter, r *http.Request) {
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "blackbox not available"})
		return
	}

	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	deviceID := r.URL.Query().Get("device")
	eventType := r.URL.Query().Get("type")
	lastN := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("last")); err == nil && n > 0 {
		lastN = n
	}

	// If no device specified, list all sessions
	if deviceID == "" {
		sessions := s.blackboxMgr.ListSessions()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"sessions": sessions,
		})
		return
	}

	session := s.blackboxMgr.GetSession(deviceID)
	if session == nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "no session for device " + deviceID})
		return
	}

	var events []BlackBoxEvent
	if eventType != "" {
		events = session.EventsByType(eventType, lastN)
	} else {
		events = session.RecentEvents(lastN)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"deviceId": deviceID,
		"events":   events,
		"total":    len(session.Events),
	})
}

// handleBlackBoxSubscribe handles GET /blackbox/subscribe?device=ID — SSE stream
// that pushes new events to the subscriber in real-time. Used by the desktop UI
// or other tools that want to watch a device's log stream live.
func (s *HTTPServer) handleBlackBoxSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "blackbox not available"})
		return
	}

	deviceID := r.URL.Query().Get("device")
	if deviceID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "device parameter required"})
		return
	}

	session := s.blackboxMgr.GetSession(deviceID)
	if session == nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "no session for device " + deviceID})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := session.Subscribe()
	defer session.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleBlackBoxContext handles GET /blackbox/context?device=ID&events=N
// Returns a prompt-formatted summary of the black box log for use by AI agents.
func (s *HTTPServer) handleBlackBoxContext(w http.ResponseWriter, r *http.Request) {
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "blackbox not available"})
		return
	}

	deviceID := r.URL.Query().Get("device")
	if deviceID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "device parameter required"})
		return
	}

	maxEvents := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("events")); err == nil && n > 0 {
		maxEvents = n
	}

	session := s.blackboxMgr.GetSession(deviceID)
	if session == nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "no session for device " + deviceID})
		return
	}

	context := session.GenerateBlackBoxContext(maxEvents)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"deviceId": deviceID,
		"context":  context,
	})
}
