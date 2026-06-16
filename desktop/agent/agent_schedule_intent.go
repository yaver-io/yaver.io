package main

// agent_schedule_intent.go — engine-side fallback for the runner-agnostic
// "future work" capability. The PRIMARY path is the prompt contract
// (schedulingPreamble) telling every runner to confirm a cadence via
// yaver_ask_user and then call schedule_self. This file is the safety net for
// runners that ignore the contract: when a task whose ORIGINAL prompt implied
// recurring/deferred work finishes without anyone creating a schedule, we ask
// the human — once — whether to make it recurring, and register the schedule
// from their structured choice.
//
// Precision over recall, same stance as agent_question_fallback.go:
//   - fires at most once per task,
//   - only when the original prompt matched a conservative intent regex,
//   - never when a question is already pending,
//   - never when a schedule was created after the task started (the runner
//     already self-scheduled), and
//   - never for scheduler-spawned runs (those must not re-propose themselves).
// The answer is a fixed CHOICE, so there is no fragile free-text cadence
// parsing — each option maps to a concrete schedule.

import (
	"log"
	"regexp"
	"strings"
	"sync"
)

// schedulingIntentPatterns match an original task prompt that asks for
// recurring or deferred work. Anchored loosely (these run over the user's
// request, not runner output) but kept conservative — a false positive costs
// the user one dismissable question, so we bias toward not firing.
var schedulingIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bevery\s+(\d+\s+)?(minute|hour|day|week|month|morning|night|monday|tuesday|wednesday|thursday|friday|saturday|sunday)`),
	regexp.MustCompile(`(?i)\b(each|every)\s+(day|morning|night|hour|week)\b`),
	regexp.MustCompile(`(?i)\b(daily|hourly|weekly|nightly|periodically|recurring|on a schedule)\b`),
	regexp.MustCompile(`(?i)\b(keep (an eye on|monitoring|watching|checking)|monitor|watch for|check back)\b`),
	regexp.MustCompile(`(?i)\b(remind me|every day at|each day at|at \d{1,2}(:\d{2})?\s*(am|pm))\b`),
	regexp.MustCompile(`(?i)\bfrom now on\b`),
}

// detectSchedulingIntent reports whether a task prompt implies recurring or
// deferred work. Pure + tested in isolation.
func detectSchedulingIntent(prompt string) bool {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return false
	}
	for _, re := range schedulingIntentPatterns {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// scheduleProposeOnce gates maybeProposeSchedule to one fire per task ID.
var scheduleProposeOnce sync.Map // taskID -> struct{}

// Fixed cadence choices. Each maps deterministically to a schedule in
// scheduleFromChoice — no free-text cadence parsing.
const (
	schedChoiceDaily  = "Every day at 9am"
	schedChoiceHourly = "Every hour"
	schedChoiceWeekly = "Every week"
	schedChoiceNo     = "No, just this once"
)

// maybeProposeSchedule is called once from the natural task-completion path
// (fireTaskDone). It must be cheap and non-blocking — it does its work in a
// goroutine and returns immediately. Safe to call while tm.mu is held: it
// reads only the fields captured up front and never re-locks synchronously.
func (tm *TaskManager) maybeProposeSchedule(task *Task) {
	if task == nil {
		return
	}
	// Guards that don't need the registry/scheduler — cheap, synchronous.
	if task.Source == "scheduler" {
		return // a scheduled run must not propose its own re-schedule
	}
	if task.Status != TaskStatusFinished {
		return // only after a clean finish
	}
	if !detectSchedulingIntent(task.Description) {
		return
	}
	if _, loaded := scheduleProposeOnce.LoadOrStore(task.ID, struct{}{}); loaded {
		return // already proposed for this task
	}

	taskID := task.ID
	prompt := task.Description
	runner := task.RunnerID
	model := task.Model
	startedAt := task.StartedAt

	go func() {
		sched := ActiveScheduler()
		if sched == nil {
			return
		}
		// If the runner already self-scheduled during this task (followed the
		// prompt contract), don't double-ask.
		if startedAt != nil && sched.CreatedSince(*startedAt) {
			return
		}
		if _, busy := globalQuestionRegistry.Pending(taskID); busy {
			return
		}

		registered, answerCh, err := globalQuestionRegistry.Register(taskID, AgentQuestion{
			Prompt:     "This looked like recurring work. Want me to run it on a schedule?",
			Header:     "Schedule?",
			Kind:       "choice",
			Choices:    []string{schedChoiceDaily, schedChoiceHourly, schedChoiceWeekly, schedChoiceNo},
			TimeoutSec: 600,
		})
		if err != nil {
			return
		}
		emitTaskEvent(task, map[string]interface{}{
			"type":     "agent_question",
			"question": registered,
			"source":   "schedule_intent",
		})

		answer := <-answerCh
		if IsCancelledAnswer(answer) {
			return
		}
		st := scheduleFromChoice(answer, prompt, runner, model)
		if st == nil {
			return // "No" or an unrecognized free-text answer
		}
		if err := sched.AddSchedule(st); err != nil {
			log.Printf("[schedule-intent] task %s: AddSchedule failed: %v", taskID, err)
			return
		}
		log.Printf("[schedule-intent] task %s: created schedule %s (next %s)", taskID, st.ID, st.NextRunAt)
	}()
}

// scheduleFromChoice maps the user's cadence pick (or a lenient free-text
// "Other") to a Task-mode ScheduledTask that re-runs the original prompt.
// Returns nil for "no" / unrecognized answers (we never guess a wild cadence).
func scheduleFromChoice(answer, prompt, runner, model string) *ScheduledTask {
	base := &ScheduledTask{
		Title:       scheduleTitle(prompt),
		Description: prompt,
		Runner:      runner,
		Model:       model,
		MaxRuns:     scheduleSelfRunawayCap, // bound recurring fallback schedules
	}
	a := strings.ToLower(strings.TrimSpace(answer))
	switch {
	case a == strings.ToLower(schedChoiceNo) || strings.HasPrefix(a, "no"):
		return nil
	case a == strings.ToLower(schedChoiceDaily) || strings.Contains(a, "day") || strings.Contains(a, "daily"):
		base.Cron = "0 9 * * *"
	case a == strings.ToLower(schedChoiceHourly) || strings.Contains(a, "hour"):
		base.RepeatInterval = 60
	case a == strings.ToLower(schedChoiceWeekly) || strings.Contains(a, "week"):
		base.Cron = "0 9 * * 1"
	default:
		return nil // unrecognized free text — don't guess
	}
	return base
}

func scheduleTitle(prompt string) string {
	t := strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " "))
	if len(t) > 60 {
		t = strings.TrimSpace(t[:60]) + "…"
	}
	if t == "" {
		t = "recurring task"
	}
	return t
}
