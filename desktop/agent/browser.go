package main

// browser.go — BrowserManager: persistent browser sessions for AI-driven automation.
//
// AI agents (Claude Code, Aider, etc.) control Chrome on the dev machine
// via MCP tools (browser_open, browser_navigate, browser_click, etc.).
// Each action returns a screenshot so the AI can reason about what it sees.
// Sessions persist across tool calls — cookies, auth state, and the
// current URL survive between steps.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
)

// BrowserManager manages named browser sessions.
type BrowserManager struct {
	mu       sync.RWMutex
	sessions map[string]*BrowserSession
	eventCh  chan BrowserEvent // broadcast to SSE listeners
	stopCh   chan struct{}
}

// BrowserSession wraps a persistent chromedp context.
type BrowserSession struct {
	ID            string    `json:"id"`
	Headful       bool      `json:"headful"`
	CreatedAt     time.Time `json:"createdAt"`
	LastUsedAt    time.Time `json:"lastUsedAt"`
	CurrentURL    string    `json:"currentUrl"`
	CurrentTitle  string    `json:"currentTitle"`
	Interactive   bool      `json:"interactive"`
	ProfileDir    string    `json:"profileDir,omitempty"`
	ProxyURL      string    `json:"proxyUrl,omitempty"` // egress proxy, creds redacted
	EgressIP      string    `json:"egressIp,omitempty"` // last observed egress IP for this vantage
	ViewW         int       `json:"viewW,omitempty"`
	ViewH         int       `json:"viewH,omitempty"`
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc
}

// BrowserEvent is pushed to SSE listeners after each action.
type BrowserEvent struct {
	Type          string `json:"type"` // "screenshot", "navigate", "action", "error", "closed"
	SessionID     string `json:"sessionId"`
	ScreenshotB64 string `json:"screenshot,omitempty"`
	URL           string `json:"url,omitempty"`
	Title         string `json:"title,omitempty"`
	Message       string `json:"message,omitempty"`
	Timestamp     string `json:"timestamp"`
}

// BrowserActionResult is returned by actions that capture a screenshot.
type BrowserActionResult struct {
	ScreenshotB64 string `json:"screenshot"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	Message       string `json:"message,omitempty"`
}

// SessionIdleTimeout is how long an unused session stays alive.
const SessionIdleTimeout = 30 * time.Minute

// NewBrowserManager creates a manager and starts the idle cleanup goroutine.
func NewBrowserManager() *BrowserManager {
	bm := &BrowserManager{
		sessions: make(map[string]*BrowserSession),
		eventCh:  make(chan BrowserEvent, 64),
		stopCh:   make(chan struct{}),
	}
	go bm.cleanupLoop()
	return bm
}

// Stop shuts down all sessions and the cleanup goroutine.
func (bm *BrowserManager) Stop() {
	close(bm.stopCh)
	bm.mu.Lock()
	defer bm.mu.Unlock()
	for id, s := range bm.sessions {
		s.browserCancel()
		s.allocCancel()
		delete(bm.sessions, id)
	}
}

func (bm *BrowserManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-bm.stopCh:
			return
		case <-ticker.C:
			bm.mu.Lock()
			for id, s := range bm.sessions {
				if time.Since(s.LastUsedAt) > SessionIdleTimeout {
					s.browserCancel()
					s.allocCancel()
					delete(bm.sessions, id)
					bm.emit(BrowserEvent{
						Type:      "closed",
						SessionID: id,
						Message:   "session timed out after idle",
					})
				}
			}
			bm.mu.Unlock()
		}
	}
}

func (bm *BrowserManager) emit(ev BrowserEvent) {
	ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
	select {
	case bm.eventCh <- ev:
	default:
		// Drop if nobody is listening.
	}
}

func (bm *BrowserManager) getSession(id string) (*BrowserSession, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	s, ok := bm.sessions[id]
	if !ok {
		return nil, fmt.Errorf("browser session %q not found", id)
	}
	return s, nil
}

func (bm *BrowserManager) touch(s *BrowserSession) {
	bm.mu.Lock()
	s.LastUsedAt = time.Now()
	bm.mu.Unlock()
}

// OpenSession starts a new Chrome instance with machine-native egress (no proxy).
func (bm *BrowserManager) OpenSession(id string, headful bool) error {
	return bm.OpenSessionWithProxy(id, headful, "")
}

// OpenSessionWithProxy starts a Chrome instance whose traffic egresses through
// proxyURL (e.g. "http://127.0.0.1:8080", "socks5://10.0.0.2:1080"). An empty
// proxyURL means machine-native egress, identical to OpenSession.
//
// The proxy is how a collector adopts a chosen vantage / egress identity: route
// a collector on this machine out through a proxy or peer the user controls so
// the source sees that egress. Yaver only routes through egress the user owns or
// is entitled to use — never a rotating pool to defeat a block. See
// docs/user-directed-data-collection-runtimes.md (Multi-Vantage / Egress).
func (bm *BrowserManager) OpenSessionWithProxy(id string, headful bool, proxyURL string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if _, exists := bm.sessions[id]; exists {
		return fmt.Errorf("browser session %q already exists", id)
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !headful),
		chromedp.Flag("disable-gpu", !headful),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("hide-scrollbars", !headful),
		chromedp.WindowSize(1280, 900),
	)
	if proxyURL != "" {
		allocOpts = append(allocOpts, chromedp.ProxyServer(proxyURL))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	// Boot Chrome.
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		return fmt.Errorf("launch chrome: %w (install Chrome/Chromium)", err)
	}

	now := time.Now()
	bm.sessions[id] = &BrowserSession{
		ID:            id,
		Headful:       headful,
		ProxyURL:      redactProxyCreds(proxyURL), // store redacted; raw is baked into the alloc
		CreatedAt:     now,
		LastUsedAt:    now,
		allocCancel:   allocCancel,
		browserCtx:    browserCtx,
		browserCancel: browserCancel,
	}

	msg := "session opened"
	if proxyURL != "" {
		msg = "session opened via proxy " + redactProxyCreds(proxyURL)
	}
	bm.emit(BrowserEvent{
		Type:      "action",
		SessionID: id,
		Message:   msg,
	})

	return nil
}

// redactProxyCreds strips any user:pass@ userinfo from a proxy URL so it is safe
// to log, emit as an event, or return over HTTP. Proxy credentials belong in the
// vault, never in logs or session listings.
func redactProxyCreds(proxyURL string) string {
	if proxyURL == "" {
		return ""
	}
	if u, err := url.Parse(proxyURL); err == nil && u.User != nil {
		u.User = url.User("redacted")
		return u.String()
	}
	return proxyURL
}

// CloseSession shuts down a browser instance.
func (bm *BrowserManager) CloseSession(id string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	s, ok := bm.sessions[id]
	if !ok {
		return fmt.Errorf("browser session %q not found", id)
	}

	s.browserCancel()
	s.allocCancel()
	delete(bm.sessions, id)

	bm.emit(BrowserEvent{
		Type:      "closed",
		SessionID: id,
		Message:   "session closed",
	})

	return nil
}

// ListSessions returns info about all active sessions.
func (bm *BrowserManager) ListSessions() []BrowserSession {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	out := make([]BrowserSession, 0, len(bm.sessions))
	for _, s := range bm.sessions {
		out = append(out, BrowserSession{
			ID:           s.ID,
			Headful:      s.Headful,
			CreatedAt:    s.CreatedAt,
			LastUsedAt:   s.LastUsedAt,
			CurrentURL:   s.CurrentURL,
			CurrentTitle: s.CurrentTitle,
			ProxyURL:     s.ProxyURL, // already redacted at open time
			EgressIP:     s.EgressIP,
		})
	}
	return out
}

// CheckEgressIP navigates the session to an IP-echo endpoint and returns the
// egress IP the source actually sees. When the session was opened with a proxy
// this reflects the proxy's IP — i.e. the vantage's egress identity — which is
// how Yaver reports "the egress it actually used". echoURL must return the
// caller's IP as its response body (e.g. https://api.ipify.org). The result is
// cached on the session as vantage metadata. The IP is provenance, not a
// normalized data field — keep it out of collected rows.
func (bm *BrowserManager) CheckEgressIP(id, echoURL string) (string, error) {
	if echoURL == "" {
		echoURL = "https://api.ipify.org"
	}
	if _, err := bm.Navigate(id, echoURL); err != nil {
		return "", fmt.Errorf("egress check navigate: %w", err)
	}
	ip, err := bm.ExtractText(id, "body")
	if err != nil {
		return "", fmt.Errorf("egress check read: %w", err)
	}
	ip = strings.TrimSpace(ip)
	if s, err := bm.getSession(id); err == nil {
		bm.mu.Lock()
		s.EgressIP = ip
		bm.mu.Unlock()
	}
	return ip, nil
}

// captureState grabs a screenshot plus current URL and title.
func (bm *BrowserManager) captureState(s *BrowserSession) (*BrowserActionResult, error) {
	var (
		screenshot []byte
		url        string
		title      string
	)
	if err := chromedp.Run(s.browserCtx,
		chromedp.Location(&url),
		chromedp.Title(&title),
		chromedp.CaptureScreenshot(&screenshot),
	); err != nil {
		return nil, fmt.Errorf("capture state: %w", err)
	}

	bm.mu.Lock()
	s.CurrentURL = url
	s.CurrentTitle = title
	bm.mu.Unlock()

	b64 := base64.StdEncoding.EncodeToString(screenshot)

	bm.emit(BrowserEvent{
		Type:          "screenshot",
		SessionID:     s.ID,
		ScreenshotB64: b64,
		URL:           url,
		Title:         title,
	})

	return &BrowserActionResult{
		ScreenshotB64: b64,
		URL:           url,
		Title:         title,
	}, nil
}

// Navigate goes to a URL and returns a screenshot.
func (bm *BrowserManager) Navigate(id, url string) (*BrowserActionResult, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	if err := chromedp.Run(s.browserCtx,
		chromedp.Navigate(url),
		chromedp.Sleep(1*time.Second), // let page settle
	); err != nil {
		return nil, fmt.Errorf("navigate to %s: %w", url, err)
	}

	bm.emit(BrowserEvent{
		Type:      "navigate",
		SessionID: s.ID,
		URL:       url,
		Message:   "navigated to " + url,
	})

	return bm.captureState(s)
}

// Click clicks a CSS selector and returns a screenshot.
func (bm *BrowserManager) Click(id, selector string) (*BrowserActionResult, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	if err := chromedp.Run(s.browserCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		return nil, fmt.Errorf("click %q: %w", selector, err)
	}

	bm.emit(BrowserEvent{
		Type:      "action",
		SessionID: s.ID,
		Message:   "clicked " + selector,
	})

	return bm.captureState(s)
}

// Type fills a text input and returns a screenshot.
func (bm *BrowserManager) Type(id, selector, text string, clear bool) (*BrowserActionResult, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	actions := []chromedp.Action{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	}
	if clear {
		actions = append(actions, chromedp.Clear(selector, chromedp.ByQuery))
	}
	actions = append(actions,
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	)

	if err := chromedp.Run(s.browserCtx, actions...); err != nil {
		return nil, fmt.Errorf("type into %q: %w", selector, err)
	}

	bm.emit(BrowserEvent{
		Type:      "action",
		SessionID: s.ID,
		Message:   fmt.Sprintf("typed into %s", selector),
	})

	return bm.captureState(s)
}

// Select picks a value in a <select> dropdown and returns a screenshot.
func (bm *BrowserManager) Select(id, selector, value string) (*BrowserActionResult, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	js := fmt.Sprintf(
		`document.querySelector(%q).value = %q; document.querySelector(%q).dispatchEvent(new Event('change', {bubbles: true}));`,
		selector, value, selector,
	)
	var ignored interface{}
	if err := chromedp.Run(s.browserCtx,
		chromedp.Evaluate(js, &ignored),
		chromedp.Sleep(300*time.Millisecond),
	); err != nil {
		return nil, fmt.Errorf("select %q in %q: %w", value, selector, err)
	}

	return bm.captureState(s)
}

// Screenshot captures the current page as a base64 PNG.
func (bm *BrowserManager) Screenshot(id string) (*BrowserActionResult, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)
	return bm.captureState(s)
}

// ExtractText returns the text content of a CSS selector (or body).
func (bm *BrowserManager) ExtractText(id, selector string) (string, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return "", err
	}
	bm.touch(s)

	if selector == "" {
		selector = "body"
	}

	var text string
	if err := chromedp.Run(s.browserCtx,
		chromedp.Text(selector, &text, chromedp.ByQuery),
	); err != nil {
		return "", fmt.Errorf("extract text from %q: %w", selector, err)
	}

	return text, nil
}

// ExtractAttribute returns an attribute value from a CSS selector.
func (bm *BrowserManager) ExtractAttribute(id, selector, attr string) (string, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return "", err
	}
	bm.touch(s)

	var value string
	var ok bool
	if err := chromedp.Run(s.browserCtx,
		chromedp.AttributeValue(selector, attr, &value, &ok, chromedp.ByQuery),
	); err != nil {
		return "", fmt.Errorf("extract attribute %q from %q: %w", attr, selector, err)
	}
	if !ok {
		return "", fmt.Errorf("attribute %q not found on %q", attr, selector)
	}

	return value, nil
}

// WaitFor waits for a CSS selector to become visible.
func (bm *BrowserManager) WaitFor(id, selector string, timeoutMs int) error {
	s, err := bm.getSession(id)
	if err != nil {
		return err
	}
	bm.touch(s)

	if timeoutMs <= 0 {
		timeoutMs = 10000
	}
	ctx, cancel := context.WithTimeout(s.browserCtx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("wait for %q (timeout %dms): %w", selector, timeoutMs, err)
	}

	return nil
}

// WaitForNavigation waits for the page URL to change.
func (bm *BrowserManager) WaitForNavigation(id string, timeoutMs int) error {
	s, err := bm.getSession(id)
	if err != nil {
		return err
	}
	bm.touch(s)

	if timeoutMs <= 0 {
		timeoutMs = 10000
	}

	bm.mu.RLock()
	startURL := s.CurrentURL
	bm.mu.RUnlock()

	deadline := time.After(time.Duration(timeoutMs) * time.Millisecond)
	for {
		select {
		case <-deadline:
			return fmt.Errorf("wait_for_navigation: timed out after %dms", timeoutMs)
		default:
		}
		var loc string
		if err := chromedp.Run(s.browserCtx, chromedp.Location(&loc)); err == nil && loc != startURL {
			bm.mu.Lock()
			s.CurrentURL = loc
			bm.mu.Unlock()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Scroll scrolls the page or an element.
func (bm *BrowserManager) Scroll(id string, deltaX, deltaY int) (*BrowserActionResult, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	js := fmt.Sprintf("window.scrollBy(%d, %d)", deltaX, deltaY)
	var ignored interface{}
	if err := chromedp.Run(s.browserCtx,
		chromedp.Evaluate(js, &ignored),
		chromedp.Sleep(300*time.Millisecond),
	); err != nil {
		return nil, fmt.Errorf("scroll: %w", err)
	}

	return bm.captureState(s)
}

// Evaluate runs JavaScript and returns the result.
func (bm *BrowserManager) Evaluate(id, js string) (interface{}, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	var result interface{}
	if err := chromedp.Run(s.browserCtx,
		chromedp.Evaluate(js, &result),
	); err != nil {
		return nil, fmt.Errorf("evaluate: %w", err)
	}

	return result, nil
}

// GetURL returns the current page URL.
func (bm *BrowserManager) GetURL(id string) (string, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return "", err
	}
	bm.touch(s)

	var url string
	if err := chromedp.Run(s.browserCtx, chromedp.Location(&url)); err != nil {
		return "", err
	}
	return url, nil
}

// GetDOM returns the page HTML, truncated for token budget.
func (bm *BrowserManager) GetDOM(id string) (string, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return "", err
	}
	bm.touch(s)

	var html string
	if err := chromedp.Run(s.browserCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			node, err := dom.GetDocument().Do(ctx)
			if err != nil {
				return err
			}
			h, err := dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
			if err != nil {
				return err
			}
			html = h
			return nil
		}),
	); err != nil {
		return "", fmt.Errorf("get DOM: %w", err)
	}

	// Truncate to 50KB for AI token budget.
	const maxLen = 50 * 1024
	if len(html) > maxLen {
		html = html[:maxLen] + "\n<!-- truncated at 50KB -->"
	}

	// Strip excessive whitespace.
	lines := strings.Split(html, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}

	return strings.Join(cleaned, "\n"), nil
}
