package main

import (
	"os"
	"strings"
	"testing"
)

// The browser-window target never launched a browser, on machines that had one.
//
// browserWindowPool.open built browserCtx via chromedp.NewContext, then booted
// with chromedp.Run(bootCtx) where bootCtx descended from the inbound REQUEST
// context. chromedp.Run only accepts a context it created, so it returned
// ErrInvalidContext — surfaced as:
//
//	launch headless chromium: invalid context (install Chrome or Chromium)
//
// Two failures compounded: the boot never happened, and the error blamed a
// missing dependency. On the box this was found on, Chrome was installed at the
// standard macOS path the whole time, so the message sent the reader hunting
// for something that was never absent.
//
// A source-level check is crude, but the alternative is launching a real
// browser in unit tests. What matters is that the context lineage cannot
// silently regress — the runtime failure it produces is indistinguishable from
// "you forgot to install Chrome".
func TestBrowserPoolBootsFromChromedpContext(t *testing.T) {
	src, err := os.ReadFile("remote_runtime_browser.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	code := stripGoLineCommentsForTest(string(src))

	if strings.Contains(code, "context.WithTimeout(ctx, 25*time.Second)") {
		t.Error("boot context derives from the request ctx again — chromedp.Run will " +
			"return ErrInvalidContext and no browser will ever start")
	}
	if !strings.Contains(code, "context.WithTimeout(browserCtx,") {
		t.Error("boot must descend from browserCtx (the chromedp.NewContext result), " +
			"or chromedp.Run cannot drive it")
	}
}

// The "install Chrome" hint must be reserved for an actually-missing browser.
// Attaching it to every failure is what made the context bug so expensive.
func TestBrowserLaunchOnlyBlamesMissingChromeWhenItIsMissing(t *testing.T) {
	src, err := os.ReadFile("remote_runtime_browser.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	code := stripGoLineCommentsForTest(string(src))

	i := strings.Index(code, "install Chrome or Chromium")
	if i < 0 {
		t.Skip("hint removed entirely; nothing to guard")
	}
	// The hint must sit inside a branch that checked for a not-found binary.
	window := code[max0(i-400):i]
	if !strings.Contains(window, "ErrNotFound") && !strings.Contains(window, "executable file not found") {
		t.Error("the 'install Chrome' hint is emitted unconditionally — it must be " +
			"gated on an exec-not-found error, or a context/permission/timeout " +
			"failure gets reported as a missing browser")
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// Local helper so this test does not depend on one living in another _test file.
func stripGoLineCommentsForTest(src string) string {
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if idx := strings.Index(ln, "//"); idx >= 0 {
			lines[i] = ln[:idx]
		}
	}
	return strings.Join(lines, "\n")
}
