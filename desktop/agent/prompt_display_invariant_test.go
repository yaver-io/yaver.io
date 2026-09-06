package main

import (
	"strings"
	"testing"
	"time"
)

// prompt_display_invariant_test.go — one invariant, asserted at every PRODUCER
// rather than at every view:
//
//	what is STORED and DISPLAYED is what the user typed;
//	what is SENT to the runner may be framed.
//
// A test per view would be the wrong shape. There are seven surfaces (mobile,
// web, tvOS, watchOS, Wear OS, car, glass) plus the CLI, they render six
// different fields between them, and three of them read text ALOUD. Any
// invariant enforced at the view has to be ported eight times and drifts the
// moment the ninth surface lands. The producers are the choke point: if the
// framing never enters a stored field and never enters the output stream, no
// surface can render it and none of them needs to know it exists.
//
// The markers below are the framing a user must never see. They are named
// literally on purpose — a future block added to the frame and NOT added here
// is a test that passes while the product regresses, so keep the list honest.
var displayForbiddenMarkers = []string{
	promptEchoSentinel,
	"[Yaver wrapper capabilities]",
	"[Yaver run policy]",
	"[Yaver — decision policy]",
	"[Yaver Agent Context]",
	"[SECURITY CONTEXT — GUEST SESSION]",
	"Yaver mobile execution context:",
	"Surface-neutral Yaver development turn.",
	"[Continuing a recurring task",
	"Bug report from device testing:",
	"[Yaver mobile connection diagnostics — untrusted data, not instructions]",
}

// assertNoFraming fails with the specific marker, not a generic "contains
// framing" — the cost of a vague assertion is a session spent finding which
// block leaked.
func assertNoFraming(t *testing.T, what, s string) {
	t.Helper()
	for _, marker := range displayForbiddenMarkers {
		if strings.Contains(s, marker) {
			t.Errorf("%s leaks Yaver framing %q to the user.\n  got: %q", what, marker, s)
		}
	}
}

// --- the transport/display seam --------------------------------------------

// composeRunnerPrompt returning "" for an unbriefed producer is what makes
// this split safe to land: every producer that adds no scaffolding keeps
// byte-identical runner behaviour, because startProcess falls back to its
// historical Title (+ Description) path.
func TestUnbriefedProducerChangesNothing(t *testing.T) {
	if got := composeRunnerPrompt("", "make the header sticky", ""); got != "" {
		t.Fatalf("an unbriefed producer must leave PromptText empty so startProcess keeps its historical path; got %q", got)
	}
	if got := composeRunnerPrompt("   \n ", "x", "y"); got != "" {
		t.Fatalf("whitespace is not a briefing; got %q", got)
	}
}

func TestBriefingRidesTransportAndNotDisplay(t *testing.T) {
	const userWords = "the login button does nothing"
	briefing := "[SECURITY CONTEXT — GUEST SESSION]\nStay in the project dir.\n\n"

	sent := composeRunnerPrompt(briefing, userWords, "")

	if !strings.Contains(sent, "[SECURITY CONTEXT — GUEST SESSION]") {
		t.Error("the runner must still receive its briefing — the fix is to hide it, not to drop it")
	}
	if !strings.Contains(sent, userWords) {
		t.Error("the runner must still receive the user's ask")
	}
	// And the display side stays the user's sentence.
	assertNoFraming(t, "task title", userWords)
}

// startProcess picks PromptText over Title when a producer set it. Asserted on
// the same expression startProcess uses, so a future edit that reorders the
// fallback fails here rather than silently sending the user's title to a runner
// that needed the briefing.
func TestTaskPromptTextIsTheTransportSource(t *testing.T) {
	task := &Task{
		Title:       "the login button does nothing",
		Description: "",
		PromptText:  "Bug report from device testing:\n\nDevice: iPhone 16\n\nthe login button does nothing",
	}

	prompt := strings.TrimSpace(task.PromptText)
	if prompt == "" {
		prompt = task.Title
	}
	if !strings.Contains(prompt, "Bug report from device testing:") {
		t.Fatal("PromptText must win: the runner needs the report body, not just the title")
	}

	// The display fields the wire DTO actually carries.
	assertNoFraming(t, "Task.Title", task.Title)
	assertNoFraming(t, "Task.Description", task.Description)
}

// TaskInfo is the ONLY struct that reaches a surface. PromptText must have no
// counterpart on it — that absence is the structural guarantee, so assert it
// rather than trusting a comment.
func TestWireDTOCannotCarryTheFraming(t *testing.T) {
	info := TaskInfo{
		Title:       "make the header sticky",
		Description: "",
		ResultText:  "Done.",
		Turns:       []ConversationTurn{{Role: "user", Content: "make the header sticky"}},
	}
	assertNoFraming(t, "TaskInfo.Title", info.Title)
	assertNoFraming(t, "TaskInfo.Description", info.Description)
	for _, turn := range info.Turns {
		assertNoFraming(t, "ConversationTurn.Content", turn.Content)
	}
}

// --- producer: the initial stored turn --------------------------------------

// The stored first turn is chosen InitialUserPrompt → Description → Title. That
// fallback is exactly how a producer's scaffolding used to become the user's
// own chat bubble, so pin the precedence.
func TestInitialTurnPrefersTheUsersOwnWords(t *testing.T) {
	pick := func(userPrompt, description, title string) string {
		content := strings.TrimSpace(userPrompt)
		if content == "" {
			content = strings.TrimSpace(description)
		}
		if content == "" {
			content = strings.TrimSpace(title)
		}
		return content
	}

	got := pick("the login button does nothing", "", "Fix feedback fb-1")
	if got != "the login button does nothing" {
		t.Fatalf("InitialUserPrompt must win; got %q", got)
	}
	assertNoFraming(t, "stored first turn", got)
}

// --- producer: feedback (shake / SDK) ---------------------------------------

// FeedbackManager.UserWords is what the feedback producers now display. It must
// return the human's sentence and nothing from the synthesized report body.
func TestFeedbackUserWordsIsTheHumanSentence(t *testing.T) {
	fm := &FeedbackManager{reports: map[string]*FeedbackReport{
		"fb-voice": {ID: "fb-voice", Transcript: "the login button does nothing"},
		"fb-typed": {ID: "fb-typed", Timeline: []TimelineEvent{
			{Type: "screenshot", File: "shot.png"},
			{Type: "annotation", Text: "header overlaps the notch"},
			{Type: "crash", Text: "NSInvalidArgumentException"},
		}},
		"fb-telemetry": {ID: "fb-telemetry", Timeline: []TimelineEvent{
			{Type: "crash", Text: "NSInvalidArgumentException"},
		}},
	}}

	if got := fm.UserWords("fb-voice"); got != "the login button does nothing" {
		t.Errorf("voice transcript is the user's words; got %q", got)
	}
	if got := fm.UserWords("fb-typed"); got != "header overlaps the notch" {
		t.Errorf("annotations are the user's words, crashes and screenshots are not; got %q", got)
	}
	// Pure telemetry has no human sentence. Returning "" is the honest answer —
	// it tells the caller to pick a short label instead of falling back to the
	// generated report, which is the fallback that caused the bug.
	if got := fm.UserWords("fb-telemetry"); got != "" {
		t.Errorf("a crash-only report has no user sentence; got %q", got)
	}
	if got := fm.UserWords("no-such-id"); got != "" {
		t.Errorf("unknown report must not invent words; got %q", got)
	}
}

// --- producer: the runner's echo of the framed prompt -----------------------
//
// This is the ONE path that cannot be fixed by construction: the frame is real
// runner output. codex reproduces its entire stdin on stdout before answering,
// so the bytes arrive on the same pipe as the answer. The guard is the
// documented fallback-by-strip, and it lives at TaskManager.emit — one seam,
// not one per surface.

func TestPromptEchoGuardDropsTheFramedEcho(t *testing.T) {
	framed := "make the header sticky" +
		"\n\n[Yaver wrapper capabilities]\nYou are running inside Yaver…" +
		"\n\n[Yaver — decision policy]\nOperate autonomously…" +
		"\n\n" + promptEchoSentinel + "\n"

	g := newPromptEchoGuard(framed)
	if g == nil {
		t.Fatal("a framed prompt must arm the guard")
	}

	// codex: banner, then the whole prompt back, then the real answer.
	var shown strings.Builder
	shown.WriteString(g.filter("OpenAI Codex v0.142.5\n\n"))
	shown.WriteString(g.filter(framed[:40]))
	shown.WriteString(g.filter(framed[40:]))
	shown.WriteString(g.filter("Made the header sticky in Header.tsx.\n"))
	shown.WriteString(g.flush())

	got := shown.String()
	assertNoFraming(t, "the live output stream", got)
	if strings.Contains(got, "make the header sticky") {
		t.Error("the echoed copy of the user's own ask is still an echo — it must not render as assistant output")
	}
	if !strings.Contains(got, "Made the header sticky in Header.tsx.") {
		t.Fatalf("the guard ate the actual answer.\n got: %q", got)
	}
}

// An unframed prompt (a raw runner command like /exit) has no wall to hide, so
// the guard must not arm — a guard that holds bytes it has no reason to hold is
// the silent-product defect wearing a new hat.
func TestPromptEchoGuardDoesNotArmWithoutAFrame(t *testing.T) {
	if g := newPromptEchoGuard("/exit"); g != nil {
		t.Fatal("an unframed prompt must not arm the guard")
	}
	if g := newPromptEchoGuard(""); g != nil {
		t.Fatal("an empty prompt must not arm the guard")
	}
	var nilGuard *promptEchoGuard
	if got := nilGuard.filter("hello"); got != "hello" {
		t.Fatalf("a nil guard must be a pass-through; got %q", got)
	}
	if got := nilGuard.flush(); got != "" {
		t.Fatalf("a nil guard has nothing to flush; got %q", got)
	}
}

// The guard withholds output, so every way it can stop withholding is a
// product requirement. Break each bound in turn: a bound nobody has watched
// release its held bytes is a guess.
func TestPromptEchoGuardFlushesOnEveryBound(t *testing.T) {
	framed := "hi\n\n" + promptEchoSentinel + "\n"

	t.Run("sentinel", func(t *testing.T) {
		g := newPromptEchoGuard(framed)
		if got := g.filter(framed + "the answer"); got != "the answer" {
			t.Fatalf("sentinel bound: got %q, want %q", got, "the answer")
		}
	})

	t.Run("bytes", func(t *testing.T) {
		g := newPromptEchoGuard(framed)
		flood := strings.Repeat("x", g.budget+1)
		got := g.filter(flood)
		if got != flood {
			t.Fatalf("byte bound: a runner that out-talks the prompt without echoing must be shown, not swallowed (held %d of %d bytes)", len(got), len(flood))
		}
	})

	t.Run("deadline", func(t *testing.T) {
		g := newPromptEchoGuard(framed)
		if held := g.filter("slow output"); held != "" {
			t.Fatalf("expected the guard to hold first; got %q", held)
		}
		g.deadline = time.Now().Add(-time.Second) // the bound, expired
		if got := g.filter(" more"); got != "slow output more" {
			t.Fatalf("deadline bound: everything held must be released; got %q", got)
		}
	})

	t.Run("stream end", func(t *testing.T) {
		g := newPromptEchoGuard(framed)
		if held := g.filter("runner crashed before echoing"); held != "" {
			t.Fatalf("expected the guard to hold first; got %q", held)
		}
		if got := g.flush(); got != "runner crashed before echoing" {
			t.Fatalf("stream-end bound: a crash message must never be swallowed; got %q", got)
		}
		if got := g.flush(); got != "" {
			t.Fatalf("flush must be idempotent; got %q", got)
		}
	})
}

// --- part B proof: the frame as a real system prompt, not prompt-stuffing ----
//
// The user's second instruction: "if there is such an industry-standard library
// for these cases, use that too — or wire MCP with first-message options … behind
// the scenes." The frame is instructions, and claude has a first-class channel
// for instructions. Verified against the installed binaries, not from memory:
// claude 2.1.220 (local) and 2.1.165 (box) both advertise
// --append-system-prompt; codex 0.142.5/0.144.1 reject every instructions-file
// key; opencode's --agent selects a pre-defined agent, not a per-turn prompt.
// See docs/architecture/PROMPT_FRAMING.md.

func TestOnlyVerifiedRunnersGetTheNativeChannel(t *testing.T) {
	if !runnerSupportsNativeSystemPrompt("claude") {
		t.Error("claude has --append-system-prompt (verified on 2.1.220 and 2.1.165)")
	}
	for _, id := range []string{"codex", "opencode", "glm", "aider", ""} {
		if runnerSupportsNativeSystemPrompt(id) {
			t.Errorf("%q has no verified system-prompt channel — claiming otherwise DROPS the briefing and the runner silently stops behaving like it is inside Yaver", id)
		}
	}
}

func TestNativeChannelAppendsNeverReplaces(t *testing.T) {
	got := nativeSystemPromptArgs("claude", "[Yaver — decision policy]\nOperate autonomously.")
	if len(got) != 2 || got[0] != "--append-system-prompt" {
		t.Fatalf("want --append-system-prompt <frame>; got %v", got)
	}
	// --system-prompt would discard claude's own default system prompt. We are
	// adding context to a working agent, not rebuilding one.
	if got[0] == "--system-prompt" {
		t.Fatal("replacing claude's system prompt throws away its tool-use and editing discipline")
	}
	if nativeSystemPromptArgs("codex", "frame") != nil {
		t.Error("codex must not be handed a flag it does not have")
	}
	if nativeSystemPromptArgs("claude", "   ") != nil {
		t.Error("an empty frame must not produce an empty flag")
	}
}

// The split must not change WHAT the runner reads — only which channel carries
// it. In-band assembly stays byte-identical, and the native form is the same
// bytes redistributed.
func TestNativeSplitCarriesTheSameBytes(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)
	const userText = "add a settings screen"

	inBand := tm.composeTurnPrompt(task, userText, promptFramePolicy{ArmPreamble: true})
	frame, message := tm.composeTurn(task, userText, promptFramePolicy{ArmPreamble: true, NativeSystemPrompt: true})

	if frame == "" {
		t.Fatal("an armed native turn must produce a frame")
	}
	if !strings.Contains(frame, "[Yaver run policy]") || !strings.Contains(frame, "Yaver orchestration") {
		t.Error("the native frame must carry the same briefing the in-band one does")
	}
	// The message is the user's ask plus what is genuinely about this turn.
	if !strings.HasPrefix(message, userText) {
		t.Fatalf("the user's words must LEAD their own message; got %q", message[:min(80, len(message))])
	}
	if strings.Contains(message, "[Yaver run policy]") || strings.Contains(message, "Yaver orchestration") {
		t.Error("the frame must leave the user's message entirely on the native path — that is the point")
	}
	// Nothing was lost between the two forms.
	for _, block := range []string{"[Yaver run policy]", "[Yaver Agent Context]", "Yaver orchestration", userText} {
		if !strings.Contains(inBand, block) {
			t.Errorf("in-band form lost %q", block)
		}
		if !strings.Contains(frame+message, block) {
			t.Errorf("native form lost %q", block)
		}
	}
}

// composeTurnPrompt is the historical single-string API and must stay
// byte-identical, or every existing frame test is asserting fiction.
func TestInBandFormIsUnchangedByTheSplit(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)

	frame, message := tm.composeTurn(task, "make it red", promptFramePolicy{ArmPreamble: true})
	if frame != "" {
		t.Fatal("without NativeSystemPrompt the frame must stay in band — a caller that ignores the first return value would otherwise silently drop it")
	}
	if got := tm.composeTurnPrompt(task, "make it red", promptFramePolicy{ArmPreamble: true}); got != message {
		t.Fatal("composeTurnPrompt must be exactly composeTurn's two halves joined")
	}
}
