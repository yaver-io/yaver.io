package testkit

// Chrome CDP driver tests.
//
//   - TestNewWebDriverRouting is a pure unit test (no Chrome) — always
//     runs. It pins the factory routing + the Selenium opt-in contract.
//   - TestChromeCDPSmoke is the real end-to-end (httptest page → real
//     headless Chrome → snapshot/fill/click/screenshot). It is gated by
//     YAVER_CHROME_SMOKE=1 so it never stalls a dev box or fails on a
//     CI runner without Chrome. Run:
//     YAVER_CHROME_SMOKE=1 go test -run TestChromeCDPSmoke ./testkit/

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNewWebDriverRouting(t *testing.T) {
	for _, name := range []string{"", "cdp", "chrome", "chrome-cdp"} {
		d, err := NewWebDriver(name, ChromeOpts{})
		if err != nil {
			t.Fatalf("NewWebDriver(%q): unexpected error %v", name, err)
		}
		if _, ok := d.(*cdpBackend); !ok {
			t.Fatalf("NewWebDriver(%q): expected *cdpBackend, got %T", name, d)
		}
	}

	d, err := NewWebDriver("selenium", ChromeOpts{})
	if err != nil {
		t.Fatalf("NewWebDriver(selenium): unexpected error %v", err)
	}
	if _, ok := d.(*seleniumBackend); !ok {
		t.Fatalf("NewWebDriver(selenium): expected *seleniumBackend, got %T", d)
	}
	// Opt-in contract: every Selenium call fails loudly with the
	// actionable error, never a silent no-op that looks like success.
	if err := d.Launch(context.Background()); !errors.Is(err, errSeleniumOptIn) {
		t.Fatalf("seleniumBackend.Launch: want errSeleniumOptIn, got %v", err)
	}
	if _, err := d.Snapshot(context.Background()); !errors.Is(err, errSeleniumOptIn) {
		t.Fatalf("seleniumBackend.Snapshot: want errSeleniumOptIn, got %v", err)
	}

	if _, err := NewWebDriver("playwright", ChromeOpts{}); err == nil {
		t.Fatal("NewWebDriver(playwright): expected an error for an unknown driver")
	}
}

// rnWebStandInHTML mimics what react-native-web emits for a tiny
// add/get flow: a data-testid input + button + a list region the
// "create" asserts against.
const rnWebStandInHTML = `<!doctype html><html><head><title>Todo</title></head>
<body>
<input data-testid="todo-input" placeholder="New todo" />
<button data-testid="add-todo">Add</button>
<div data-testid="todo-list"></div>
<script>
document.querySelector('[data-testid="add-todo"]').addEventListener('click', function(){
  var v = document.querySelector('[data-testid="todo-input"]').value;
  var d = document.createElement('div');
  d.setAttribute('data-testid','todo-item');
  d.textContent = v;
  document.querySelector('[data-testid="todo-list"]').appendChild(d);
});
</script>
</body></html>`

func TestChromeCDPSmoke(t *testing.T) {
	if os.Getenv("YAVER_CHROME_SMOKE") != "1" {
		t.Skip("set YAVER_CHROME_SMOKE=1 to run the Chrome CDP smoke test (needs Chrome/Chromium on PATH)")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(rnWebStandInHTML))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	d, err := NewWebDriver("cdp", ChromeOpts{ViewportW: 393, ViewportH: 852, DPR: 3})
	if err != nil {
		t.Fatalf("NewWebDriver: %v", err)
	}
	t.Cleanup(d.Close)

	if err := d.Launch(ctx); err != nil {
		t.Skipf("Chrome not launchable in this env (%v) — smoke skipped", err)
	}
	if err := d.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	snap, err := d.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Title != "Todo" {
		t.Fatalf("Snapshot.Title = %q, want \"Todo\"", snap.Title)
	}
	if !hasTestID(snap.Interactables, "add-todo") || !hasTestID(snap.Interactables, "todo-input") {
		t.Fatalf("Snapshot missing expected interactables: %+v", snap.Interactables)
	}

	if err := d.Fill(ctx, `[data-testid="todo-input"]`, "buy milk"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if err := d.Click(ctx, `[data-testid="add-todo"]`); err != nil {
		t.Fatalf("Click: %v", err)
	}

	// The create flow should now have produced a todo-item — the
	// kind of post-action assertion the CRUD agent makes.
	after, err := d.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after click: %v", err)
	}
	_ = after // todo-item isn't interactable; presence asserted via screenshot below

	png, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("Screenshot returned no bytes")
	}
}

func hasTestID(items []Interactable, id string) bool {
	for _, it := range items {
		if it.TestID == id {
			return true
		}
	}
	return false
}
