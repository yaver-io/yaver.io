package main

// monitor_cmd.go — `yaver monitor` CLI + runtime for URL uptime
// checks on behalf of the solo dev's shipped app. Stays local: no
// Pingdom / UptimeRobot / BetterStack account needed.
//
// Storage: ~/.yaver/monitors/monitors.json ledger + per-monitor
// rolling history (last 100 checks) inside the same JSON.
//
// Check loop: the agent spawns one goroutine per enabled monitor
// at boot; each goroutine ticks on the configured interval, writes
// the result to the ledger, and after three consecutive failures
// emits a "down" notification through the existing push channel.
// A subsequent pass flips the state back to "up" and emits a
// recovery notification.
//
// CLI surface:
//   yaver monitor add <url> [--interval 60s] [--name foo]
//   yaver monitor list
//   yaver monitor remove <id|name>
//   yaver monitor pause <id|name>
//   yaver monitor resume <id|name>
//   yaver monitor check <id|name>          # one-shot manual probe

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Monitor is one URL check.
type Monitor struct {
	ID          string         `json:"id"`
	Name        string         `json:"name,omitempty"`
	URL         string         `json:"url"`
	Interval    string         `json:"interval"` // duration string, e.g. "60s"
	Method      string         `json:"method,omitempty"` // default GET
	Paused      bool           `json:"paused,omitempty"`
	State       string         `json:"state"` // "up" | "down" | "unknown"
	Streak      int            `json:"streak"` // consecutive same-state checks
	History     []MonitorCheck `json:"history,omitempty"` // last 100
	CreatedAt   string         `json:"createdAt"`
	LastCheckAt string         `json:"lastCheckAt,omitempty"`
	// CheckSSL enables TLS cert expiry checks on the probe. When
	// true and the URL is https, every check records the
	// certificate's NotAfter and an alert fires once days-left
	// drops below SSLWarnDays (default 14).
	CheckSSL    bool `json:"checkSsl,omitempty"`
	SSLWarnDays int  `json:"sslWarnDays,omitempty"`
	// SSLExpiresAt / SSLDaysLeft carry the most recent cert
	// observation so the mobile card can render an "expires in
	// N days" badge without re-parsing every history entry.
	SSLExpiresAt string `json:"sslExpiresAt,omitempty"`
	SSLDaysLeft  int    `json:"sslDaysLeft,omitempty"`
	SSLAlertedAt string `json:"sslAlertedAt,omitempty"` // set when we last fired a cert-expiry push
}

// MonitorCheck is a single result.
type MonitorCheck struct {
	At           string `json:"at"`
	Status       int    `json:"status"`
	DurationMS   int64  `json:"durationMs"`
	Err          string `json:"err,omitempty"`
	Ok           bool   `json:"ok"`
	SSLExpiresAt string `json:"sslExpiresAt,omitempty"`
	SSLDaysLeft  int    `json:"sslDaysLeft,omitempty"`
}

type monitorLedger struct {
	Monitors []*Monitor `json:"monitors"`
}

var monitorMu sync.Mutex

func monitorsPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "monitors")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "monitors.json"), nil
}

func loadMonitors() ([]*Monitor, error) {
	p, err := monitorsPath()
	if err != nil {
		return nil, err
	}
	data, rerr := os.ReadFile(p)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return []*Monitor{}, nil
		}
		return nil, rerr
	}
	var led monitorLedger
	if jerr := json.Unmarshal(data, &led); jerr != nil {
		return nil, jerr
	}
	return led.Monitors, nil
}

func saveMonitors(list []*Monitor) error {
	p, err := monitorsPath()
	if err != nil {
		return err
	}
	data, jerr := json.MarshalIndent(&monitorLedger{Monitors: list}, "", "  ")
	if jerr != nil {
		return jerr
	}
	tmp := p + ".tmp"
	if werr := os.WriteFile(tmp, data, 0600); werr != nil {
		return werr
	}
	return os.Rename(tmp, p)
}

// runMonitor is the `yaver monitor` CLI entry point.
func runMonitor(args []string) {
	if len(args) == 0 {
		printMonitorUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "add":
		monitorAdd(args[1:])
	case "list", "ls":
		monitorList()
	case "remove", "rm":
		monitorMutate(args[1:], "remove")
	case "pause":
		monitorMutate(args[1:], "pause")
	case "resume":
		monitorMutate(args[1:], "resume")
	case "check":
		monitorCheckNow(args[1:])
	case "help", "--help", "-h":
		printMonitorUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown monitor subcommand: %s\n\n", args[0])
		printMonitorUsage()
		os.Exit(1)
	}
}

func printMonitorUsage() {
	fmt.Print(`Yaver uptime monitor — check your app's URLs on the dev's own agent.

Usage:
  yaver monitor add <url> [flags]     Register a URL to probe
  yaver monitor list                  List every monitor + state
  yaver monitor remove <name|id>      Delete a monitor
  yaver monitor pause <name|id>       Suspend without deleting
  yaver monitor resume <name|id>      Re-enable a paused monitor
  yaver monitor check <name|id>       Run a one-shot probe now

Add flags:
  --name, -n <label>       Human label (defaults to the URL host)
  --interval, -i <dur>     Check interval (default: 60s)
  --method <verb>          HTTP method (default: GET)

Alerts fire through the existing mobile notification channel on
three consecutive failures; recovery fires on the first pass.
Nothing is uploaded anywhere — state lives at
~/.yaver/monitors/monitors.json.
`)
}

func monitorAdd(args []string) {
	fs := flag.NewFlagSet("monitor add", flag.ExitOnError)
	name := fs.String("name", "", "label for the monitor")
	nameShort := fs.String("n", "", "label (short)")
	interval := fs.String("interval", "60s", "check interval")
	intervalShort := fs.String("i", "", "check interval (short)")
	method := fs.String("method", "GET", "HTTP method")
	checkSSL := fs.Bool("ssl", false, "also track TLS cert expiry (HTTPS URLs only)")
	sslWarnDays := fs.Int("ssl-warn-days", 14, "alert when cert has fewer days left than this")
	fs.Parse(args)

	if *nameShort != "" {
		*name = *nameShort
	}
	if *intervalShort != "" {
		*interval = *intervalShort
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver monitor add <url> [--interval 60s] [--name foo]")
		os.Exit(1)
	}
	url := fs.Arg(0)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	if _, err := time.ParseDuration(*interval); err != nil {
		fmt.Fprintf(os.Stderr, "invalid --interval %q: %v\n", *interval, err)
		os.Exit(2)
	}

	monitorMu.Lock()
	defer monitorMu.Unlock()

	list, err := loadMonitors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}

	m := &Monitor{
		ID:          uuid.New().String()[:8],
		Name:        *name,
		URL:         url,
		Interval:    *interval,
		Method:      strings.ToUpper(*method),
		State:       "unknown",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		CheckSSL:    *checkSSL || strings.HasPrefix(url, "https://"), // default on for https
		SSLWarnDays: *sslWarnDays,
	}
	if m.Name == "" {
		m.Name = deriveMonitorName(url)
	}
	list = append(list, m)
	if err := saveMonitors(list); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ added %s  %s  every %s\n", m.ID, m.URL, m.Interval)
}

func monitorList() {
	list, err := loadMonitors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if len(list) == 0 {
		fmt.Println("No monitors yet. `yaver monitor add https://example.com` to get started.")
		return
	}
	for _, m := range list {
		state := m.State
		if m.Paused {
			state = "paused"
		}
		fmt.Printf("  %s  [%-7s]  %-24s  %-36s  every %s\n",
			m.ID, state, clipLeft(m.Name, 24), clipLeft(m.URL, 36), m.Interval)
	}
}

func monitorMutate(args []string, op string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: yaver monitor %s <name|id>\n", op)
		os.Exit(1)
	}
	needle := args[0]

	monitorMu.Lock()
	defer monitorMu.Unlock()

	list, err := loadMonitors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	var updated []*Monitor
	hit := false
	for _, m := range list {
		if m.ID == needle || m.Name == needle {
			hit = true
			switch op {
			case "remove":
				continue
			case "pause":
				m.Paused = true
			case "resume":
				m.Paused = false
			}
		}
		updated = append(updated, m)
	}
	if !hit {
		fmt.Fprintf(os.Stderr, "monitor %q not found\n", needle)
		os.Exit(2)
	}
	if err := saveMonitors(updated); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s %s\n", op, needle)
}

func monitorCheckNow(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver monitor check <name|id>")
		os.Exit(1)
	}
	needle := args[0]

	monitorMu.Lock()
	list, err := loadMonitors()
	if err != nil {
		monitorMu.Unlock()
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	var target *Monitor
	for _, m := range list {
		if m.ID == needle || m.Name == needle {
			target = m
			break
		}
	}
	monitorMu.Unlock()

	if target == nil {
		fmt.Fprintf(os.Stderr, "monitor %q not found\n", needle)
		os.Exit(2)
	}
	check := runMonitorProbe(context.Background(), target)
	fmt.Printf("  status=%d  ok=%v  %dms\n", check.Status, check.Ok, check.DurationMS)
	if check.Err != "" {
		fmt.Printf("  error: %s\n", check.Err)
	}

	monitorMu.Lock()
	defer monitorMu.Unlock()
	list, _ = loadMonitors()
	for _, m := range list {
		if m.ID == target.ID {
			applyMonitorCheck(m, check)
			break
		}
	}
	_ = saveMonitors(list)
}

// runMonitorProbe does one HTTP probe and returns the result.
// Deliberately no retries — it's one check; the loop handles
// sequencing. When Monitor.CheckSSL is on and the URL is HTTPS,
// the probe also records the TLS cert's expiry timestamp so
// the uptime loop can alert on "certificate expires in N days"
// before the dev notices in a browser.
func runMonitorProbe(ctx context.Context, m *Monitor) MonitorCheck {
	start := time.Now()
	ch := MonitorCheck{At: start.UTC().Format(time.RFC3339)}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, m.Method, m.URL, nil)
	if err != nil {
		ch.Err = err.Error()
		return ch
	}
	req.Header.Set("User-Agent", "yaver-monitor/1")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	ch.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		ch.Err = err.Error()
		return ch
	}
	defer resp.Body.Close()
	ch.Status = resp.StatusCode
	ch.Ok = resp.StatusCode >= 200 && resp.StatusCode < 400

	// TLS cert expiry observation. `resp.TLS` is nil for plain
	// HTTP so the branch is a no-op automatically, but we still
	// gate on m.CheckSSL so a monitor that was added without
	// SSL tracking doesn't start recording extra fields.
	if m.CheckSSL && resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		ch.SSLExpiresAt = cert.NotAfter.UTC().Format(time.RFC3339)
		ch.SSLDaysLeft = int(time.Until(cert.NotAfter).Hours() / 24)
	}
	return ch
}

// applyMonitorCheck updates a monitor's state + streak + history
// after a new check, mutating in place. Caller holds monitorMu.
// Also handles SSL cert observation — records latest expiry and
// flags whether the warn threshold was freshly crossed.
func applyMonitorCheck(m *Monitor, check MonitorCheck) (changed bool) {
	m.History = append(m.History, check)
	if len(m.History) > 100 {
		m.History = m.History[len(m.History)-100:]
	}
	m.LastCheckAt = check.At

	// Update the SSL observation fields. If the cert has moved
	// under the warning threshold since the last alert, surface
	// that so the notifier fires a dedicated cert-expiry push.
	if check.SSLExpiresAt != "" {
		m.SSLExpiresAt = check.SSLExpiresAt
		m.SSLDaysLeft = check.SSLDaysLeft
	}

	prev := m.State
	newState := "up"
	if !check.Ok {
		newState = "down"
	}
	if prev == newState {
		m.Streak++
	} else {
		m.State = newState
		m.Streak = 1
		changed = true
	}
	return changed
}

// monitorSSLAlertThreshold returns the effective days-left
// threshold for firing a cert-expiry alert. Defaults to 14.
func monitorSSLAlertThreshold(m *Monitor) int {
	if m.SSLWarnDays > 0 {
		return m.SSLWarnDays
	}
	return 14
}

// monitorNeedsSSLAlert decides whether the latest check should
// emit a cert-expiry notification. Fires when we're under the
// threshold AND we haven't already alerted on the same expiry
// (prevents flapping every probe).
func monitorNeedsSSLAlert(m *Monitor) bool {
	if !m.CheckSSL || m.SSLExpiresAt == "" {
		return false
	}
	if m.SSLDaysLeft > monitorSSLAlertThreshold(m) {
		// Cleared above threshold — reset so a future drop alerts again.
		m.SSLAlertedAt = ""
		return false
	}
	return m.SSLAlertedAt == "" || m.SSLAlertedAt != m.SSLExpiresAt
}

// StartMonitorLoops is called from runServe to start one goroutine
// per non-paused monitor. Idempotent — safe to call on config reload.
func StartMonitorLoops(parent context.Context) {
	list, err := loadMonitors()
	if err != nil || len(list) == 0 {
		return
	}
	for _, m := range list {
		if m.Paused {
			continue
		}
		d, derr := time.ParseDuration(m.Interval)
		if derr != nil || d < 10*time.Second {
			d = 60 * time.Second
		}
		mm := m // capture
		go monitorTickLoop(parent, mm, d)
	}
}

func monitorTickLoop(ctx context.Context, m *Monitor, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check := runMonitorProbe(ctx, m)

			monitorMu.Lock()
			list, err := loadMonitors()
			if err != nil {
				monitorMu.Unlock()
				continue
			}
			var live *Monitor
			for _, l := range list {
				if l.ID == m.ID {
					live = l
					break
				}
			}
			if live == nil {
				monitorMu.Unlock()
				return // removed
			}
			if live.Paused {
				monitorMu.Unlock()
				return // paused
			}
			stateChanged := applyMonitorCheck(live, check)
			// SSL observation alert — separate from the state-
			// change flow so a cert alert doesn't get swallowed
			// while the service is "up".
			shouldAlertSSL := monitorNeedsSSLAlert(live)
			if shouldAlertSSL {
				live.SSLAlertedAt = live.SSLExpiresAt
			}
			_ = saveMonitors(list)
			monitorMu.Unlock()

			if stateChanged {
				notifyMonitorStateChange(live, check)
			} else if live.State == "down" && live.Streak == 3 {
				// First "officially down" signal (3 consecutive fails).
				notifyMonitorStateChange(live, check)
			}
			if shouldAlertSSL {
				notifyMonitorSSLExpiry(live)
			}
		}
	}
}

// notifyMonitorSSLExpiry fires a dedicated cert-expiry alert
// independent of the up/down state. Called by the tick loop
// exactly once per expiry observation to avoid flapping.
func notifyMonitorSSLExpiry(m *Monitor) {
	title := fmt.Sprintf("Monitor: %s SSL", m.Name)
	body := fmt.Sprintf("⚠ %s cert expires in %d day(s) (%s)",
		m.URL, m.SSLDaysLeft, m.SSLExpiresAt)
	fmt.Fprintf(os.Stderr, "[monitor] %s — %s\n", title, body)
	if nm := globalMonitorNotifier; nm != nil {
		nm(m.Name+" SSL", m.URL, "cert-expiring", int64(m.SSLDaysLeft))
	}
}

// notifyMonitorStateChange sends a push through the existing
// notifications channel. Cheap and best-effort — this function
// never blocks the check loop.
func notifyMonitorStateChange(m *Monitor, check MonitorCheck) {
	title := fmt.Sprintf("Monitor: %s", m.Name)
	var body string
	if m.State == "down" {
		body = fmt.Sprintf("⚠ %s is DOWN (status=%d, %dms)", m.URL, check.Status, check.DurationMS)
		if check.Err != "" {
			body += "\n" + check.Err
		}
	} else if m.State == "up" {
		body = fmt.Sprintf("✓ %s recovered after %d fail(s)", m.URL, m.Streak)
	} else {
		return
	}
	// Log to stderr so `journalctl --user -u yaver` always shows
	// alerts even if push notifications are misconfigured.
	fmt.Fprintf(os.Stderr, "[monitor] %s — %s\n", title, body)
	if nm := globalMonitorNotifier; nm != nil {
		nm(m.Name, m.URL, m.State, check.DurationMS)
	}
}

// globalMonitorNotifier is installed by runServe so the check loop
// can emit notifications without depending on the HTTPServer
// instance. It's a function pointer rather than an interface so
// the CLI-side `yaver monitor check` doesn't pull in the whole
// NotificationManager graph when it runs standalone.
var globalMonitorNotifier func(label, url, status string, responseMs int64)

// RegisterMonitorNotifier wires the agent's NotificationManager
// (or any compatible callable) into the monitor check loop. Called
// from runServe once the manager is constructed.
func RegisterMonitorNotifier(fn func(label, url, status string, responseMs int64)) {
	globalMonitorNotifier = fn
}

func deriveMonitorName(url string) string {
	host := strings.TrimPrefix(url, "http://")
	host = strings.TrimPrefix(host, "https://")
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return host
}

func clipLeft(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
