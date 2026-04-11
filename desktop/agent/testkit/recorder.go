package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Recorder captures clicks + inputs on a real Chromium window the dev
// drives manually, and emits a yaver-test-sdk YAML spec from them.
//
// CLI: `yaver test record [--out yaver-tests/<name>.test.yaml] [--url http://...]`
//
// Flow:
//
//   1. Run() opens a *headful* Chrome via chromedp.
//   2. Inject a tiny page hook (Runtime.evaluate) that listens to
//      click + change events and reports them back via
//      Runtime.bindingCalled.
//   3. The recorder loop appends each event to an in-memory list of
//      Steps in the canonical step vocabulary.
//   4. When the user closes the browser (or sends SIGINT), Stop()
//      returns the assembled Spec, which the caller writes to disk.
//
// We deliberately use the simplest possible "best-effort" CSS path
// generator (id → tag.classes → nth-of-type fallback). It's enough to
// bootstrap a real spec; the dev refines selectors afterward.

// RecordOptions controls a recording session.
type RecordOptions struct {
	Name string // spec name (also drives the YAML filename)
	URL  string // initial URL to load in the browser
	// OutPath: where to write the YAML. Defaults to
	// yaver-tests/<name>.test.yaml.
	OutPath string
}

// Record opens a headful browser pointed at opts.URL, listens for the
// user's clicks and inputs, and returns when the browser closes.
// Writes the captured spec to opts.OutPath.
func Record(ctx context.Context, opts RecordOptions) (*Spec, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("record: URL is required")
	}
	if opts.Name == "" {
		opts.Name = "recorded"
	}
	if opts.OutPath == "" {
		opts.OutPath = filepath.Join("yaver-tests", opts.Name+".test.yaml")
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("mute-audio", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// Channel collecting recorded steps from the page hook.
	stepCh := make(chan Step, 256)
	defer close(stepCh)

	// Listen for binding callbacks from the page.
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		if e, ok := ev.(*runtime.EventBindingCalled); ok && e.Name == "yaverRecord" {
			var msg recordedEvent
			if err := json.Unmarshal([]byte(e.Payload), &msg); err == nil {
				select {
				case stepCh <- recordedToStep(msg):
				default:
				}
			}
		}
	})

	// Boot the browser and inject the recorder hook on every navigation.
	if err := chromedp.Run(browserCtx,
		runtime.AddBinding("yaverRecord"),
		chromedp.Navigate(opts.URL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := injectRecorderScript(ctx)
			return err
		}),
	); err != nil {
		return nil, fmt.Errorf("record: launch chrome: %w", err)
	}

	// Re-inject the recorder hook on every new execution context (each
	// navigation creates one). The dispatch is cheap and idempotent
	// thanks to the `__yaverRecorderInstalled` guard inside the script.
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		if _, ok := ev.(*runtime.EventExecutionContextCreated); ok {
			go func() {
				_, _ = injectRecorderScript(browserCtx)
			}()
		}
	})

	steps := []Step{
		{Goto: "/"},
	}
	// Drain the channel until the user closes the browser. We treat
	// any chromedp.Run error or context cancellation as the end of the
	// session.
	done := make(chan struct{})
	go func() {
		<-browserCtx.Done()
		close(done)
	}()

drain:
	for {
		select {
		case s := <-stepCh:
			steps = append(steps, s)
		case <-done:
			break drain
		case <-ctx.Done():
			break drain
		}
	}

	spec := &Spec{
		Name:   opts.Name,
		Target: TargetWeb,
		URL:    opts.URL,
		Steps:  steps,
	}
	if err := writeSpecYAML(opts.OutPath, spec); err != nil {
		return spec, fmt.Errorf("record: write yaml: %w", err)
	}
	return spec, nil
}

// recordedEvent is the JSON payload the page hook sends back via the
// runtime binding. Kept tiny to avoid noisy log lines.
type recordedEvent struct {
	Type     string `json:"type"`     // "click" | "input" | "submit"
	Selector string `json:"selector"` // CSS path computed in-browser
	Value    string `json:"value"`    // text value for input events
	Tag      string `json:"tag"`      // tagname for fallback labelling
}

func recordedToStep(e recordedEvent) Step {
	switch e.Type {
	case "click":
		return Step{Click: e.Selector}
	case "input":
		return Step{Fill: &FillStep{Selector: e.Selector, Text: e.Value}}
	}
	return Step{}
}

// injectRecorderScript installs the page-side hook that turns user
// clicks and inputs into Yaver step events. Re-injected on every
// navigation so single-page-app routing keeps working.
func injectRecorderScript(ctx context.Context) (interface{}, error) {
	// Wrap the script in a CSS-path computer + listeners. The result
	// is delivered via the `yaverRecord` binding registered above.
	const script = `(() => {
  if (window.__yaverRecorderInstalled) return;
  window.__yaverRecorderInstalled = true;
  function cssPath(el) {
    if (!el || el.nodeType !== 1) return '';
    if (el.id) return '#' + CSS.escape(el.id);
    const parts = [];
    while (el && el.nodeType === 1 && el !== document.body) {
      let part = el.tagName.toLowerCase();
      if (el.classList && el.classList.length) {
        part += '.' + Array.from(el.classList).slice(0, 2).map(CSS.escape).join('.');
      }
      const parent = el.parentNode;
      if (parent) {
        const siblings = Array.from(parent.children).filter(c => c.tagName === el.tagName);
        if (siblings.length > 1) {
          part += ':nth-of-type(' + (siblings.indexOf(el) + 1) + ')';
        }
      }
      parts.unshift(part);
      el = el.parentNode;
    }
    return parts.join(' > ');
  }
  document.addEventListener('click', (e) => {
    try {
      const sel = cssPath(e.target);
      if (sel && window.yaverRecord) {
        window.yaverRecord(JSON.stringify({type: 'click', selector: sel, tag: e.target.tagName}));
      }
    } catch {}
  }, true);
  document.addEventListener('change', (e) => {
    try {
      const t = e.target;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT')) {
        const sel = cssPath(t);
        if (sel && window.yaverRecord) {
          window.yaverRecord(JSON.stringify({type: 'input', selector: sel, value: t.value || '', tag: t.tagName}));
        }
      }
    } catch {}
  }, true);
})();`
	var ignored interface{}
	err := chromedp.Run(ctx, chromedp.Evaluate(script, &ignored))
	return nil, err
}

// writeSpecYAML emits a hand-readable YAML for the captured spec. We
// don't pull in a YAML marshaller for this — the format is small and
// stable enough that direct fmt.Fprintf produces cleaner output than
// the generic encoder, which would emit a lot of noise.
func writeSpecYAML(path string, spec *Spec) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "# Recorded by `yaver test record` on %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "# Edit selectors to match what's stable on your page; pure auto-recorded\n")
	fmt.Fprintf(f, "# CSS paths can be brittle.\n\n")
	fmt.Fprintf(f, "name: %s\n", spec.Name)
	fmt.Fprintf(f, "target: %s\n", spec.Target)
	fmt.Fprintf(f, "url: %s\n", spec.URL)
	fmt.Fprintln(f, "steps:")
	// Always emit the initial goto / so the spec is runnable from
	// the recorded URL even if the recorder didn't capture a nav.
	fmt.Fprintln(f, "  - goto: /")
	for _, step := range spec.Steps {
		switch {
		case step.Goto != "" && step.Goto != "/":
			fmt.Fprintf(f, "  - goto: %q\n", step.Goto)
		case step.Click != "":
			fmt.Fprintf(f, "  - click: %q\n", step.Click)
		case step.Fill != nil:
			fmt.Fprintf(f, "  - fill:\n")
			fmt.Fprintf(f, "      selector: %q\n", step.Fill.Selector)
			fmt.Fprintf(f, "      text: %q\n", step.Fill.Text)
		}
	}
	return nil
}

// FormatRecordSummary returns a one-line summary of a recording session
// for the CLI to print after Stop().
func FormatRecordSummary(spec *Spec, path string) string {
	clicks := 0
	fills := 0
	for _, s := range spec.Steps {
		if s.Click != "" {
			clicks++
		}
		if s.Fill != nil {
			fills++
		}
	}
	return fmt.Sprintf("Recorded %d click(s), %d input(s) → %s", clicks, fills, path)
}

