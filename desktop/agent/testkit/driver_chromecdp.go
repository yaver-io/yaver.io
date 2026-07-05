package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	cdpnetwork "github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Chrome CDP driver — the default Auto Test web surface.
//
// This is the driver the Yaver agent wraps when a user runs Auto Test
// against a React Native app: it launches Chrome with
// `--remote-debugging-port=<port> --user-data-dir=<tmp>` (exactly the
// flags the feature spec calls for) and drives it over the Chrome
// DevTools Protocol via chromedp — which testkit already depends on
// (a11y.go, runner.go, recorder.go), so the CDP path adds zero new
// `npm install` weight and no Java/ChromeDriver.
//
// Selenium is the *opt-in* backend: a project that sets
// `"driver":"selenium"` in .yaver/autotest.json gets seleniumBackend,
// which is lazily provisioned on first use (never at npm install). It
// is a thin stub today — the CDP path is what v1 ships and tests.
//
// We keep this deliberately small: launch, navigate, snapshot the
// interactable DOM (what the CRUD-enumeration agent traverses),
// click/fill, screenshot (reused by the WebRTC frame pump), and
// console/network capture (what turns a failed assertion into a bug
// report). Anything beyond that returns a clear error rather than a
// silent no-op.

// ChromeOpts configures a Chrome session. ViewportW/H + DPR drive the
// tablet/phone size the user picks in the UI; RemoteDebugPort honors
// the spec's explicit `--remote-debugging-port=9222` (0 → an ephemeral
// free port so concurrent runs / a user's own Chrome don't collide).
type ChromeOpts struct {
	URL             string
	ViewportW       int
	ViewportH       int
	DPR             float64
	Headful         bool // true only when the user opted into the live WebRTC stream
	RemoteDebugPort int  // 0 = pick a free port; the spec's default is 9222
	UserDataDir     string
}

// ConsoleMsg is one captured browser console line (warn/error only —
// info/debug is too noisy to be a useful bug signal).
type ConsoleMsg struct {
	Level string    `json:"level"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
}

// NetEvent is one captured network request outcome. The CRUD agent
// uses these to assert that an add/update/get actually hit its API
// hook and didn't 4xx/5xx.
type NetEvent struct {
	URL    string    `json:"url"`
	Method string    `json:"method"`
	Status int64     `json:"status"`
	At     time.Time `json:"at"`
}

// Interactable is one element the CRUD-enumeration agent can act on.
// Selector is a stable CSS selector (testID > id > aria > text path).
type Interactable struct {
	Selector string `json:"selector"`
	Role     string `json:"role"` // button | link | textbox | checkbox | other
	Text     string `json:"text"`
	TestID   string `json:"testId,omitempty"`
}

// Snapshot is the agent-facing view of the page: URL/title plus the
// interactable elements. This is intentionally a flattened list, not a
// full DOM tree — the agent reasons about "what can I tap/fill here",
// and a flat list keeps the prompt small.
type Snapshot struct {
	URL           string         `json:"url"`
	Title         string         `json:"title"`
	Interactables []Interactable `json:"interactables"`
}

// WebDriver is the backend-agnostic surface. cdpBackend (default) and
// seleniumBackend (opt-in) both satisfy it so the orchestrator never
// branches on the driver kind.
type WebDriver interface {
	Launch(ctx context.Context) error
	Navigate(ctx context.Context, url string) error
	Snapshot(ctx context.Context) (Snapshot, error)
	Click(ctx context.Context, selector string) error
	Fill(ctx context.Context, selector, value string) error
	VisibleText(ctx context.Context, selector string) (string, error)
	Screenshot(ctx context.Context) ([]byte, error) // PNG; feeds the JPEG/RTP pump
	Console() []ConsoleMsg
	Network() []NetEvent
	Close()
}

// NewWebDriver returns the configured backend. driver is "" / "cdp"
// for the default Chrome-DevTools-Protocol path, or "selenium" for the
// opt-in WebDriver path.
func NewWebDriver(driver string, opts ChromeOpts) (WebDriver, error) {
	switch driver {
	case "", "cdp", "chrome", "chrome-cdp":
		return newCDPBackend(opts), nil
	case "selenium":
		return newSeleniumBackend(opts), nil
	default:
		return nil, fmt.Errorf("unknown autotest driver %q (use \"cdp\" or \"selenium\")", driver)
	}
}

// --- CDP backend -----------------------------------------------------

type cdpBackend struct {
	opts        ChromeOpts
	allocCancel context.CancelFunc
	browserCtx  context.Context
	browserStop context.CancelFunc

	mu      sync.Mutex
	console []ConsoleMsg
	network []NetEvent
}

func newCDPBackend(opts ChromeOpts) *cdpBackend {
	if opts.DPR <= 0 {
		opts.DPR = 1
	}
	return &cdpBackend{opts: opts}
}

// Launch boots Chrome with the spec's explicit flags. chromedp's
// ExecAllocator turns these into the literal command line
// `chrome --remote-debugging-port=<port> --user-data-dir=<dir> ...`,
// so the user gets exactly the Chrome they asked for, driven over CDP.
func (b *cdpBackend) Launch(ctx context.Context) error {
	port := b.opts.RemoteDebugPort
	if port == 0 {
		port = pickFreePort()
	}
	userDataDir := b.opts.UserDataDir
	if userDataDir == "" {
		dir, err := os.MkdirTemp("", "yaver-autotest-chrome-*")
		if err != nil {
			return fmt.Errorf("autotest: temp user-data-dir: %w", err)
		}
		userDataDir = dir
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !b.opts.Headful),
		chromedp.Flag("disable-gpu", !b.opts.Headful),
		chromedp.Flag("hide-scrollbars", !b.opts.Headful),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("remote-debugging-port", strconv.Itoa(port)),
		chromedp.UserDataDir(userDataDir),
	)
	if b.opts.ViewportW > 0 && b.opts.ViewportH > 0 {
		allocOpts = append(allocOpts, chromedp.WindowSize(b.opts.ViewportW, b.opts.ViewportH))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	browserCtx, browserStop := chromedp.NewContext(allocCtx)

	b.allocCancel = allocCancel
	b.browserCtx = browserCtx
	b.browserStop = browserStop

	// Capture console + network the moment the target is live so a
	// bug that fires on first paint isn't missed.
	chromedp.ListenTarget(browserCtx, b.onEvent)

	if err := chromedp.Run(browserCtx); err != nil {
		b.Close()
		return fmt.Errorf("autotest: launch chrome: %w (install Chrome/Chromium and ensure it's on PATH)", err)
	}

	// Emulate the picked device viewport + pixel ratio so RN-web
	// renders at the tablet/phone size the user selected, not the
	// host window's size.
	if b.opts.ViewportW > 0 && b.opts.ViewportH > 0 {
		_ = chromedp.Run(browserCtx, emulation.SetDeviceMetricsOverride(
			int64(b.opts.ViewportW), int64(b.opts.ViewportH), b.opts.DPR, true,
		))
	}
	return nil
}

// onEvent buffers warn/error console lines and network responses. Ring
// is bounded so a chatty page can't grow memory without limit.
func (b *cdpBackend) onEvent(ev interface{}) {
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		level := string(e.Type)
		if level != "warning" && level != "error" && level != "assert" {
			return
		}
		msg := ""
		for _, a := range e.Args {
			if a.Value != nil {
				msg += string(a.Value) + " "
			}
		}
		b.mu.Lock()
		b.console = appendCapped(b.console, ConsoleMsg{Level: level, Text: msg, At: time.Now().UTC()})
		b.mu.Unlock()
	case *runtime.EventExceptionThrown:
		text := ""
		if e.ExceptionDetails != nil {
			text = e.ExceptionDetails.Text
		}
		b.mu.Lock()
		b.console = appendCapped(b.console, ConsoleMsg{Level: "error", Text: text, At: time.Now().UTC()})
		b.mu.Unlock()
	case *cdpnetwork.EventResponseReceived:
		if e.Response == nil {
			return
		}
		b.mu.Lock()
		b.network = appendCapped(b.network, NetEvent{
			URL:    e.Response.URL,
			Method: methodFromHeaders(e.Response.RequestHeaders),
			Status: e.Response.Status,
			At:     time.Now().UTC(),
		})
		b.mu.Unlock()
	}
}

// methodFromHeaders pulls the request verb out of the response's
// echoed request headers when present. CDP's EventResponseReceived
// doesn't carry the method directly; the header echo is the cheapest
// reliable source without also wiring EventRequestWillBeSent state.
func methodFromHeaders(h cdpnetwork.Headers) string {
	if h == nil {
		return ""
	}
	if m, ok := h[":method"].(string); ok {
		return m
	}
	return ""
}

// parseSnapshotJSON unmarshals the JS-side DOM walk result. The page
// returns a JSON string (not a JS object) so chromedp doesn't have to
// deep-marshal a DOM-derived structure.
func parseSnapshotJSON(raw string) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Snapshot{}, fmt.Errorf("autotest: parse dom snapshot: %w", err)
	}
	return s, nil
}

func appendCapped[T any](s []T, v T) []T {
	const cap = 500
	s = append(s, v)
	if len(s) > cap {
		s = s[len(s)-cap:]
	}
	return s
}

func (b *cdpBackend) Navigate(ctx context.Context, url string) error {
	return chromedp.Run(b.browserCtx, chromedp.Navigate(url))
}

// domSnapshotJS walks the live DOM and returns the interactable
// elements with a stable selector for each. Preference order for the
// selector mirrors how RN-web emits identity: testID
// (data-testid) > id > aria-label > a structural :nth-of-type path.
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

func (b *cdpBackend) Snapshot(ctx context.Context) (Snapshot, error) {
	var raw string
	if err := chromedp.Run(b.browserCtx, chromedp.Evaluate(domSnapshotJS, &raw)); err != nil {
		return Snapshot{}, fmt.Errorf("autotest: dom snapshot: %w", err)
	}
	return parseSnapshotJSON(raw)
}

func (b *cdpBackend) Click(ctx context.Context, selector string) error {
	return chromedp.Run(b.browserCtx,
		chromedp.Click(selector, chromedp.ByQuery, chromedp.NodeVisible))
}

func (b *cdpBackend) Fill(ctx context.Context, selector, value string) error {
	return chromedp.Run(b.browserCtx,
		chromedp.SendKeys(selector, value, chromedp.ByQuery))
}

func (b *cdpBackend) VisibleText(ctx context.Context, selector string) (string, error) {
	if strings.TrimSpace(selector) == "" {
		selector = "body"
	}
	var text string
	if err := chromedp.Run(b.browserCtx, chromedp.Text(selector, &text, chromedp.ByQuery)); err != nil {
		return "", err
	}
	return text, nil
}

func (b *cdpBackend) Screenshot(ctx context.Context) ([]byte, error) {
	var buf []byte
	if err := chromedp.Run(b.browserCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return nil, fmt.Errorf("autotest: screenshot: %w", err)
	}
	return buf, nil
}

func (b *cdpBackend) Console() []ConsoleMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ConsoleMsg(nil), b.console...)
}

func (b *cdpBackend) Network() []NetEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]NetEvent(nil), b.network...)
}

func (b *cdpBackend) Close() {
	if b.browserStop != nil {
		b.browserStop()
		b.browserStop = nil
	}
	if b.allocCancel != nil {
		b.allocCancel()
		b.allocCancel = nil
	}
}
