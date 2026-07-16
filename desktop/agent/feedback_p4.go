package main

// feedback_p4.go — P4 of the n2n plan. Two new pieces:
//
//   feedback_create {surface, transcript?, screenshotSessionId?, source?}
//       Mints a FeedbackReport programmatically so a runner can file
//       one without waiting for a shake/overlay UI. When
//       screenshotSessionId is set, we attach a runtime_frame JPEG
//       to the report (Auto-attach path — the SDK doesn't have to
//       drag screenshots along).
//
//   feedback_speak {id?}
//       TTS-summarizes the feedback queue via voice_speak. Empty id
//       broadcasts a summary of the recent reports; a specific id
//       reads that one report. Uses the P3 voice_speak pipeline —
//       nothing here does its own TTS.
//
// Voice→FeedbackReport authoring is a small tweak in voice_http.go
// (routes turns to FeedbackManager when the intent tag says feedback);
// that lives next to the voice_http handler to keep the intent map
// local. This file wires the MCP surface.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// feedbackCreateArgs is the payload for feedback_create.
type feedbackCreateArgs struct {
	Surface             string `json:"surface"`
	Transcript          string `json:"transcript,omitempty"`
	Source              string `json:"source,omitempty"` // yaver-app / in-app-sdk / mcp
	AppName             string `json:"appName,omitempty"`
	Platform            string `json:"platform,omitempty"`
	OSVersion           string `json:"osVersion,omitempty"`
	Model               string `json:"model,omitempty"`
	AppVersion          string `json:"appVersion,omitempty"`
	BuildID             string `json:"buildId,omitempty"`
	ScreenshotSessionID string `json:"screenshotSessionId,omitempty"`
}

// runFeedbackCreate mints a FeedbackReport via FeedbackManager and
// (best-effort) attaches a JPEG frame from a live runtime session.
// Split from the MCP wrapper for tests.
func runFeedbackCreate(mgr *FeedbackManager, args feedbackCreateArgs) map[string]interface{} {
	if mgr == nil {
		return map[string]interface{}{"ok": false, "error": "agent has no FeedbackManager — pair a device first"}
	}
	surface := strings.TrimSpace(args.Surface)
	if surface == "" {
		return map[string]interface{}{"ok": false, "error": "surface is required"}
	}
	source := strings.TrimSpace(args.Source)
	if source == "" {
		source = "mcp"
	}
	report := FeedbackReport{
		Source:     source,
		Transcript: strings.TrimSpace(args.Transcript),
		AppVersion: args.AppVersion,
		BuildID:    args.BuildID,
		DeviceInfo: DeviceFBInfo{
			Platform:  args.Platform,
			Model:     args.Model,
			OSVersion: args.OSVersion,
			AppName:   args.AppName,
		},
		Project: FeedbackProject{
			AppName: args.AppName,
			Surface: surface,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	meta, err := json.Marshal(report)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	files := map[string][]byte{}
	if id := strings.TrimSpace(args.ScreenshotSessionID); id != "" {
		buf, status, ferr := feedbackFrameFetch(id)
		if ferr == nil && status < 400 && len(buf) > 0 {
			files["screenshot-0.jpg"] = buf
		}
	}
	saved, err := mgr.ReceiveFeedback(meta, files)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return map[string]interface{}{
		"ok":          true,
		"id":          saved.ID,
		"surface":     surface,
		"attachments": len(files),
	}
}

// feedbackFrameFetch is a var seam so tests can stub the /frame proxy
// without a live HTTP handler. Production hits remoteRuntimeFrameJPEG.
var feedbackFrameFetch = remoteRuntimeFrameJPEG

// feedbackSpeakArgs is the payload for feedback_speak.
type feedbackSpeakArgs struct {
	ID       string `json:"id,omitempty"`
	Device   string `json:"device,omitempty"`
	MaxItems int    `json:"maxItems,omitempty"`
}

// runFeedbackSpeak composes a spoken summary of the feedback queue
// (or one specific report) and hands it to runVoiceSpeak. Keeping
// this a compose function (not a new TTS engine) matches the
// principle in the plan: no new transport.
func runFeedbackSpeak(fm *FeedbackManager, bb *BlackBoxManager, args feedbackSpeakArgs) map[string]interface{} {
	if fm == nil {
		return map[string]interface{}{"ok": false, "error": "no FeedbackManager"}
	}
	text := composeFeedbackSummary(fm, args.ID, args.MaxItems)
	if text == "" {
		return map[string]interface{}{"ok": true, "note": "no feedback to summarise"}
	}
	return runVoiceSpeak(bb, voiceSpeakArgs{Device: args.Device, Text: text})
}

func composeFeedbackSummary(fm *FeedbackManager, id string, maxItems int) string {
	if id != "" {
		reports := fm.ListReports()
		for _, r := range reports {
			if r.ID == id {
				return oneFeedbackLine(r)
			}
		}
		return ""
	}
	if maxItems <= 0 {
		maxItems = 3
	}
	reports := fm.ListReports()
	if len(reports) == 0 {
		return ""
	}
	if len(reports) > maxItems {
		reports = reports[:maxItems]
	}
	pieces := make([]string, 0, len(reports)+1)
	pieces = append(pieces, fmt.Sprintf("You have %d recent feedback report(s).", len(reports)))
	for _, r := range reports {
		pieces = append(pieces, oneFeedbackLine(r))
	}
	return strings.Join(pieces, " ")
}

func oneFeedbackLine(r *FeedbackReport) string {
	if r == nil {
		return ""
	}
	title := strings.TrimSpace(r.Transcript)
	if title == "" && len(r.Errors) > 0 {
		title = r.Errors[0].Message
	}
	if title == "" {
		title = "(no transcript)"
	}
	// Trim long transcripts so the TTS engine doesn't monologue.
	if len(title) > 200 {
		title = title[:200] + "…"
	}
	surface := r.Project.Surface
	if surface == "" {
		surface = r.DeviceInfo.Platform
	}
	if surface == "" {
		surface = "unknown surface"
	}
	return fmt.Sprintf("Feedback %s on %s: %s.", strings.TrimSpace(r.ID), surface, title)
}

// ListReports exposes the internal report map as a slice (newest
// first). Kept here so we don't touch feedback.go for one call site.
func (fm *FeedbackManager) ListReports() []*FeedbackReport {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	out := make([]*FeedbackReport, 0, len(fm.reports))
	for _, r := range fm.reports {
		out = append(out, r)
	}
	// newest first; CreatedAt is RFC3339 so lexical > works.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt > out[j-1].CreatedAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *HTTPServer) mcpFeedbackCreate(args feedbackCreateArgs) interface{} {
	return runFeedbackCreate(s.feedbackMgr, args)
}
func (s *HTTPServer) mcpFeedbackSpeak(args feedbackSpeakArgs) interface{} {
	return runFeedbackSpeak(s.feedbackMgr, s.blackboxMgr, args)
}
