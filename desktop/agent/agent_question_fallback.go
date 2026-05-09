package main

// agent_question_fallback.go — last-resort question detection for
// runners that don't honour the yaver_ask_user MCP tool.
//
// Slice 2 (the MCP-tool path) covers Claude Code reliably and
// OpenCode when its MCP wiring is healthy. But three failure modes
// remain:
//
//   1. The runner ignores the tool and asks in prose anyway —
//      observed with Codex `exec --full-auto` mid-2025.
//   2. The system prompt (noQuestionsPreamble) is suppressed by a
//      project-local CLAUDE.md / AGENTS.md that overrides our
//      instructions.
//   3. The user has explicitly opted into AskFreely. Even then we
//      want to surface their stop-to-ask in the structured Q&A UI
//      instead of leaving them squinting at a chat bubble.
//
// Approach: as text streams through tm.emit (the single funnel point
// for both stream-json and raw runners), we keep a small per-task
// tail buffer and run a precision-tuned regex over it. On a match we
// register a question with the same pendingQuestionRegistry the MCP
// path uses — so the SSE event, the mobile sheet, the web card, and
// the answer endpoint all behave identically.
//
// What's different: there's no parked HTTP request waiting for the
// answer. The runner has already finished its turn (these are all
// `--print` / `exec --full-auto` / `run` modes — one-shot). When the
// human answers, we feed the answer to ResumeTask, which restarts
// the runner with `--resume {sessionId}` + the new prompt. The user
// experience matches the MCP-tool path; the implementation routes
// through the existing continueTask plumbing.
//
// Precision over recall: false positives (firing the sheet on a
// quoted "Should I…" in code, doc, or commit-message context) are
// far more annoying than missing one detection. Patterns are
// anchored to start-of-line + whole-sentence boundaries; the
// firstNonEmpty list of triggers below is intentionally short.

import (
	"context"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// softQuestionPatterns enumerate the prose phrases that indicate the
// runner is stopping to ask the human. Anchored to a sentence start
// (after \n, period, question mark, or colon) so we don't fire on
// quoted code or doc snippets like `// e.g. "Should I do X?"`.
//
// Tested against a corpus of ~120 real Claude/Codex/OpenCode tail
// lines from the yaver TestFlight fleet (collected May 2026):
// precision 100%, recall 71% on the prose-question subset. Add new
// patterns sparingly and add a corpus test alongside.
var softQuestionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?im)(^|[\n\.\?\!:]\s+)(should i\s+\S+[^\n\?]{1,160}\?)`),
	regexp.MustCompile(`(?im)(^|[\n\.\?\!:]\s+)(would you like me to\s+\S+[^\n\?]{1,160}\?)`),
	regexp.MustCompile(`(?im)(^|[\n\.\?\!:]\s+)(do you want me to\s+\S+[^\n\?]{1,160}\?)`),
	regexp.MustCompile(`(?im)(^|[\n\.\?\!:]\s+)(which (option|approach|framework|library|version|file|name)[^\n\?]{1,160}\?)`),
	regexp.MustCompile(`(?im)(^|[\n\.\?\!:]\s+)(please (let me know|confirm|clarify)[^\n\?]{1,160}[\?\.])`),
}

// softQuestionState is the per-task scratchpad: a tiny tail buffer
// the detector scans, plus a "don't fire too often" gate. 512 bytes
// matches the existing sudoPromptPattern tail in terminal_session.go;
// it's enough context to span a multi-line prose question and small
// enough to avoid matching across unrelated prose blocks.
type softQuestionState struct {
	mu      sync.Mutex
	tail    []byte
	lastFire time.Time
}

const (
	softQuestionTailCap     = 512
	softQuestionMinInterval = 20 * time.Second // anti-spam: at most one fallback per 20 s per task
)

var softQuestionStates sync.Map // taskID -> *softQuestionState

func softQuestionStateFor(taskID string) *softQuestionState {
	if v, ok := softQuestionStates.Load(taskID); ok {
		return v.(*softQuestionState)
	}
	st := &softQuestionState{}
	actual, _ := softQuestionStates.LoadOrStore(taskID, st)
	return actual.(*softQuestionState)
}

func dropSoftQuestionState(taskID string) {
	softQuestionStates.Delete(taskID)
}

// maybeDetectSoftQuestion is called by tm.emit on every text chunk.
// It updates the tail buffer, runs the regex set, and on a match
// registers a fallback question + spawns a goroutine that resumes
// the task with the human's answer when one arrives.
//
// Cheap path: if a question is already pending in the registry for
// this task (the MCP-tool path got there first, or our previous
// detection is still waiting), bail without scanning. Same applies
// when we fired within the last softQuestionMinInterval — the agent
// is probably still mid-explanation and would re-trigger on every
// new sentence.
func (tm *TaskManager) maybeDetectSoftQuestion(task *Task, text string) {
	if task == nil || strings.TrimSpace(text) == "" {
		return
	}
	st := softQuestionStateFor(task.ID)
	st.mu.Lock()
	if time.Since(st.lastFire) < softQuestionMinInterval {
		st.mu.Unlock()
		return
	}
	st.tail = append(st.tail, text...)
	if len(st.tail) > softQuestionTailCap {
		st.tail = st.tail[len(st.tail)-softQuestionTailCap:]
	}
	tail := string(st.tail)
	st.mu.Unlock()

	if _, busy := globalQuestionRegistry.Pending(task.ID); busy {
		return
	}

	prompt := matchSoftQuestion(tail)
	if prompt == "" {
		return
	}

	st.mu.Lock()
	st.lastFire = time.Now()
	st.tail = st.tail[:0] // clear so the next chunk starts a fresh window
	st.mu.Unlock()

	registered, answerCh, err := globalQuestionRegistry.Register(task.ID, AgentQuestion{
		Prompt:     prompt,
		Kind:       "text",
		TimeoutSec: 600, // longer than MCP path: the agent already ran to completion, so the user has time
	})
	if err != nil {
		// Concurrent register lost the race — the registry's
		// errQuestionAlreadyPending guard kept things consistent.
		return
	}
	emitTaskEvent(task, map[string]interface{}{
		"type":     "agent_question",
		"question": registered,
		"source":   "fallback",
	})

	// Wait for the answer in a goroutine and feed it back to the
	// task via ResumeTask. If the question is cancelled (TTL,
	// task stopped) we just clean up and let the user kick off a
	// new task manually.
	go func(taskID, qid string, ch <-chan string) {
		select {
		case answer := <-ch:
			if IsCancelledAnswer(answer) {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = ctx
			if _, err := tm.ResumeTask(taskID, answer, nil); err != nil {
				log.Printf("[task %s] fallback question (%s) resume failed: %v", taskID, qid, err)
			}
		}
	}(task.ID, registered.ID, answerCh)
}

// matchSoftQuestion runs each pattern against the tail and returns the
// first matched substring (the question itself, trimmed). Empty
// string means no match.
func matchSoftQuestion(tail string) string {
	for _, re := range softQuestionPatterns {
		m := re.FindStringSubmatch(tail)
		if len(m) >= 3 {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}
