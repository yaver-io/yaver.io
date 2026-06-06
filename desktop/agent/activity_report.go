package main

// activity_report.go — the source-agnostic analysis half of Yaver's
// generic monitoring spine. A "probe" (screen frames, PLC tag changes,
// process samples, packet flows) reduces its raw observations to a list
// of ActivitySample intervals; this file turns ANY such list into a
// human-and-runner-readable ActivityReport: time-by-category, top
// labels, hourly histogram, active vs idle.
//
// screenlog is the first probe (source="screen"). The talos machine
// engine can feed the same report with source="machine" (PLC register
// activity), a process sampler with source="process", etc. Keeping the
// report generic is what makes "monitor a PC / a tool / a PLC and ask
// what it spent time on" one code path instead of four.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ActivitySample is one attributed interval of observed activity.
type ActivitySample struct {
	Start    int64  // unix ms
	End      int64  // unix ms
	Category string // primary bucket: app name, PLC tag, process, …
	Label    string // detail: window title, register, command, …
	Idle     bool   // the subject was idle during this interval
}

// CategoryStat is aggregated time for one category or label.
type CategoryStat struct {
	Name    string  `json:"name"`
	Seconds int     `json:"seconds"`
	Percent float64 `json:"percent"`
	Samples int     `json:"samples"`
}

// HourBucket is active seconds in one local hour-of-day (0–23).
type HourBucket struct {
	Hour    int `json:"hour"`
	Seconds int `json:"seconds"`
}

// ActivityReport is the deterministic summary. No LLM involved — the
// "what did they spend time on" answer is ByCategory[0]. A runner can
// narrate it from NarrativePrompt, but the numbers are exact.
type ActivityReport struct {
	Source     string         `json:"source"`
	Subject    string         `json:"subject"`
	FromMs     int64          `json:"fromMs"`
	ToMs       int64          `json:"toMs"`
	SpanSec    int            `json:"spanSec"`
	ActiveSec  int            `json:"activeSec"`
	IdleSec    int            `json:"idleSec"`
	Samples    int            `json:"samples"`
	ByCategory []CategoryStat `json:"byCategory"`
	TopLabels  []CategoryStat `json:"topLabels"`
	Hourly     []HourBucket   `json:"hourly"`
}

// buildActivityReport aggregates samples into a report. Idle intervals
// count toward IdleSec and are excluded from category/label/hourly
// breakdowns (you don't "spend time on" idle).
func buildActivityReport(samples []ActivitySample, source, subject string) ActivityReport {
	r := ActivityReport{Source: source, Subject: subject, Samples: len(samples)}
	catSec := map[string]int{}
	catN := map[string]int{}
	labSec := map[string]int{}
	labN := map[string]int{}
	hourSec := map[int]int{}

	for _, s := range samples {
		if s.End <= s.Start {
			continue
		}
		dur := int((s.End - s.Start) / 1000)
		if dur <= 0 {
			dur = 1
		}
		if r.FromMs == 0 || s.Start < r.FromMs {
			r.FromMs = s.Start
		}
		if s.End > r.ToMs {
			r.ToMs = s.End
		}
		if s.Idle {
			r.IdleSec += dur
			continue
		}
		r.ActiveSec += dur
		cat := s.Category
		if cat == "" {
			cat = "unknown"
		}
		catSec[cat] += dur
		catN[cat]++
		if s.Label != "" {
			labSec[s.Label] += dur
			labN[s.Label]++
		}
		// Attribute the whole interval to its start hour (intervals are
		// short relative to an hour for every real probe).
		hourSec[time.UnixMilli(s.Start).Hour()] += dur
	}

	if r.ToMs > r.FromMs {
		r.SpanSec = int((r.ToMs - r.FromMs) / 1000)
	}
	r.ByCategory = sortedStats(catSec, catN, r.ActiveSec)
	r.TopLabels = sortedStats(labSec, labN, r.ActiveSec)
	if len(r.TopLabels) > 15 {
		r.TopLabels = r.TopLabels[:15]
	}
	for h := 0; h < 24; h++ {
		if sec := hourSec[h]; sec > 0 {
			r.Hourly = append(r.Hourly, HourBucket{Hour: h, Seconds: sec})
		}
	}
	return r
}

func sortedStats(sec, n map[string]int, total int) []CategoryStat {
	out := make([]CategoryStat, 0, len(sec))
	for name, s := range sec {
		pct := 0.0
		if total > 0 {
			pct = float64(s) * 100 / float64(total)
		}
		out = append(out, CategoryStat{Name: name, Seconds: s, Percent: round1(pct), Samples: n[name]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seconds != out[j].Seconds {
			return out[i].Seconds > out[j].Seconds
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func humanDur(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm", sec/60)
	}
	return fmt.Sprintf("%dh%dm", sec/3600, (sec%3600)/60)
}

// NarrativePrompt renders a compact, runner-ready brief. The MCP client's
// runner (claude-code / codex on whatever machine) calls the analyze verb,
// gets this back, and writes the prose answer to "what did they spend most
// time on" — so the analysis "utilizes runners" without the agent ever
// spawning a headless one.
func (r ActivityReport) NarrativePrompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Activity report for %q (source=%s).\n", r.Subject, r.Source)
	if r.FromMs > 0 {
		fmt.Fprintf(&b, "Window: %s → %s (span %s).\n",
			time.UnixMilli(r.FromMs).Format("2006-01-02 15:04"),
			time.UnixMilli(r.ToMs).Format("15:04"), humanDur(r.SpanSec))
	}
	fmt.Fprintf(&b, "Active %s, idle %s, %d samples.\n", humanDur(r.ActiveSec), humanDur(r.IdleSec), r.Samples)
	b.WriteString("Time by category (most first):\n")
	for i, c := range r.ByCategory {
		if i >= 10 {
			break
		}
		fmt.Fprintf(&b, "  - %s: %s (%.1f%%)\n", c.Name, humanDur(c.Seconds), c.Percent)
	}
	b.WriteString("Write a short natural-language summary of what this subject spent most of their time on, calling out the top 2-3 activities and any notable idle stretches.")
	return b.String()
}
