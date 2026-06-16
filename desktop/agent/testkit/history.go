package testkit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// History is the on-disk run log. We deliberately use newline-delimited
// JSON (one suite per line) instead of SQLite so the binary stays small
// and the file is grep-able from the terminal — solo devs love that.
//
// Location: <project root>/yaver-tests/.history.jsonl
//
// Mobile app reads this file via the existing artifact-streaming
// channel; nothing is uploaded to a server.
type History struct {
	Path string
}

// HistoryEntry is one suite run flattened for the log.
type HistoryEntry struct {
	StartedAt  time.Time           `json:"started_at"`
	FinishedAt time.Time           `json:"finished_at"`
	DurationMS int64               `json:"duration_ms"`
	Total      int                 `json:"total"`
	Passed     int                 `json:"passed"`
	Failed     int                 `json:"failed"`
	FlakyCount int                 `json:"flaky_count"`
	GitSHA     string              `json:"git_sha,omitempty"`
	GitBranch  string              `json:"git_branch,omitempty"`
	HostOS     string              `json:"host_os"`
	Specs      []HistorySpecResult `json:"specs"`
}

// HistorySpecResult is the per-spec view written to the log.
type HistorySpecResult struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Target     Target `json:"target"`
	Passed     bool   `json:"passed"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
	Flaky      bool   `json:"flaky,omitempty"`
	Attempt    int    `json:"attempt"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// AppendSuite writes one HistoryEntry derived from `suite` to the log.
// The log file is created on first write, never truncated, and
// silently rotated if it grows beyond 5 MB so it doesn't fill the disk
// of a long-running dev box.
func (h *History) AppendSuite(suite *Suite, gitSHA, gitBranch, hostOS string) error {
	if h.Path == "" {
		return fmt.Errorf("history path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(h.Path), 0o755); err != nil {
		return err
	}

	// Rotate if the file is over 5 MB.
	if info, err := os.Stat(h.Path); err == nil && info.Size() > 5*1024*1024 {
		_ = os.Rename(h.Path, h.Path+".old")
	}

	total, passed, failed := suite.Counts()
	flaky := 0
	specs := make([]HistorySpecResult, 0, len(suite.Results))
	for _, r := range suite.Results {
		if r == nil {
			continue
		}
		hr := HistorySpecResult{
			Name:       r.Spec.Name,
			Path:       r.Spec.Path,
			Target:     r.Spec.Target,
			Passed:     r.Passed,
			Skipped:    r.Skipped,
			SkipReason: r.SkipReason,
			Flaky:      r.Flaky,
			Attempt:    r.Attempt,
			DurationMS: r.Duration().Milliseconds(),
		}
		if r.Err != nil {
			hr.Error = r.Err.Error()
		}
		if r.Flaky {
			flaky++
		}
		specs = append(specs, hr)
	}

	entry := HistoryEntry{
		StartedAt:  suite.StartedAt,
		FinishedAt: suite.FinishedAt,
		DurationMS: suite.FinishedAt.Sub(suite.StartedAt).Milliseconds(),
		Total:      total,
		Passed:     passed,
		Failed:     failed,
		FlakyCount: flaky,
		GitSHA:     gitSHA,
		GitBranch:  gitBranch,
		HostOS:     hostOS,
		Specs:      specs,
	}

	f, err := os.OpenFile(h.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(entry)
}

// Tail returns the most recent n entries (oldest first). Used by the
// `yaver test history` CLI command and the mobile app's "Runs" tab.
func (h *History) Tail(n int) ([]HistoryEntry, error) {
	f, err := os.Open(h.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	all := []HistoryEntry{}
	for scanner.Scan() {
		var e HistoryEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err == nil {
			all = append(all, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// FlakeReport scans the history for specs that have been flaky in the
// last `lookback` runs and returns a sorted list. Used by
// `yaver test flake` so the dev can see "this spec has failed 3 of the
// last 10 runs" without scrolling through CI logs.
func (h *History) FlakeReport(lookback int) ([]FlakeStats, error) {
	entries, err := h.Tail(lookback)
	if err != nil {
		return nil, err
	}
	stats := map[string]*FlakeStats{}
	for _, e := range entries {
		for _, sr := range e.Specs {
			s, ok := stats[sr.Name]
			if !ok {
				s = &FlakeStats{Name: sr.Name, Path: sr.Path}
				stats[sr.Name] = s
			}
			s.Total++
			if sr.Passed {
				s.Passed++
			} else {
				s.Failed++
			}
			if sr.Flaky {
				s.Flaky++
			}
		}
	}
	out := make([]FlakeStats, 0, len(stats))
	for _, s := range stats {
		out = append(out, *s)
	}
	// Sort by failure ratio descending so the worst offenders surface
	// first.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].FailureRatio() < out[j].FailureRatio(); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

// FlakeStats is per-spec aggregate from the history log.
type FlakeStats struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Total  int    `json:"total"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
	Flaky  int    `json:"flaky"`
}

// FailureRatio returns the fraction of runs that failed (0..1).
func (f FlakeStats) FailureRatio() float64 {
	if f.Total == 0 {
		return 0
	}
	return float64(f.Failed) / float64(f.Total)
}

// HistoryPathFor returns the canonical history file path for a given
// spec root. We always store it inside the spec dir so a project's
// history travels with it (and is gitignore-able via .gitignore).
func HistoryPathFor(specRoot string) string {
	return filepath.Join(specRoot, ".history.jsonl")
}

// gitSHA / gitBranch helpers — used by the CLI to enrich history
// entries. Best-effort only; never fail the run.
func gitInfo(dir string) (sha, branch string) {
	sha = strings.TrimSpace(runCmd(dir, "git", "rev-parse", "HEAD"))
	branch = strings.TrimSpace(runCmd(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"))
	return
}
