package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BlackBoxEvent represents a single event in the device's black box stream.
// These events flow continuously from the SDK to the agent, building a
// flight-recorder-style log of everything that happens in the app.
type BlackBoxEvent struct {
	Type      string                 `json:"type"`                // log, error, navigation, lifecycle, network, state, render
	Level     string                 `json:"level,omitempty"`     // info, warn, error (for log events)
	Message   string                 `json:"message"`             // Human-readable description
	Timestamp int64                  `json:"timestamp"`           // Unix ms
	Stack     []string               `json:"stack,omitempty"`     // Stack trace (for errors)
	IsFatal   bool                   `json:"isFatal,omitempty"`   // Fatal crash flag
	Metadata  map[string]interface{} `json:"metadata,omitempty"`  // Arbitrary context
	Source    string                 `json:"source,omitempty"`    // File/component that emitted
	Duration  float64                `json:"duration,omitempty"`  // Duration in ms (for network, render)
	Route     string                 `json:"route,omitempty"`     // Screen/route name (for navigation)
	PrevRoute string                 `json:"prevRoute,omitempty"` // Previous screen (for navigation)
}

// BlackBoxCommand represents a command pushed from the agent to a connected SDK device.
type BlackBoxCommand struct {
	Command string                 `json:"command"`           // "reload", "reload_bundle"
	Data    map[string]interface{} `json:"data,omitempty"`    // e.g. {"bundleUrl": "/dev/native-bundle"}
}

// BlackBoxSession represents a continuous streaming session from a device.
type BlackBoxSession struct {
	DeviceID  string           `json:"deviceId"`
	Platform  string           `json:"platform"` // ios, android, web
	AppName   string           `json:"appName,omitempty"`
	StartedAt string           `json:"startedAt"`
	Events    []BlackBoxEvent  `json:"events"`
	mu        sync.RWMutex
	maxEvents int
	// SSE subscribers waiting for new events
	subscribers []chan BlackBoxEvent
	subMu       sync.Mutex
	// Command channels for pushing commands to connected SDK devices (via /blackbox/stream SSE)
	commandListeners []chan BlackBoxCommand
	cmdMu            sync.Mutex
}

// BlackBoxManager manages streaming sessions from multiple devices.
type BlackBoxManager struct {
	mu       sync.RWMutex
	sessions map[string]*BlackBoxSession // keyed by deviceID
	baseDir  string
}

const defaultMaxBlackBoxEvents = 1000

// NewBlackBoxManager creates a new black box manager.
func NewBlackBoxManager() (*BlackBoxManager, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Join(dir, "blackbox")
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, err
	}
	return &BlackBoxManager{
		sessions: make(map[string]*BlackBoxSession),
		baseDir:  baseDir,
	}, nil
}

// GetOrCreateSession returns the session for a device, creating one if needed.
func (m *BlackBoxManager) GetOrCreateSession(deviceID, platform, appName string) *BlackBoxSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[deviceID]; ok {
		return s
	}

	s := &BlackBoxSession{
		DeviceID:  deviceID,
		Platform:  platform,
		AppName:   appName,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Events:    make([]BlackBoxEvent, 0, 256),
		maxEvents: defaultMaxBlackBoxEvents,
	}
	m.sessions[deviceID] = s
	return s
}

// GetSession returns a session by device ID, or nil.
func (m *BlackBoxManager) GetSession(deviceID string) *BlackBoxSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[deviceID]
}

// ListSessions returns summaries of all active sessions.
func (m *BlackBoxManager) ListSessions() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]map[string]interface{}, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.RLock()
		result = append(result, map[string]interface{}{
			"deviceId":   s.DeviceID,
			"platform":   s.Platform,
			"appName":    s.AppName,
			"startedAt":  s.StartedAt,
			"eventCount": len(s.Events),
		})
		s.mu.RUnlock()
	}
	return result
}

// PushEvent adds an event to a session and notifies subscribers.
func (s *BlackBoxSession) PushEvent(event BlackBoxEvent) {
	s.mu.Lock()
	s.Events = append(s.Events, event)
	if len(s.Events) > s.maxEvents {
		// Drop oldest 10% to avoid frequent shifts
		drop := s.maxEvents / 10
		if drop < 1 {
			drop = 1
		}
		s.Events = s.Events[drop:]
	}
	s.mu.Unlock()

	// Fan out to the cross-device error store so the mobile
	// Errors tab sees aggregated errors across every SDK session.
	// Non-error events are filtered inside ErrorStore.Record so
	// track / analytics events pass through unchanged.
	if event.Type == "error" || event.IsFatal {
		GlobalErrorStore().Record(s.DeviceID, event)
	}

	// Track events get appended to the analytics ledger for CSV
	// export. Zero dashboards here — the ledger is a tunnel.
	if event.Type == "track" {
		props := map[string]string{}
		for k, v := range event.Metadata {
			if sv, ok := v.(string); ok {
				props[k] = sv
			}
		}
		AnalyticsAppend(TrackEvent{
			Name:      event.Message,
			DeviceID:  s.DeviceID,
			Route:     event.Route,
			Props:     props,
			Timestamp: event.Timestamp,
		})
	}

	// Notify SSE subscribers (non-blocking)
	s.subMu.Lock()
	for _, ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// Slow subscriber — skip
		}
	}
	s.subMu.Unlock()
}

// Subscribe returns a channel that receives new events.
func (s *BlackBoxSession) Subscribe() chan BlackBoxEvent {
	ch := make(chan BlackBoxEvent, 64)
	s.subMu.Lock()
	s.subscribers = append(s.subscribers, ch)
	s.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *BlackBoxSession) Unsubscribe(ch chan BlackBoxEvent) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for i, c := range s.subscribers {
		if c == ch {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

// SubscribeCommands returns a channel that receives commands pushed by the agent.
func (s *BlackBoxSession) SubscribeCommands() chan BlackBoxCommand {
	ch := make(chan BlackBoxCommand, 16)
	s.cmdMu.Lock()
	s.commandListeners = append(s.commandListeners, ch)
	s.cmdMu.Unlock()
	return ch
}

// UnsubscribeCommands removes a command listener channel.
func (s *BlackBoxSession) UnsubscribeCommands(ch chan BlackBoxCommand) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	for i, c := range s.commandListeners {
		if c == ch {
			s.commandListeners = append(s.commandListeners[:i], s.commandListeners[i+1:]...)
			close(ch)
			return
		}
	}
}

// SendCommand pushes a command to all connected SDK devices for this session.
func (s *BlackBoxSession) SendCommand(cmd BlackBoxCommand) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	for _, ch := range s.commandListeners {
		select {
		case ch <- cmd:
		default:
			// Slow listener — skip
		}
	}
}

// BroadcastCommand pushes a command to ALL connected SDK sessions.
func (m *BlackBoxManager) BroadcastCommand(cmd BlackBoxCommand) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		s.SendCommand(cmd)
	}
}

// RecentEvents returns the last N events from the session.
func (s *BlackBoxSession) RecentEvents(n int) []BlackBoxEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n <= 0 || len(s.Events) == 0 {
		return nil
	}
	start := len(s.Events) - n
	if start < 0 {
		start = 0
	}
	result := make([]BlackBoxEvent, len(s.Events)-start)
	copy(result, s.Events[start:])
	return result
}

// EventsByType returns the last N events filtered by type.
func (s *BlackBoxSession) EventsByType(eventType string, n int) []BlackBoxEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]BlackBoxEvent, 0)
	for i := len(s.Events) - 1; i >= 0 && len(result) < n; i-- {
		if s.Events[i].Type == eventType {
			result = append(result, s.Events[i])
		}
	}
	// Reverse to chronological order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// GenerateBlackBoxContext creates a prompt-friendly summary of the black box
// log for an AI agent. This gets prepended to feedback/task prompts so the
// agent knows what the app was doing before the user reported an issue.
func (s *BlackBoxSession) GenerateBlackBoxContext(maxEvents int) string {
	events := s.RecentEvents(maxEvents)
	if len(events) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Live App Black Box (%s, %s) ===\n", s.Platform, s.AppName))
	sb.WriteString(fmt.Sprintf("Streaming since %s | %d events captured\n", s.StartedAt, len(s.Events)))
	sb.WriteString(fmt.Sprintf("Showing last %d events:\n\n", len(events)))

	for _, e := range events {
		ts := time.UnixMilli(e.Timestamp).Format("15:04:05.000")
		switch e.Type {
		case "error":
			fatal := ""
			if e.IsFatal {
				fatal = " FATAL"
			}
			sb.WriteString(fmt.Sprintf("[%s] ERROR%s: %s\n", ts, fatal, e.Message))
			for _, frame := range e.Stack {
				sb.WriteString(fmt.Sprintf("    %s\n", frame))
			}
			if len(e.Metadata) > 0 {
				metaJSON, _ := json.Marshal(e.Metadata)
				sb.WriteString(fmt.Sprintf("    context: %s\n", string(metaJSON)))
			}
		case "log":
			level := strings.ToUpper(e.Level)
			if e.Source != "" {
				sb.WriteString(fmt.Sprintf("[%s] %s (%s): %s\n", ts, level, e.Source, e.Message))
			} else {
				sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", ts, level, e.Message))
			}
		case "navigation":
			if e.PrevRoute != "" {
				sb.WriteString(fmt.Sprintf("[%s] NAVIGATE: %s -> %s\n", ts, e.PrevRoute, e.Route))
			} else {
				sb.WriteString(fmt.Sprintf("[%s] NAVIGATE: -> %s\n", ts, e.Route))
			}
		case "lifecycle":
			sb.WriteString(fmt.Sprintf("[%s] LIFECYCLE: %s\n", ts, e.Message))
		case "network":
			if e.Duration > 0 {
				sb.WriteString(fmt.Sprintf("[%s] NETWORK: %s (%.0fms)\n", ts, e.Message, e.Duration))
			} else {
				sb.WriteString(fmt.Sprintf("[%s] NETWORK: %s\n", ts, e.Message))
			}
		case "state":
			sb.WriteString(fmt.Sprintf("[%s] STATE: %s\n", ts, e.Message))
		case "render":
			if e.Duration > 0 {
				sb.WriteString(fmt.Sprintf("[%s] RENDER: %s (%.1fms)\n", ts, e.Message, e.Duration))
			} else {
				sb.WriteString(fmt.Sprintf("[%s] RENDER: %s\n", ts, e.Message))
			}
		default:
			sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", ts, strings.ToUpper(e.Type), e.Message))
		}
	}
	sb.WriteString("\n=== End Black Box ===\n")
	return sb.String()
}
