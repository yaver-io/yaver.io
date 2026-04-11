package main

// test_report_cmd.go — `yaver test report` — renders the latest
// suite runs from .history.jsonl as a standalone HTML file. Solo
// dev gets a dashboard they can open in a browser without running
// the agent, email to a collaborator, or archive as a per-release
// snapshot.
//
// Explicitly NOT a live service: the output is a single
// self-contained .html file with inlined CSS. No external assets,
// no JS dependencies, no server. `open <file>` and done.

import (
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// runTestReport is the `yaver test report` entry point.
func runTestReport(args []string) {
	fs := flag.NewFlagSet("test report", flag.ExitOnError)
	out := fs.String("o", "", "output HTML file (default: yaver-tests/report.html)")
	limit := fs.Int("limit", 20, "number of recent runs to include")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: yaver test report [path] [-o out.html] [-limit N]

Renders the latest N suite runs from <path>/.history.jsonl as a
single-file HTML report. Open the file in a browser to see the
summary, per-spec results, and error messages. No JS or external
assets — safe to archive, email, or commit to a release.`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	root := "yaver-tests"
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %s does not exist\n", abs)
		os.Exit(2)
	}

	h := &testkit.History{Path: testkit.HistoryPathFor(abs)}
	entries, err := h.Tail(*limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read history: %v\n", err)
		os.Exit(2)
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no history yet — run `yaver test run` first")
		os.Exit(0)
	}

	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(abs, "report.html")
	}
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", outPath, err)
		os.Exit(2)
	}
	defer f.Close()

	tmpl, err := template.New("report").Funcs(reportFuncs).Parse(reportHTMLTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse template: %v\n", err)
		os.Exit(2)
	}
	data := reportData{
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05 MST"),
		Root:        abs,
		Entries:     entries,
	}
	if err := tmpl.Execute(f, data); err != nil {
		fmt.Fprintf(os.Stderr, "render report: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("Wrote %s (%d runs)\n", outPath, len(entries))
}

type reportData struct {
	GeneratedAt string
	Root        string
	Entries     []testkit.HistoryEntry
}

var reportFuncs = template.FuncMap{
	"fmtTime": func(t time.Time) string {
		return t.Format("2006-01-02 15:04:05")
	},
	"fmtDuration": func(ms int64) string {
		d := time.Duration(ms) * time.Millisecond
		if d < time.Second {
			return fmt.Sprintf("%dms", ms)
		}
		return d.Round(10 * time.Millisecond).String()
	},
	"statusBadge": func(passed bool) template.HTML {
		if passed {
			return template.HTML(`<span class="pass">PASS</span>`)
		}
		return template.HTML(`<span class="fail">FAIL</span>`)
	},
	"suiteHealth": func(e testkit.HistoryEntry) template.HTML {
		if e.Failed == 0 {
			return template.HTML(fmt.Sprintf(`<span class="pass">%d/%d</span>`, e.Passed, e.Total))
		}
		return template.HTML(fmt.Sprintf(`<span class="fail">%d/%d</span>`, e.Passed, e.Total))
	},
}

// reportHTMLTemplate is a single-file self-contained report. No
// external CSS, no JS, no web fonts. Opens in any browser, safe to
// email, safe to commit next to a release.
const reportHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>yaver-test-sdk report</title>
<style>
  :root { color-scheme: light dark; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    max-width: 960px;
    margin: 2rem auto;
    padding: 0 1rem;
    line-height: 1.5;
  }
  h1 { margin-bottom: 0.25rem; }
  header p { margin: 0; color: #888; font-size: 0.9rem; }
  .run {
    border: 1px solid rgba(128,128,128,0.25);
    border-radius: 8px;
    padding: 1rem 1.2rem;
    margin: 1.2rem 0;
  }
  .run-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 1rem;
  }
  .run-head h2 { margin: 0; font-size: 1.1rem; }
  .meta { color: #888; font-size: 0.85rem; }
  table { width: 100%; border-collapse: collapse; margin-top: 0.75rem; }
  th, td {
    text-align: left;
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid rgba(128,128,128,0.15);
    font-size: 0.9rem;
  }
  th { font-weight: 600; color: #666; }
  .pass { color: #16a34a; font-weight: 600; }
  .fail { color: #dc2626; font-weight: 600; }
  .err {
    color: #dc2626;
    font-family: "SF Mono", Menlo, monospace;
    font-size: 0.8rem;
    white-space: pre-wrap;
    word-break: break-word;
  }
  footer { color: #888; font-size: 0.8rem; margin-top: 2rem; text-align: center; }
</style>
</head>
<body>
<header>
  <h1>yaver-test-sdk report</h1>
  <p>Root: {{.Root}} &middot; generated {{.GeneratedAt}}</p>
</header>

{{range .Entries}}
<section class="run">
  <div class="run-head">
    <h2>{{fmtTime .StartedAt}}</h2>
    <div>
      {{suiteHealth .}} &middot; {{fmtDuration .DurationMS}}
      {{if .GitBranch}} &middot; <span class="meta">{{.GitBranch}}</span>{{end}}
      {{if .GitSHA}} <span class="meta">({{.GitSHA}})</span>{{end}}
    </div>
  </div>
  <p class="meta">
    Host: {{.HostOS}}
    {{if gt .FlakyCount 0}} &middot; {{.FlakyCount}} flaky{{end}}
  </p>
  <table>
    <thead>
      <tr><th>Status</th><th>Spec</th><th>Target</th><th>Duration</th><th>Attempt</th></tr>
    </thead>
    <tbody>
    {{range .Specs}}
      <tr>
        <td>{{statusBadge .Passed}}</td>
        <td>{{.Name}}</td>
        <td class="meta">{{.Target}}</td>
        <td>{{fmtDuration .DurationMS}}</td>
        <td class="meta">{{.Attempt}}</td>
      </tr>
      {{if .Error}}
      <tr><td colspan="5" class="err">{{.Error}}</td></tr>
      {{end}}
    {{end}}
    </tbody>
  </table>
</section>
{{end}}

<footer>
  yaver-test-sdk &middot; single-file report, no JS, no external assets
</footer>
</body>
</html>
`
