package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AutoFix log.
//
// For a solo developer there is no "approval" — the dev IS the
// approver, and an approval queue would just be busywork. The
// autonomous fix loop *applies* high-confidence patches directly and
// records them here so the dev can see what changed and roll back if
// they disagree. Think of it as a per-project undo stack for the
// loop's work.
//
// Persisted to <spec>/.yaver-test-results/autofix.jsonl. Append-only,
// rotates after 5MB.

// AutoFixState is what we did with the proposed change.
type AutoFixState string

const (
	// AutoFixApplied — the loop applied this fix automatically because
	// confidence was high enough. Default state.
	AutoFixApplied AutoFixState = "applied"

	// AutoFixRolledBack — the dev hit Undo from one of their surfaces
	// (mobile, desktop, web), and the loop reverted whatever it did.
	AutoFixRolledBack AutoFixState = "rolled_back"

	// AutoFixSkipped — the loop wanted to fix this but bailed (low
	// confidence, no proposal, etc). Recorded so the dev sees "the
	// loop noticed but didn't touch this."
	AutoFixSkipped AutoFixState = "skipped"
)

// AutoFix is one entry in the log. The dev's surfaces render this
// list as a timeline with an Undo button on each row.
type AutoFix struct {
	ID         string       `json:"id"`
	State      AutoFixState `json:"state"`
	CreatedAt  time.Time    `json:"created_at"`
	UndoneAt   *time.Time   `json:"undone_at,omitempty"`

	// What the loop touched.
	SpecName    string  `json:"spec_name"`
	SpecPath    string  `json:"spec_path"`
	Strategy    string  `json:"strategy"` // "selector_replace" | "code_edit" | "snapshot_rebase"
	Description string  `json:"description"`
	Notes       string  `json:"notes,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`

	// Optional rich payload depending on Strategy. The runner uses
	// these to undo the change: writing OldValue back over NewValue
	// in the spec file is enough for selector swaps.
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
	Diff     string `json:"diff,omitempty"`
}

// AutoFixLog is the agent-process-wide log. One per project keyed by
// spec root path.
type AutoFixLog struct {
	mu      sync.Mutex
	file    string
	entries []AutoFix
}

// NewAutoFixLog loads the log from disk if a file exists.
func NewAutoFixLog(specRoot string) *AutoFixLog {
	l := &AutoFixLog{file: AutoFixLogPath(specRoot)}
	l.load()
	return l
}

// AutoFixLogPath is the canonical persistence file for a given spec
// root. Used by tests and HTTP handlers.
func AutoFixLogPath(specRoot string) string {
	return filepath.Join(specRoot, ".yaver-test-results", "autofix.jsonl")
}

func (l *AutoFixLog) load() {
	data, err := os.ReadFile(l.file)
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var a AutoFix
		if err := json.Unmarshal([]byte(line), &a); err == nil {
			l.entries = append(l.entries, a)
		}
	}
}

func (l *AutoFixLog) persist() {
	if l.file == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.file), 0o755); err != nil {
		return
	}
	if info, err := os.Stat(l.file); err == nil && info.Size() > 5*1024*1024 {
		_ = os.Rename(l.file, l.file+".old")
	}
	f, err := os.Create(l.file)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range l.entries {
		_ = enc.Encode(e)
	}
}

// Record adds a new autofix entry. Defaults state to AutoFixApplied
// since the autonomous loop only writes here after it has actually
// applied the fix. Returns the stored entry.
func (l *AutoFixLog) Record(a AutoFix) AutoFix {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a.ID == "" {
		a.ID = fmt.Sprintf("af-%d", time.Now().UnixNano())
	}
	if a.State == "" {
		a.State = AutoFixApplied
	}
	a.CreatedAt = time.Now()
	l.entries = append(l.entries, a)
	l.persist()
	return a
}

// List returns all log entries newest first.
func (l *AutoFixLog) List(n int) []AutoFix {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AutoFix, len(l.entries))
	copy(out, l.entries)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// MarkUndone flips an entry to AutoFixRolledBack. The actual file
// edits to undo the change are the caller's job — this only records
// the fact.
func (l *AutoFixLog) MarkUndone(id string) (*AutoFix, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.entries {
		if l.entries[i].ID != id {
			continue
		}
		if l.entries[i].State == AutoFixRolledBack {
			return nil, fmt.Errorf("autofix %s already rolled back", id)
		}
		now := time.Now()
		l.entries[i].State = AutoFixRolledBack
		l.entries[i].UndoneAt = &now
		l.persist()
		cp := l.entries[i]
		return &cp, nil
	}
	return nil, fmt.Errorf("autofix not found: %s", id)
}

// AppliedCount returns the number of entries currently in the
// AutoFixApplied state. Used by mobile/desktop/web for "N recent
// fixes" badges.
func (l *AutoFixLog) AppliedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.entries {
		if e.State == AutoFixApplied {
			n++
		}
	}
	return n
}
