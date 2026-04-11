package main

// statuspage.go — single-file status dashboard that consumes the
// data the Monitor features are already capturing (uptime checks,
// releases, errors). No JS, no external assets, safe to commit
// or email. Two entry points:
//
//   1. `yaver status-page render [-o out.html]` writes the page
//      to disk. Use this in a cron to publish to a static host.
//
//   2. `GET /statuspage` serves the same content live through
//      the agent's HTTP server (still behind auth — no public
//      exposure by default). Flip `--public-statuspage` on
//      `yaver serve` to allow unauthenticated reads.
//
// The idea: a solo dev's "status.example.com" with zero SaaS.

import (
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type statusPageData struct {
	GeneratedAt string
	Origin      string
	Monitors    []*Monitor
	Releases    map[string]*ReleaseManifest
	ErrorsStats map[string]int
	Events      int
	Machine     MachineHealth
	Peers       []*PeerState
}

func buildStatusPageData() statusPageData {
	data := statusPageData{
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05 MST"),
		Origin:      syncOrigin(),
		Releases:    map[string]*ReleaseManifest{},
	}
	if list, err := loadMonitors(); err == nil {
		data.Monitors = list
	}
	for _, channel := range []string{"production", "staging", "canary"} {
		if m, err := loadManifest(channel); err == nil && len(m.Releases) > 0 {
			data.Releases[channel] = m
		}
	}
	if store := GlobalErrorStore(); store != nil {
		data.ErrorsStats = store.Stats()
	}
	data.Events = len(analyticsTail(0, 1000))

	// Machine health snapshot + peers. We take a read lock so
	// the status page render doesn't fight the scanner.
	machineHealthMu.RLock()
	data.Machine = machineHealth
	machineHealthMu.RUnlock()
	data.Peers = globalPeerWatcher().Snapshot()
	return data
}

// Render writes the status page to the given path. Overwrites
// any existing file.
func RenderStatusPage(path string) error {
	if path == "" {
		dir, err := ConfigDir()
		if err != nil {
			return err
		}
		out := filepath.Join(dir, "statuspage")
		if err := os.MkdirAll(out, 0755); err != nil {
			return err
		}
		path = filepath.Join(out, "index.html")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	tmpl, err := template.New("statuspage").Funcs(statusPageFuncs).Parse(statusPageTemplate)
	if err != nil {
		return err
	}
	return tmpl.Execute(f, buildStatusPageData())
}

var statusPageFuncs = template.FuncMap{
	"upOk": func(state string) template.HTML {
		switch state {
		case "up":
			return template.HTML(`<span class="up">operational</span>`)
		case "down":
			return template.HTML(`<span class="down">OUTAGE</span>`)
		case "paused":
			return template.HTML(`<span class="muted">paused</span>`)
		default:
			return template.HTML(`<span class="muted">unknown</span>`)
		}
	},
	"timeAgoMs": func(iso string) string {
		if iso == "" {
			return "never"
		}
		t, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return iso
		}
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return fmt.Sprintf("%ds ago", int(d.Seconds()))
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		}
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	},
}

const statusPageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Yaver — status</title>
<style>
  :root { color-scheme: light dark; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    max-width: 840px;
    margin: 2rem auto;
    padding: 0 1rem;
    line-height: 1.5;
  }
  header h1 { margin-bottom: 0.25rem; }
  header p { color: #888; margin-top: 0; }
  section { border: 1px solid rgba(128,128,128,0.25); border-radius: 10px; padding: 1rem 1.2rem; margin: 1.2rem 0; }
  h2 { margin-top: 0; font-size: 1.1rem; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 0.45rem 0.6rem; border-bottom: 1px solid rgba(128,128,128,0.15); font-size: 0.9rem; }
  th { color: #666; font-weight: 600; }
  .up { color: #16a34a; font-weight: 600; }
  .down { color: #dc2626; font-weight: 600; }
  .muted { color: #888; }
  .stats { display: flex; gap: 2rem; margin-top: 0.5rem; }
  .stat { text-align: center; }
  .stat-value { font-size: 1.6rem; font-weight: 700; }
  .stat-label { color: #888; font-size: 0.8rem; }
  footer { color: #888; font-size: 0.75rem; margin-top: 2rem; text-align: center; }
</style>
</head>
<body>

<header>
  <h1>Status</h1>
  <p>Generated {{.GeneratedAt}} · origin {{.Origin}}</p>
</header>

<section>
  <h2>Uptime</h2>
  {{if .Monitors}}
  <table>
    <thead><tr><th>Service</th><th>State</th><th>URL</th><th>Last check</th></tr></thead>
    <tbody>
      {{range .Monitors}}
      <tr>
        <td>{{.Name}}</td>
        <td>{{if .Paused}}{{upOk "paused"}}{{else}}{{upOk .State}}{{end}}</td>
        <td class="muted">{{.URL}}</td>
        <td class="muted">{{timeAgoMs .LastCheckAt}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{else}}
  <p class="muted">No monitors configured. <code>yaver monitor add https://example.com</code></p>
  {{end}}
</section>

<section>
  <h2>Releases</h2>
  {{if .Releases}}
  {{range $channel, $manifest := .Releases}}
  <p><strong>{{$channel}}</strong> &mdash; latest <code>{{$manifest.Latest}}</code> · rollout {{$manifest.RolloutPercent}}%</p>
  {{end}}
  {{else}}
  <p class="muted">No OTA releases yet. <code>yaver release publish --channel production</code></p>
  {{end}}
</section>

<section>
  <h2>Errors</h2>
  <div class="stats">
    <div class="stat"><div class="stat-value">{{index .ErrorsStats "open"}}</div><div class="stat-label">open</div></div>
    <div class="stat"><div class="stat-value">{{index .ErrorsStats "openLast24h"}}</div><div class="stat-label">last 24h</div></div>
    <div class="stat"><div class="stat-value">{{index .ErrorsStats "resolved"}}</div><div class="stat-label">resolved</div></div>
    <div class="stat"><div class="stat-value">{{index .ErrorsStats "totalDistinct"}}</div><div class="stat-label">distinct</div></div>
  </div>
</section>

<section>
  <h2>Events</h2>
  <p class="muted">{{.Events}} recent business events ingested through yaver.track().</p>
</section>

{{if .Machine.Hostname}}
<section>
  <h2>Machine: {{.Machine.Hostname}} ({{.Machine.OS}})</h2>
  <p class="muted">Last scan {{timeAgoMs .Machine.UpdatedAt}}</p>
  {{if .Machine.Alerts}}
  <div style="margin-top: 0.5rem;">
    {{range .Machine.Alerts}}
    <p style="color: #dc2626; font-weight: 600;">⚠ {{.}}</p>
    {{end}}
  </div>
  {{end}}
  {{if .Machine.Filesystems}}
  <table>
    <thead><tr><th>Mount</th><th>Used</th><th>Free</th><th>%</th></tr></thead>
    <tbody>
      {{range .Machine.Filesystems}}
      <tr>
        <td>{{.Mount}}</td>
        <td>{{printf "%.1f" .UsedGB}} GB</td>
        <td>{{printf "%.1f" .FreeGB}} GB</td>
        <td>{{printf "%.0f" .UsedPct}}%</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{end}}
  {{if .Machine.Drives}}
  <h3 style="margin-top: 1rem; font-size: 0.95rem;">SMART</h3>
  <table>
    <thead><tr><th>Device</th><th>Model</th><th>Health</th><th>Temp</th></tr></thead>
    <tbody>
      {{range .Machine.Drives}}
      <tr>
        <td><code>{{.Device}}</code></td>
        <td class="muted">{{.Model}}</td>
        <td>{{if eq .Health "passed"}}<span class="up">OK</span>{{else if eq .Health "failing"}}<span class="down">FAILING</span>{{else}}<span class="muted">unknown</span>{{end}}</td>
        <td class="muted">{{if gt .TemperatureC 0}}{{.TemperatureC}}°C{{end}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{end}}
</section>
{{end}}

{{if .Peers}}
<section>
  <h2>Peer heartbeats</h2>
  <table>
    <thead><tr><th>Device</th><th>State</th><th>Last seen</th></tr></thead>
    <tbody>
      {{range .Peers}}
      <tr>
        <td>{{if .Name}}{{.Name}}{{else}}<code>{{.DeviceID}}</code>{{end}}</td>
        <td>{{if eq .State "online"}}<span class="up">online</span>{{else if eq .State "offline"}}<span class="down">OFFLINE</span>{{else}}<span class="muted">stale</span>{{end}}</td>
        <td class="muted">{{timeAgoMs .LastSeen}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
</section>
{{end}}

<footer>
  Self-hosted by Yaver &middot; no JS, no external assets, no vendor &middot;
  <a href="https://yaver.io">yaver.io</a>
</footer>

</body>
</html>
`

// --- HTTP ----------------------------------------------------------

// handleStatusPage serves the live status page through the agent.
// Reuses the templating path so the rendered HTML always reflects
// current state on every hit.
func (s *HTTPServer) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	tmpl, err := template.New("statuspage").Funcs(statusPageFuncs).Parse(statusPageTemplate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=15")
	_ = tmpl.Execute(w, buildStatusPageData())
}

// --- CLI -----------------------------------------------------------

func runStatusPage(args []string) {
	if len(args) == 0 {
		printStatusPageUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "render":
		statusPageRenderCmd(args[1:])
	case "help", "--help", "-h":
		printStatusPageUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown status-page subcommand: %s\n\n", args[0])
		printStatusPageUsage()
		os.Exit(1)
	}
}

func printStatusPageUsage() {
	fmt.Print(`Yaver status-page — single-file dashboard from your own agent data.

Usage:
  yaver status-page render [-o out.html]

Consumes local monitor, release, error, and event state and
writes a standalone HTML file. No JS, no external assets. Serve
it from any static host, commit it next to a release, or email
it to your stakeholders.
`)
}

func statusPageRenderCmd(args []string) {
	fs := flag.NewFlagSet("status-page render", flag.ExitOnError)
	out := fs.String("o", "", "output HTML file (default: ~/.yaver/statuspage/index.html)")
	fs.Parse(args)
	if err := RenderStatusPage(*out); err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
		os.Exit(1)
	}
	target := *out
	if target == "" {
		dir, _ := ConfigDir()
		target = filepath.Join(dir, "statuspage", "index.html")
	}
	fmt.Printf("✓ wrote %s\n", target)
}
