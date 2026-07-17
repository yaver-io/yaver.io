package main

// recap_script.go — what the recap SAYS.
//
// The script is the source of truth for everything audible or readable in a
// recap. The subtitle track is a render of it; the narration audio is a
// render of it; a 3D surface drawing captions as geometry renders it too.
// That inversion is deliberate — it's what makes "mute the narrator and read
// instead" a volume control rather than a re-encode, and it's what makes
// translation nearly free later.
//
// Two sources, joined:
//
//   - WHAT WAS ON SCREEN — screenlog's process model already segments a
//     session into task episodes (system, window title, duration, keyframes).
//     That's the shot list.
//   - WHAT LANDED IN GIT — CollectGitActivity + pickHighlights already read
//     commits/PRs and filter them down to user-facing changes, dropping
//     chore/docs/ci noise. That's the plot.
//
// The LLM stage is OPTIONAL and follows newsletter_compose.go's pattern
// exactly: build a deterministic draft that reads fine on its own, then offer
// it to a runner for polish. A box with no runner, no key, or no network
// still gets a narrated recap — just a plainer one.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// latestScreenlogSessionID returns the most recent session's id.
func latestScreenlogSessionID() (string, error) {
	sessions, err := listScreenlogSessions()
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no screenlog sessions — start one with screenlog_start to record recaps")
	}
	return sessions[0].ID, nil
}

// screenlogSessionForWindow picks the session with the most frames inside
// [sinceMs, untilMs]. An autorun run should be recapped from the session that
// was actually recording while it ran — which is not always the newest one,
// e.g. when a run finished and a fresh session started before anyone asked
// for the recap.
func screenlogSessionForWindow(sinceMs, untilMs int64) (string, error) {
	sessions, err := listScreenlogSessions()
	if err != nil {
		return "", err
	}
	best, bestCount := "", 0
	for _, s := range sessions {
		count := 0
		for _, f := range s.Frames {
			if sinceMs > 0 && f.CapturedAt < sinceMs {
				continue
			}
			if untilMs > 0 && f.CapturedAt > untilMs {
				continue
			}
			count++
		}
		if count > bestCount {
			best, bestCount = s.ID, count
		}
	}
	if best == "" {
		return "", fmt.Errorf("no screenlog frames recorded in that window")
	}
	return best, nil
}

// recapEpisodesInWindow returns the process-model episodes overlapping the
// build window, oldest first. Falls back to nil (not an error) when the
// process model can't be built — the recap then narrates from git alone.
func recapEpisodesInWindow(sessionID string, sinceMs, untilMs int64) []ProcessEpisode {
	model, _, err := buildProcessSkeleton(sessionID)
	if err != nil || model == nil {
		return nil
	}
	var out []ProcessEpisode
	for _, ep := range model.Episodes {
		if untilMs > 0 && ep.StartMs > untilMs {
			continue
		}
		if sinceMs > 0 && ep.EndMs < sinceMs {
			continue
		}
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

// recapMaxCues bounds the script. A 75-second recap with 40 cues is a
// subtitle blizzard nobody reads; the point is the few things that mattered.
const recapMaxCues = 12

// recapMinCueSec keeps a cue on screen long enough to actually read.
const recapMinCueSec = 1.8

// BuildRecapCues produces the timed script for a recap.
func BuildRecapCues(ctx context.Context, sess *ScreenlogSession, tl *recapTimeline, opts RecapBuildOpts) ([]RecapCue, error) {
	episodes := recapEpisodesInWindow(sess.ID, opts.SinceMs, opts.UntilMs)

	var activity *GitActivity
	if opts.WorkDir != "" {
		// Window the git collection to the run itself. SinceDays is the only
		// knob CollectGitActivity has, so round up to whole days and let
		// pickHighlights do the narrowing.
		days := 1
		if opts.SinceMs > 0 {
			elapsed := time.Since(time.UnixMilli(opts.SinceMs))
			days = int(elapsed.Hours()/24) + 1
		}
		act, err := CollectGitActivity(ComposeNewsletterOptions{
			Repo:      opts.WorkDir,
			SinceDays: days,
		})
		if err == nil {
			activity = act
		}
	}

	draft := buildRecapDraft(episodes, activity, opts)
	if len(draft) == 0 {
		return nil, fmt.Errorf("nothing to narrate")
	}

	// Optional polish. Same shape as newsletter_compose: raw material + a
	// skeleton, rewritten by the runner. On any failure we keep the draft —
	// a plainer recap beats no recap.
	if opts.Runner != "" || opts.Narrate {
		if polished, err := polishRecapScript(ctx, draft, episodes, activity, opts); err == nil && len(polished) > 0 {
			draft = polished
		} else if err != nil {
			log.Printf("[recap] script polish skipped: %v", err)
		}
	}

	return timeRecapCues(draft, episodes, tl), nil
}

// recapBeat is one line of script before it's been placed on the timeline.
type recapBeat struct {
	Text string
	// WallMs anchors the beat to when it happened. Zero = unanchored (an
	// opener or a closer), placed at the head/tail of the video.
	WallMs int64
}

// buildRecapDraft writes the deterministic script.
//
// Structure: an opener that states the evidence, one beat per significant
// episode, and a closer that states the outcome honestly.
func buildRecapDraft(episodes []ProcessEpisode, act *GitActivity, opts RecapBuildOpts) []recapBeat {
	var beats []recapBeat

	// --- opener: what this run was, in facts ---
	name := opts.Task
	if name == "" {
		name = "this session"
	}
	beats = append(beats, recapBeat{Text: recapOpener(name, opts)})

	// --- body: the episodes worth mentioning ---
	// Longest episodes first to choose WHICH, then back to chronological to
	// place them — a recap that jumps around in time is unreadable.
	sig := make([]ProcessEpisode, len(episodes))
	copy(sig, episodes)
	sort.Slice(sig, func(i, j int) bool { return sig[i].DurationSec > sig[j].DurationSec })
	budget := recapMaxCues - 2 // leave room for opener + closer
	if len(sig) > budget {
		sig = sig[:budget]
	}
	sort.Slice(sig, func(i, j int) bool { return sig[i].StartMs < sig[j].StartMs })
	for _, ep := range sig {
		if t := recapEpisodeLine(ep); t != "" {
			beats = append(beats, recapBeat{Text: t, WallMs: ep.StartMs})
		}
	}

	// --- closer: what actually landed ---
	beats = append(beats, recapBeat{Text: recapCloser(act, opts)})
	return beats
}

func recapOpener(name string, opts RecapBuildOpts) string {
	if opts.Iterations > 0 {
		return fmt.Sprintf("%s. %s over %s.",
			name, recapPlural(opts.Iterations, "iteration"), recapWindowPhrase(opts))
	}
	return fmt.Sprintf("%s%s.", strings.ToUpper(name[:1])+name[1:], recapWindowSuffix(opts))
}

// recapCloser states the outcome — and is careful about what it claims.
//
// This is the honesty seam. Per 3a32a4fc3, FinishReason == autorunReasonDone
// means only that a line in the progress file said DONE; a runner once wrote
// "I did not run the gate, so this is NOT marked DONE" and the substring match
// ended the run as complete. So the closer narrates the runner's CLAIM as a
// claim, and states the verifiable evidence (commits + a final commit)
// separately. It must never say "shipped" on the strength of the claim alone.
func recapCloser(act *GitActivity, opts RecapBuildOpts) string {
	var b strings.Builder
	switch {
	case opts.Commits > 0:
		b.WriteString(fmt.Sprintf("Landed %s", recapPlural(opts.Commits, "verified commit")))
		if act != nil && len(act.Highlights) > 0 {
			b.WriteString(": " + strings.ToLower(strings.TrimSuffix(act.Highlights[0], ".")))
		}
		b.WriteString(".")
	case opts.FinishReason == autorunReasonGate:
		b.WriteString("The gate failed, so nothing was kept.")
	case opts.FinishReason == autorunReasonScope:
		b.WriteString("A scope violation stopped the run; the changes were stashed, not committed.")
	case opts.FinishReason == autorunReasonRunner:
		b.WriteString("The runner failed and the run ended without landing anything.")
	case opts.FinishReason == autorunReasonResources:
		b.WriteString("The machine ran out of resources before anything landed.")
	case opts.FinishReason == autorunReasonStopped:
		b.WriteString("You stopped this run. Nothing was committed.")
	default:
		b.WriteString("No commits were kept.")
	}
	if opts.Heals > 0 {
		b.WriteString(fmt.Sprintf(" It self-healed %s along the way.", recapPlural(opts.Heals, "time")))
	}
	// The claim, clearly marked as a claim rather than a result.
	if opts.FinishReason == autorunReasonDone && !opts.Verified {
		b.WriteString(" The runner said it was done, but nothing was committed to show for it — worth a look.")
	} else if opts.FinishReason == autorunReasonConverged && opts.Commits == 0 {
		b.WriteString(" It stopped making changes rather than finishing.")
	}
	return b.String()
}

// recapEpisodeLine describes one episode from its deterministic fields. The
// runner-filled Intent is preferred when present — it's a real description
// rather than an app name.
func recapEpisodeLine(ep ProcessEpisode) string {
	if strings.TrimSpace(ep.Intent) != "" {
		return strings.TrimSpace(ep.Intent)
	}
	system := strings.TrimSpace(ep.System)
	screen := strings.TrimSpace(ep.Screen)
	if system == "" && screen == "" {
		return ""
	}
	dur := recapDurationPhrase(ep.DurationSec)
	if screen != "" && system != "" {
		return fmt.Sprintf("%s — %s (%s)", system, screen, dur)
	}
	if system != "" {
		return fmt.Sprintf("%s for %s", system, dur)
	}
	return fmt.Sprintf("%s for %s", screen, dur)
}

func recapDurationPhrase(sec int) string {
	switch {
	case sec >= 3600:
		return fmt.Sprintf("%.1f hours", float64(sec)/3600)
	case sec >= 60:
		return fmt.Sprintf("%d minutes", sec/60)
	default:
		return fmt.Sprintf("%d seconds", sec)
	}
}

func recapWindowPhrase(opts RecapBuildOpts) string {
	if opts.SinceMs > 0 && opts.UntilMs > opts.SinceMs {
		return recapDurationPhrase(int((opts.UntilMs - opts.SinceMs) / 1000))
	}
	return "the session"
}

func recapWindowSuffix(opts RecapBuildOpts) string {
	if opts.SinceMs > 0 && opts.UntilMs > opts.SinceMs {
		return " over " + recapWindowPhrase(opts)
	}
	return ""
}

func recapPlural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// --- LLM polish ------------------------------------------------------------

// buildRecapPrompt mirrors BuildComposePrompt (newsletter_compose.go:284):
// raw material + a skeleton draft + a voice instruction.
func buildRecapPrompt(draft []recapBeat, episodes []ProcessEpisode, act *GitActivity, opts RecapBuildOpts) string {
	var b strings.Builder
	b.WriteString("You are writing the narration for a short screen-recap video of an automated coding run.\n")
	b.WriteString("It is spoken aloud AND shown as subtitles, so: short sentences, plain words, no markdown, no lists, no emoji.\n")
	b.WriteString("Write for someone who does not read code and was asleep while this ran.\n")
	b.WriteString(fmt.Sprintf("Return EXACTLY %d lines — one per input line, same order, same meaning, better phrasing.\n", len(draft)))
	b.WriteString("Return a JSON array of strings and nothing else.\n\n")

	// The honesty rule, restated for the model. Without this a summariser will
	// cheerfully turn "a line said DONE" into "successfully completed".
	b.WriteString("HONESTY RULES — these override any instinct to sound positive:\n")
	b.WriteString("- Do not say the work is finished, shipped, or successful unless commits were actually kept.\n")
	b.WriteString("- A runner claiming it was done is a claim, not a fact. Say so plainly if nothing was committed.\n")
	b.WriteString("- Do not invent detail that is not in the source material below.\n\n")

	fmt.Fprintf(&b, "Evidence: %d iterations, %d verified commits kept, finish reason %q, %d self-heals.\n",
		opts.Iterations, opts.Commits, opts.FinishReason, opts.Heals)

	if act != nil && len(act.Highlights) > 0 {
		b.WriteString("\nUser-facing changes that landed:\n")
		for _, h := range act.Highlights {
			b.WriteString("- " + h + "\n")
		}
	}
	if len(episodes) > 0 {
		b.WriteString("\nWhat was on screen (app, window, seconds):\n")
		for _, ep := range episodes {
			fmt.Fprintf(&b, "- %s | %s | %ds\n", ep.System, ep.Screen, ep.DurationSec)
		}
	}
	b.WriteString("\nSkeleton to rewrite (keep the order and the meaning):\n")
	for _, d := range draft {
		b.WriteString("- " + d.Text + "\n")
	}
	b.WriteString("\nReturn only the JSON array.\n")
	return b.String()
}

// polishRecapScript runs the draft through a runner. Returns the draft's beats
// with rewritten text, preserving every anchor — the model rewrites words, it
// does not get to move things in time.
func polishRecapScript(ctx context.Context, draft []recapBeat, episodes []ProcessEpisode, act *GitActivity, opts RecapBuildOpts) ([]recapBeat, error) {
	prompt := buildRecapPrompt(draft, episodes, act, opts)
	out, err := runMailDraftInline(opts.Runner, prompt)
	if err != nil {
		return nil, err
	}
	lines, err := parseRecapScriptJSON(out)
	if err != nil {
		return nil, err
	}
	if len(lines) != len(draft) {
		// A model that returned a different number of lines has lost the
		// mapping to the timeline; the anchors would be wrong. Keep the draft.
		return nil, fmt.Errorf("runner returned %d lines, want %d", len(lines), len(draft))
	}
	polished := make([]recapBeat, len(draft))
	for i := range draft {
		polished[i] = recapBeat{Text: strings.TrimSpace(lines[i]), WallMs: draft[i].WallMs}
		if polished[i].Text == "" {
			polished[i].Text = draft[i].Text
		}
	}
	return polished, nil
}

// parseRecapScriptJSON pulls a JSON string array out of a runner reply, which
// may be wrapped in prose or a fenced block despite instructions.
func parseRecapScriptJSON(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			s = s[i : j+1]
		}
	}
	var lines []string
	if err := json.Unmarshal([]byte(s), &lines); err != nil {
		return nil, fmt.Errorf("runner reply was not a JSON array: %w", err)
	}
	return lines, nil
}

// --- timing ----------------------------------------------------------------

// timeRecapCues places beats on the video timeline.
//
// The closer is structurally privileged: it is the line that states the
// outcome ("the gate failed, so nothing was kept"), and it is the last thing
// authored, which makes it the first casualty of any naive overlap pass. So
// the TAIL OF THE VIDEO IS RESERVED FOR IT before anything else is placed.
//
// An earlier version clamped the closer into the tail AFTER sorting, which
// broke the sort invariant — a late-anchored episode left the list as
// [0, 4.5, 2.745], and the overlap pass then dropped the closer as
// unplaceable. A recap that silently omits what happened is worse than no
// recap, so the reservation is not an optimisation; it's the invariant.
func timeRecapCues(beats []recapBeat, episodes []ProcessEpisode, tl *recapTimeline) []RecapCue {
	if len(beats) == 0 || tl.totalSec <= 0 {
		return nil
	}
	if len(beats) == 1 {
		return []RecapCue{{Text: beats[0].Text, StartSec: 0, EndSec: tl.totalSec}}
	}

	closer := beats[len(beats)-1]
	head := beats[:len(beats)-1]

	// Reserve the tail. On a very short video this collapses to 0 and the
	// closer takes the whole thing — correct: the outcome outranks the tour.
	closerStart := tl.totalSec - recapMinCueSec
	if closerStart < 0 {
		closerStart = 0
	}

	type mark struct {
		text  string
		start float64
	}
	marks := make([]mark, 0, len(head))
	for i, b := range head {
		start := 0.0
		switch {
		case b.WallMs > 0:
			start = tl.videoSecAt(b.WallMs) // where it actually happened
		case i > 0:
			start = closerStart // an unanchored non-opener sinks to the tail
		}
		marks = append(marks, mark{b.Text, start})
	}
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].start < marks[j].start })

	out := make([]RecapCue, 0, len(beats))
	var prevEnd float64
	for i, m := range marks {
		start := m.start
		if start < prevEnd {
			start = prevEnd // pushed forward by the previous cue's minimum
		}
		if start >= closerStart {
			break // the rest would collide with the closer's reservation
		}
		end := closerStart
		if i+1 < len(marks) && marks[i+1].start > start && marks[i+1].start < closerStart {
			end = marks[i+1].start
		}
		if end-start < recapMinCueSec {
			end = start + recapMinCueSec
		}
		if end > closerStart {
			end = closerStart
		}
		if end-start < 0.4 {
			continue // no room left; drop rather than flash it subliminally
		}
		out = append(out, RecapCue{Text: m.text, StartSec: start, EndSec: end})
		prevEnd = end
	}
	// The closer always lands.
	out = append(out, RecapCue{Text: closer.Text, StartSec: closerStart, EndSec: tl.totalSec})
	return out
}

// --- WebVTT ----------------------------------------------------------------

// writeVTT emits a WebVTT sidecar.
//
// Sidecar rather than burned-in (studio/compositor.go's CaptionMP4 burns text
// into pixels with ffmpeg drawtext) because burned text can't be toggled,
// can't be translated, and can't be muted independently of the voice. Every
// surface can consume this: web via <track>, and tvOS/mobile/VR by parsing it
// — which VR has to do anyway, since it draws captions as geometry.
func writeVTT(path string, cues []RecapCue) error {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n", vttTimestamp(c.StartSec), vttTimestamp(c.EndSec))
		// A cue is one line of speech; strip newlines so it can't inject cue
		// settings or break the block structure.
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(c.Text), "\n", " "))
		b.WriteString("\n\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// vttTimestamp formats seconds as WebVTT's HH:MM:SS.mmm.
func vttTimestamp(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := int(sec) % 60
	ms := int((sec - float64(int(sec))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
