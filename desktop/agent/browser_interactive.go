package main

// browser_interactive.go — generic interactive / human-in-the-loop co-browse.
//
// Streams a headful browser as JPEG frames and relays mouse/keyboard/scroll
// input so a human can solve a captcha or log in remotely. Once the human is
// done, automation resumes against the same persistent session (cookies and
// auth state survive on disk in the per-session profile directory).
//
// This is GENERIC — it has no knowledge of any particular site. It just opens
// a browser, navigates to a URL, and exposes raw input/frame primitives.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// findChromePath discovers a usable Chrome/Chromium executable.
//
// System Chrome may be absent on a dev/CI machine, but a Playwright-managed
// Chromium is often present. We check, in order:
//  1. chromedp's own auto-discovery (return "" to let chromedp find it).
//  2. Playwright cache globs (Linux and macOS).
//  3. exec.LookPath for common binary names.
//
// Returns "" when nothing better than chromedp's default is found, in which
// case the caller should NOT set an explicit ExecPath.
func findChromePath() string {
	// (a) Let chromedp auto-find first if a well-known binary is on PATH or in
	// its default search locations. We only override when we can locate a
	// Playwright Chromium, which chromedp does not know about.
	home, _ := os.UserHomeDir()

	// (b) Playwright cache globs.
	var globs []string
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			globs = append(globs,
				filepath.Join(home, "Library/Caches/ms-playwright/chromium-*/chrome-mac*/Chromium.app/Contents/MacOS/Chromium"),
				filepath.Join(home, "Library/Caches/ms-playwright/chromium-*/chrome-mac*/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"),
			)
		}
	default: // linux and others
		if home != "" {
			globs = append(globs,
				filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux/chrome"),
			)
		}
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
				return m
			}
		}
	}

	// (c) Common binary names on PATH.
	for _, name := range []string{
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}

	// Fall back to chromedp's default discovery.
	return ""
}

// OpenInteractiveSession starts a headful Chrome wired for human-in-the-loop
// co-browse. Like OpenSession but with a persistent profile dir, a real window
// size, automation-detection mitigations, and an explicit ExecPath when a
// browser can be discovered.
func (bm *BrowserManager) OpenInteractiveSession(id, profileDir string, width, height int) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if _, exists := bm.sessions[id]; exists {
		return fmt.Errorf("browser session %q already exists", id)
	}

	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 800
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(width, height),
	)
	if profileDir != "" {
		allocOpts = append(allocOpts, chromedp.UserDataDir(profileDir))
	}
	if chromePath := findChromePath(); chromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(chromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	// Boot Chrome.
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		return fmt.Errorf("launch interactive chrome: %w (install Chrome/Chromium or Playwright Chromium)", err)
	}

	now := time.Now()
	bm.sessions[id] = &BrowserSession{
		ID:            id,
		Headful:       true,
		Interactive:   true,
		ProfileDir:    profileDir,
		ViewW:         width,
		ViewH:         height,
		CreatedAt:     now,
		LastUsedAt:    now,
		allocCancel:   allocCancel,
		browserCtx:    browserCtx,
		browserCancel: browserCancel,
	}

	bm.emit(BrowserEvent{
		Type:      "action",
		SessionID: id,
		Message:   "interactive session opened",
	})

	return nil
}

// FrameJPEG captures the current page as a JPEG (for low-latency streaming).
func (bm *BrowserManager) FrameJPEG(id string, quality int) ([]byte, error) {
	s, err := bm.getSession(id)
	if err != nil {
		return nil, err
	}
	bm.touch(s)

	if quality <= 0 || quality > 100 {
		quality = 55
	}

	var buf []byte
	if err := chromedp.Run(s.browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		b, e := page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatJpeg).
			WithQuality(int64(quality)).
			Do(ctx)
		if e != nil {
			return e
		}
		buf = b
		return nil
	})); err != nil {
		return nil, fmt.Errorf("frame jpeg: %w", err)
	}
	return buf, nil
}

// InjectClick dispatches a real mouse click at viewport coordinates.
func (bm *BrowserManager) InjectClick(id string, x, y float64) error {
	s, err := bm.getSession(id)
	if err != nil {
		return err
	}
	bm.touch(s)
	if err := chromedp.Run(s.browserCtx, chromedp.MouseClickXY(x, y)); err != nil {
		return fmt.Errorf("inject click: %w", err)
	}
	return nil
}

// InjectKeys dispatches keyboard input (raw text or key sequences).
func (bm *BrowserManager) InjectKeys(id, text string) error {
	s, err := bm.getSession(id)
	if err != nil {
		return err
	}
	bm.touch(s)
	if err := chromedp.Run(s.browserCtx, chromedp.KeyEvent(text)); err != nil {
		return fmt.Errorf("inject keys: %w", err)
	}
	return nil
}

// InjectScroll dispatches a mouse-wheel scroll at viewport coordinates.
func (bm *BrowserManager) InjectScroll(id string, x, y, dy float64) error {
	s, err := bm.getSession(id)
	if err != nil {
		return err
	}
	bm.touch(s)
	if err := chromedp.Run(s.browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MouseWheel, x, y).
			WithDeltaX(0).
			WithDeltaY(dy).
			Do(ctx)
	})); err != nil {
		return fmt.Errorf("inject scroll: %w", err)
	}
	return nil
}

// Prefill optionally fills a form field before handing control to the human
// (e.g. pre-populate a username so they only have to solve the captcha).
func (bm *BrowserManager) Prefill(id, selector, value string) error {
	s, err := bm.getSession(id)
	if err != nil {
		return err
	}
	bm.touch(s)
	if err := chromedp.Run(s.browserCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, value, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		return fmt.Errorf("prefill %q: %w", selector, err)
	}
	return nil
}

// InteractiveStatus returns the current URL and title for a session.
func (bm *BrowserManager) InteractiveStatus(id string) (url, title string, err error) {
	s, e := bm.getSession(id)
	if e != nil {
		return "", "", e
	}
	bm.touch(s)
	if e := chromedp.Run(s.browserCtx,
		chromedp.Location(&url),
		chromedp.Title(&title),
	); e != nil {
		return "", "", fmt.Errorf("interactive status: %w", e)
	}
	bm.mu.Lock()
	s.CurrentURL = url
	s.CurrentTitle = title
	bm.mu.Unlock()
	return url, title, nil
}
