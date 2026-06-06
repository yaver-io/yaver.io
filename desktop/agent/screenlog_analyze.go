package main

// screenlog_analyze.go — turns a recorded screenlog session into an
// ActivityReport ("what did this machine spend its time on"). Pure +
// local + deterministic: each kept frame is point-in-time, so we
// attribute the span until the NEXT frame to that frame's active app,
// capped (a long gap = the machine was idle / asleep, not "8 hours in
// Notepad"). This is exactly the question behind "ask what my dad spent
// most of his time on".

import (
	"fmt"
	"sort"
)

// screenlogToSamples converts frames to attributed intervals.
//
//	idleGapMs:       a gap larger than this (no kept frame) is treated as
//	                 idle for the portion beyond maxAttributeMs.
//	maxAttributeMs:  the most time a single frame may represent.
func screenlogToSamples(sess *ScreenlogSession, idleGapMs, maxAttributeMs int64) []ActivitySample {
	// Collapse multi-display frames captured at the same instant: attribute
	// once per timestamp (the active app is the same across displays).
	type pt struct {
		at  int64
		to  int64 // stored ActiveToMs (0 = unknown → fall back to next point)
		app string
		win string
	}
	seen := map[int64]bool{}
	var pts []pt
	for _, f := range sess.Frames {
		if seen[f.CapturedAt] {
			continue
		}
		seen[f.CapturedAt] = true
		app := f.ActiveApp
		if app == "" {
			app = "unknown"
		}
		pts = append(pts, pt{at: f.CapturedAt, to: f.ActiveToMs, app: app, win: f.ActiveWindow})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].at < pts[j].at })

	var out []ActivitySample
	for i, p := range pts {
		// Prefer the recorder's stored active interval (exact "on from X
		// to Y"); fall back to the next frame's timestamp for old data.
		var span int64
		switch {
		case p.to > p.at:
			span = p.to - p.at
		case i+1 < len(pts):
			span = pts[i+1].at - p.at
		default:
			span = maxAttributeMs
		}
		// A span WITHIN the idle threshold is genuinely active on this app
		// — "the screen was on Excel from 12:01 to 12:53". Only a span that
		// exceeds the threshold (no kept/heartbeat frame for a long time →
		// machine asleep / user away) is capped to a short active head with
		// the remainder counted as idle.
		var active int64
		if span <= idleGapMs {
			active = span
		} else {
			active = maxAttributeMs
			if active > span {
				active = span
			}
		}
		out = append(out, ActivitySample{
			Start: p.at, End: p.at + active, Category: p.app, Label: p.win,
		})
		if span > active {
			out = append(out, ActivitySample{
				Start: p.at + active, End: p.at + span, Category: "idle", Idle: true,
			})
		}
	}
	return out
}

// analyzeScreenlogSession builds the activity report for a session id.
func analyzeScreenlogSession(id string, idleGapSec, maxAttributeSec int) (*ActivityReport, *ScreenlogSession, error) {
	sess, err := loadScreenlogSession(id)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %s", id)
	}
	if idleGapSec <= 0 {
		// Default to a window a bit beyond the heartbeat (300s): an
		// unchanged screen kept alive by heartbeats counts active; only a
		// gap longer than this (capture paused — asleep/away) is idle.
		idleGapSec = 600
	}
	if maxAttributeSec <= 0 {
		maxAttributeSec = sess.Config.IntervalSec * 4
		if maxAttributeSec < 10 {
			maxAttributeSec = 10
		}
	}
	subject := sess.Host
	if subject == "" {
		subject = id
	}
	samples := screenlogToSamples(sess, int64(idleGapSec)*1000, int64(maxAttributeSec)*1000)
	rep := buildActivityReport(samples, "screen", subject)
	return &rep, sess, nil
}

// sampleKeyframes picks up to n evenly-spaced frames from a session so a
// vision-capable runner can actually SEE representative moments (returned
// as inline MCP image content by the dispatcher).
func sampleKeyframes(sess *ScreenlogSession, n int) []ScreenlogFrame {
	if n <= 0 || len(sess.Frames) == 0 {
		return nil
	}
	if n >= len(sess.Frames) {
		return sess.Frames
	}
	out := make([]ScreenlogFrame, 0, n)
	step := float64(len(sess.Frames)-1) / float64(n-1)
	for i := 0; i < n; i++ {
		idx := int(float64(i)*step + 0.5)
		if idx >= len(sess.Frames) {
			idx = len(sess.Frames) - 1
		}
		out = append(out, sess.Frames[idx])
	}
	return out
}
