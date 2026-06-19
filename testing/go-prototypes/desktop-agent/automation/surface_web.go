package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

// WebSurface implements AutomationSurface for web browsers using chromedp
type WebSurface struct {
	sessionID      string
	config         SessionConfig

	// chromedp state
	browserCtx     context.Context
	browserCancel  context.CancelFunc
	allocCancel    context.CancelFunc

	// session state
	isWarm        bool
	lastUsed      time.Time
	metrics       SurfaceMetrics
	metricsMu     sync.RWMutex

	// console + network capture
	consoleMu      sync.RWMutex
	consoleMsgs     []ConsoleMsg
	networkMu      sync.RWMutex
	networkEvents  []NetworkEvent

	// event sink
	events        []SurfaceEvent
	eventsMu      sync.RWMutex
}

// NewWebSurface creates a new web automation surface
func NewWebSurface(ctx context.Context, config SessionConfig) (*WebSurface, error) {
	if config.Platform != "web" {
		return nil, fmt.Errorf("WebSurface requires platform=web, got %s", config.Platform)
	}

	sessionID := uuid.New().String()
	ws := &WebSurface{
		sessionID:    sessionID,
		config:       config,
		lastUsed:     time.Now(),
		metrics: SurfaceMetrics{
			TasksTotal:      0,
			TasksSuccess:    0,
			TasksFailed:     0,
			SessionsCreated: 1,
		},
		consoleMsgs:    make([]ConsoleMsg, 0, 500),
		networkEvents: make([]NetworkEvent, 0, 500),
		events:        make([]SurfaceEvent, 0, 1000),
	}

	// Initialize chromedp
	if err := ws.initChromedp(ctx); err != nil {
		return nil, fmt.Errorf("init chromedp: %w", err)
	}

	// Emit creation event
	ws.emitEvent(SurfaceEvent{
		SessionID: sessionID,
		Event:     EventCreated,
		Timestamp: time.Now(),
		Details: map[string]any{
			"driverType": "chromedp",
			"targetID":   config.TargetID,
		},
	})

	return ws, nil
}

// initChromedp initializes the chromedp backend
func (ws *WebSurface) initChromedp(ctx context.Context) error {
	port := 0
	userDataDir := ""

	// Generate temp user data dir for session isolation
	if ws.config.EncryptAtRest {
		dir, err := os.MkdirTemp("", fmt.Sprintf("yaver-web-%s-", ws.sessionID[:8]))
		if err != nil {
			return fmt.Errorf("create user-data-dir: %w", err)
		}
		userDataDir = dir
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("remote-debugging-port", strconv.Itoa(port)),
	}

	if userDataDir != "" {
		opts = append(opts, chromedp.UserDataDir(userDataDir))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	ws.allocCancel = allocCancel
	ws.browserCtx = browserCtx
	ws.browserCancel = browserCancel

	// Capture events
	chromedp.ListenTarget(browserCtx, ws.onEvent)

	return nil
}

// onEvent captures console and network events
func (ws *WebSurface) onEvent(ev interface{}) {
	// Simplified event capture - in production, wire up full capture from testkit
	// For now, we'll just track that events are coming in
	ws.eventsMu.Lock()
	defer ws.eventsMu.Unlock()
	ws.events = append(ws.events, SurfaceEvent{
		SessionID: ws.sessionID,
		Event:     "event_captured",
		Timestamp: time.Now(),
		Details:   map[string]any{"eventType": fmt.Sprintf("%T", ev)},
	})
}

// EnsureReady provisions and brings the surface to ready state
func (ws *WebSurface) EnsureReady(ctx context.Context) error {
	if ws.IsWarm() {
		// Already warm, just run health check
		report := ws.HealthCheck(ctx)
		if report.Overall == HealthHealthy {
			return nil
		}

		// Surface degraded, attempt to heal
		return ws.heal(ctx)
	}

	// Cold boot: launch and navigate to target
	start := time.Now()

	if err := chromedp.Run(ws.browserCtx); err != nil {
		return fmt.Errorf("launch chrome: %w", err)
	}

	// Navigate to target URL if specified
	if ws.config.TargetID != "" {
		if err := chromedp.Run(ws.browserCtx, chromedp.Navigate(ws.config.TargetID)); err != nil {
			ws.Close()
			return fmt.Errorf("navigate to %s: %w", ws.config.TargetID, err)
		}
	}

	// Wait for page load
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.WaitReady("networkidle"),
	); err != nil {
		ws.Close()
		return fmt.Errorf("wait for page load: %w", err)
	}

	ws.isWarm = true
	ws.lastUsed = time.Now()

	ws.emitEvent(SurfaceEvent{
		SessionID: ws.sessionID,
		Event:     EventWarmed,
		Timestamp: time.Now(),
		Details: map[string]any{
			"warmupTime": time.Since(start),
		},
	})

	// Record warmup in metrics
	ws.recordMetric("warmup_time_ms", float64(time.Since(start).Milliseconds()))

	return nil
}

// WarmKeep keeps the surface warm (no-op for web, chromedp stays alive in context)
func (ws *WebSurface) WarmKeep(ctx context.Context) error {
	if !ws.IsWarm() {
		return fmt.Errorf("cannot warm-keep cold surface")
	}

	// For web surfaces, warm-keep just means keeping the context alive
	// No special action needed - the chromedp browser stays alive in the context

	// Update last used time
	ws.lastUsed = time.Now()

	return nil
}

// Navigate to a URL
func (ws *WebSurface) Navigate(ctx context.Context, target string) error {
	start := time.Now()

	if err := chromedp.Run(ws.browserCtx, chromedp.Navigate(target)); err != nil {
		ws.recordTaskResult(false)
		return err
	}

	// Audit log
	ws.auditLog("navigate", target, "", err == nil, auditErrString(err))

	ws.recordMetric("navigate_latency_ms", float64(time.Since(start).Milliseconds()))
	return nil
}

// Element returns an element by selector
func (ws *WebSurface) Element(ctx context.Context, selector string) (Element, error) {
	start := time.Now()

	snapshot, err := ws.snapshot(ctx)
	if err != nil {
		return Element{}, fmt.Errorf("capture snapshot: %w", err)
	}

	// Find element in snapshot
	for _, el := range Snapshot.Elements {
		if el.Selector == selector {
			ws.recordMetric("element_find_latency_ms", float64(time.Since(start).Milliseconds()))
			return el, nil
		}
	}

	return Element{}, fmt.Errorf("element not found: %s", selector)
}

// Action performs a generic action on an element
func (ws *WebSurface) Action(ctx context.Context, selector string, action ActionType, payload any) error {
	start := time.Now()
	metadata := map[string]any{}

	var err error

	switch action {
	case ActionClick:
		err = chromedp.Run(ws.browserCtx,
			chromedp.Click(selector, chromedp.ByQuery, chromedp.NodeVisible),
		)

	case ActionFill:
		text, ok := payload.(string)
		if !ok {
			return fmt.Errorf("fill action requires string payload")
		}
		err = chromedp.Run(ws.browserCtx,
			chromedp.SendKeys(selector, text, chromedp.ByQuery),
		)

	case ActionSelect:
		value, ok := payload.(string)
		if !ok {
			return fmt.Errorf("select action requires string payload")
		}
		err = chromedp.Run(ws.browserCtx,
			chromedp.SetValue(selector, value, chromedp.ByQuery),
		)

	case ActionNavigate:
		url, ok := payload.(string)
		if !ok {
			return fmt.Errorf("navigate action requires string payload")
		}
		return ws.Navigate(ctx, url)

	case ActionWait:
		if ws.config.TargetID == "" {
			return fmt.Errorf("wait action requires configured URL to navigate to first")
		}
		err = chromedp.Run(ws.browserCtx,
			chromedp.WaitVisible(selector, chromedp.ByQuery),
		)

	default:
		return fmt.Errorf("unsupported action: %s", action)
	}

	// Audit log
	ws.auditLog(string(action), ws.config.TargetID, selector, err == nil, auditErrString(err))

	if err != nil {
		ws.recordTaskResult(false)
		return err
	}

	ws.recordTaskResult(true)
	ws.recordMetric(fmt.Sprintf("action_%s_latency_ms", action), float64(time.Since(start).Milliseconds()))
	return nil
}

// Screenshot captures the current state
func (ws *WebSurface) Screenshot(ctx context.Context) ([]byte, error) {
	start := time.Now()

	var buf []byte
	err := chromedp.Run(ws.browserCtx, chromedp.CaptureScreenshot(&buf))

	if err != nil {
		return nil, err
	}

	ws.recordMetric("screenshot_latency_ms", float64(time.Since(start).Milliseconds()))
	return buf, nil
}

// CaptureState returns the complete surface state
func (ws *WebSurface) CaptureState(ctx context.Context) (SurfaceState, error) {
	start := time.Now()

	snapshot, err := ws.snapshot(ctx)
	if err != nil {
		return SurfaceState{}, err
	}

	// Add metadata
	snapshot.Meta = StateMeta{
		Platform:    "web",
		Viewport:    Rect{Width: 1280, Height: 800}, // TODO: get actual viewport
		CapturedAt:  time.Now(),
		SessionID:   ws.sessionID,
	}

	ws.recordMetric("capture_state_latency_ms", float64(time.Since(start).Milliseconds()))
	return snapshot, nil
}

// CaptureNetwork returns recent network events
func (ws *WebSurface) CaptureNetwork(ctx context.Context) ([]NetworkEvent, error) {
	ws.networkMu.RLock()
	defer ws.networkMu.RUnlock()
	return append([]NetworkEvent(nil), ws.networkEvents...)
}

// CaptureConsole returns recent console messages
func (ws *WebSurface) CaptureConsole(ctx context.Context) ([]ConsoleMsg, error) {
	ws.consoleMu.RLock()
	defer ws.consoleMu.RUnlock()
	return append([]ConsoleMsg(nil), ws.consoleMsgs...)
}

// Mobile-Specific Actions (no-op on web)
func (ws *WebSurface) TapAt(ctx context.Context, x, y int) error {
	return fmt.Errorf("TapAt not supported on web surface")
}

func (ws *WebSurface) Swipe(ctx context.Context, x1, y1, x2, y2 int, durationMs int) error {
	return fmt.Errorf("Swipe not supported on web surface")
}

func (ws *WebSurface) Back(ctx context.Context) error {
	return fmt.Errorf("Back not supported on web surface")
}

func (ws *WebSurface) Home(ctx context.Context) error {
	return fmt.Errorf("Home not supported on web surface")
}

// HealthCheck runs diagnostics
func (ws *WebSurface) HealthCheck(ctx context.Context) HealthReport {
	report := HealthReport{
		Overall:   HealthHealthy,
		Checks:    make(map[string]HealthCheck),
		Suggested: []HealingAction{},
		LastCheck: time.Now(),
	}

	// Check browser is responsive
	checkStart := time.Now()
	var url string
	err := chromedp.Run(ws.browserCtx, chromedp.Location(&url))
	report.Checks["browser_responsive"] = HealthCheck{
		Name:     "browser_responsive",
		Status:   func() HealthStatus { if err != nil { return HealthUnhealthy } else { return HealthHealthy } }(),
		Message:  func() string { if err != nil { return err.Error() } else { return fmt.Sprintf("OK (current URL: %s)", url) } }(),
		Duration: time.Since(checkStart),
	}

	// Check for errors in console
	errors := ws.CaptureConsole(ctx)
	hasRecentErrors := false
	for _, msg := range errors {
		if time.Since(msg.At) < 5*time.Minute {
			hasRecentErrors = true
			break
		}
	}

	report.Checks["recent_console_errors"] = HealthCheck{
		Name:     "recent_console_errors",
		Status:   func() HealthStatus { if hasRecentErrors { return HealthDegraded } else { return HealthHealthy } }(),
		Message:  func() string {
			if hasRecentErrors {
				count := 0
				for _, msg := range errors {
					if time.Since(msg.At) < 5*time.Minute {
						count++
					}
				}
				return fmt.Sprintf("%d recent errors", count)
			}
			return "No recent errors"
		}(),
	}

	if hasRecentErrors {
		report.Overall = HealthDegraded
	}

	return report
}

// heal attempts to heal a degraded surface
func (ws *WebSurface) heal(ctx context.Context) error {
	// Simple healing: restart the surface
	ws.Close()
	return ws.EnsureReady(ctx)
}

// Metrics returns performance metrics
func (ws *WebSurface) Metrics() SurfaceMetrics {
	ws.metricsMu.RLock()
	defer ws.metricsMu.RUnlock()
	metrics := ws.metrics
	metrics.LastUsedAt = ws.lastUsed
	return metrics
}

// SessionID returns the session identifier
func (ws *WebSurface) SessionID() string {
	return ws.sessionID
}

// IsWarm reports whether the surface is warm
func (ws *WebSurface) IsWarm() bool {
	return ws.isWarm
}

// LastUsed returns when the surface was last used
func (ws *WebSurface) LastUsed() time.Time {
	return ws.lastUsed
}

// IdleTime returns how long the surface has been idle
func (ws *WebSurface) IdleTime() time.Duration {
	return time.Since(ws.lastUsed)
}

// Close releases resources
func (ws *WebSurface) Close() {
	if ws.browserCancel != nil {
		ws.browserCancel()
		ws.browserCancel = nil
	}
	if ws.allocCancel != nil {
		ws.allocCancel()
		ws.allocCancel = nil
	}

	ws.isWarm = false

	ws.emitEvent(SurfaceEvent{
		SessionID: ws.sessionID,
		Event:     "closed",
		Timestamp: time.Now(),
	})
}

// --- Helper methods ---

func (ws *WebSurface) recordMetric(name string, value float64) {
	ws.metricsMu.Lock()
	defer ws.metricsMu.Unlock()
	// Simple counter for now - in production use time series
	_ = name
	_ = value
}

func (ws *WebSurface) recordTaskResult(success bool) {
	ws.metricsMu.Lock()
	defer ws.metricsMu.Unlock()
	ws.metrics.TasksTotal++
	if success {
		ws.metrics.TasksSuccess++
	} else {
		ws.metrics.TasksFailed++
	}
}

func (ws *WebSurface) emitEvent(event SurfaceEvent) {
	ws.eventsMu.Lock()
	defer ws.eventsMu.Unlock()
	ws.events = append(ws.events, event)
}

func (ws *WebSurface) snapshot(ctx context.Context) (SurfaceState, error) {
	var raw string
	if err := chromedp.Run(ws.browserCtx, chromedp.Evaluate(domSnapshotJS, &raw)); err != nil {
		return SurfaceState{}, fmt.Errorf("dom snapshot: %w", err)
	}
	return parseSnapshotJSON(raw)
}

func (ws *WebSurface) auditLog(action, target, selector string, success bool, errMsg string) {
	// Simplified audit log - in production, use proper persistence
	_ = action
	_ = target
	_ = selector
	_ = success
	_ = errMsg
}

func auditErrString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// parseSnapshotJSON unmarshals the JS-side DOM walk result
func parseSnapshotJSON(raw string) (SurfaceState, error) {
	var s Snapshot
	if err := json.Unmarshal([]byte(raw), &s); err != {
		return SurfaceState{}, fmt.Errorf("parse dom snapshot: %w", err)
	}
	return s, nil
}

func jsStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// DOM snapshot JS from testkit
const domSnapshotJS = `(() => {
  function sel(el){
    const tid = el.getAttribute('data-testid');
    if (tid) return '[data-testid="' + tid + '"]';
    if (el.id) return '#' + CSS.escape(el.id);
    const al = el.getAttribute('aria-label');
    if (al) return el.tagName.toLowerCase() + '[aria-label="' + al.replace(/"/g,'\\"') + '"]';
    let path = [], n = el;
    while (n && n.nodeType === 1 && path.length < 6) {
      let i = 1, s = n;
      while ((s = s.previousElementSibling)) if (s.tagName === n.tagName) i++;
      path.unshift(n.tagName.toLowerCase() + ':nth-of-type(' + i + ')');
      n = n.parentElement;
    }
    return path.join(' > ');
  }
  function role(el){
    const t = el.tagName.toLowerCase();
    if (t === 'a') return 'link';
    if (t === 'button' || el.getAttribute('role') === 'button') return 'button';
    if (t === 'input' || t === 'textarea') return 'textbox';
    if (el.getAttribute('role') === 'checkbox') return 'checkbox';
    return 'other';
  }
  const out = [];
  const els = document.querySelectorAll(
    'a,button,input,textarea,select,[role="button"],[role="link"],[data-testid]');
  els.forEach(el => {
    const r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) return;
    out.push({
      selector: sel(el),
      role: role(el),
      text: (el.innerText || el.value || el.placeholder || '').trim().slice(0, 120),
      testId: el.getAttribute('data-testid') || ''
    });
  });
  return JSON.stringify({
    url: location.href,
    title: document.title,
    interactables: out.slice(0, 200)
  });
})()`