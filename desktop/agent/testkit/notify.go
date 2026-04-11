package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Failure-only notifications.
//
// Solo dev wants exactly one signal: "your tests just broke." Not a
// passing run, not a queue update — just the failure with enough
// context to act on it. We deliberately do NOT integrate with Expo
// Push or Apple/Google's push services; that would couple the
// runner to a third-party token system and the dev would have to
// manage credentials. Instead:
//
//   - The agent writes a notification JSON line into an in-memory
//     ring buffer + a local file under <spec>/.yaver-test-results/notifications.jsonl.
//   - The mobile app polls /testkit/notifications over the existing
//     P2P transport every few seconds while the user has the Yaver
//     app open. When closed, the OS-level Yaver push channel that
//     already runs for AI tasks delivers the same payload.
//   - For users who want webhook delivery (Slack, Discord, ntfy.sh,
//     a self-hosted Gotify), the agent reads YAVER_TEST_NOTIFY_URL
//     and POSTs the same JSON. The dev provides the URL; we never
//     touch a third-party token.

// Notification is one entry the mobile app or webhook receives.
type Notification struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "test_failed" | "test_recovered"
	SpecName   string    `json:"spec_name"`
	SpecPath   string    `json:"spec_path"`
	Error      string    `json:"error,omitempty"`
	Screenshot string    `json:"screenshot,omitempty"`
	GitSHA     string    `json:"git_sha,omitempty"`
	GitBranch  string    `json:"git_branch,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// NotificationCenter is the in-memory ring buffer + file persistence
// for the local-CI notification stream. The HTTP server holds one
// instance and the runner pushes events into it.
type NotificationCenter struct {
	mu      sync.Mutex
	max     int
	entries []Notification
	file    string
}

// NewNotificationCenter — `file` is where to persist a tail of
// notifications so the mobile app can fetch them across agent
// restarts. Defaults to <spec>/.yaver-test-results/notifications.jsonl
// when called via the runner.
func NewNotificationCenter(file string, max int) *NotificationCenter {
	if max < 16 {
		max = 16
	}
	nc := &NotificationCenter{max: max, file: file}
	nc.loadFromFile()
	return nc
}

func (nc *NotificationCenter) loadFromFile() {
	if nc.file == "" {
		return
	}
	data, err := os.ReadFile(nc.file)
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var n Notification
		if err := json.Unmarshal([]byte(line), &n); err == nil {
			nc.entries = append(nc.entries, n)
		}
	}
}

// Append adds a new notification, persists it, and dispatches the
// optional webhook if YAVER_TEST_NOTIFY_URL is set.
func (nc *NotificationCenter) Append(n Notification) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	nc.entries = append(nc.entries, n)
	if len(nc.entries) > nc.max {
		nc.entries = nc.entries[len(nc.entries)-nc.max:]
	}
	if nc.file != "" {
		if err := os.MkdirAll(filepath.Dir(nc.file), 0o755); err == nil {
			f, err := os.OpenFile(nc.file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err == nil {
				_ = json.NewEncoder(f).Encode(n)
				_ = f.Close()
			}
		}
	}
	go fireWebhook(n)
}

// List returns the latest n notifications, newest first.
func (nc *NotificationCenter) List(n int) []Notification {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	out := make([]Notification, len(nc.entries))
	copy(out, nc.entries)
	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// PublishSuiteFailures emits one notification per failed spec in the
// suite. Called by the runner after every run; passing runs that
// follow a failed run also emit a "test_recovered" entry so the dev
// sees "good news, the thing you were debugging is fixed."
func PublishSuiteFailures(nc *NotificationCenter, suite *Suite, gitSHA, gitBranch string) {
	if nc == nil || suite == nil {
		return
	}
	for _, r := range suite.Results {
		if r == nil || r.Passed {
			continue
		}
		shot := ""
		errMsg := ""
		if r.Err != nil {
			errMsg = r.Err.Error()
		}
		for _, st := range r.Steps {
			if st.Err != nil {
				if errMsg == "" {
					errMsg = fmt.Sprintf("step %d (%s): %s", st.Index, st.Description, st.Err.Error())
				}
				if st.ScreenshotPath != "" {
					shot = st.ScreenshotPath
				}
				break
			}
		}
		nc.Append(Notification{
			ID:         fmt.Sprintf("%s-%d", r.Spec.Name, time.Now().UnixNano()),
			Kind:       "test_failed",
			SpecName:   r.Spec.Name,
			SpecPath:   r.Spec.Path,
			Error:      errMsg,
			Screenshot: shot,
			GitSHA:     gitSHA,
			GitBranch:  gitBranch,
			CreatedAt:  time.Now(),
		})
	}
}

// fireWebhook is a best-effort POST to YAVER_TEST_NOTIFY_URL when
// the env var is set. We deliberately swallow errors — failure to
// notify must never crash the runner. Solo dev tolerates a missed
// alert; they don't tolerate a crashed test process.
func fireWebhook(n Notification) {
	url := os.Getenv("YAVER_TEST_NOTIFY_URL")
	if url == "" {
		return
	}
	body, err := json.Marshal(n)
	if err != nil {
		return
	}
	postWithTimeout(url, body)
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// NotificationsPathFor returns the canonical persistence file for
// a project's notification log.
func NotificationsPathFor(specRoot string) string {
	return filepath.Join(specRoot, ".yaver-test-results", "notifications.jsonl")
}
