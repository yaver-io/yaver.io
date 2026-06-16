package main

// routines_mcp.go — MCP-only surface for "routines": scheduled
// invocations of any registered ops verb against any machine
// (local / primary / peer deviceId), one-shot or recurring (cron or
// interval). The underlying machinery is the existing Scheduler +
// ScheduledTask; this file is just the MCP-tool wrapper that sets
// Verb / Machine / OpsPayload on a ScheduledTask and exposes 8
// CRUD-shaped tools.
//
// Deliberately NOT exposed: HTTP routes, CLI commands, mobile screens,
// web dashboard tiles, CLAUDE.md surface docs. The owner asked for an
// MCP-only surface so external AI agents (Cursor / Claude Desktop /
// Aider / Codex / Goose) can self-schedule via MCP without it
// graduating to a "feature" the rest of the product has to maintain.
//
// Auth: every MCP call goes through the owner-only /mcp endpoint
// (guests can't reach the dispatcher), so no per-tool guest check is
// needed here. Routines fire as Caller="owner" by virtue of the
// dispatcher wired in main.go.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// routineToolSchemas returns the JSON-schema descriptors for the 8
// routine_* MCP tools. Appended into the master tools list by
// mcp_tools.go::buildToolsSchema.
func routineToolSchemas() []map[string]interface{} {
	commonScheduleProps := map[string]interface{}{
		"run_at":          map[string]interface{}{"type": "string", "description": "ISO8601 UTC timestamp for one-shot run, e.g. 2026-05-05T06:00:00Z. Use this OR cron OR repeat_interval, never more than one."},
		"cron":            map[string]interface{}{"type": "string", "description": "5-field cron expression for recurring runs (minute hour day month weekday), e.g. '0 9 * * 1-5' for weekdays at 9am UTC. Use this OR run_at OR repeat_interval."},
		"repeat_interval": map[string]interface{}{"type": "integer", "description": "Repeat every N minutes. Use this OR run_at OR cron."},
		"max_runs":        map[string]interface{}{"type": "integer", "description": "Stop after this many fires (0 = unlimited)."},
	}

	return []map[string]interface{}{
		{
			"name":        "routine_create",
			"description": "Create a routine that fires an ops verb on a target machine on a schedule. Pick exactly one of run_at (one-shot), cron (recurring), or repeat_interval (every N minutes). Machine can be 'local' (this agent), 'primary' (the user's primary device alias), or any peer deviceId — peer routing uses the existing P2P relay. The verb must be one of the registered ops verbs (call ops_verbs to list).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"verb"},
				"properties": map[string]interface{}{
					"name":            map[string]interface{}{"type": "string", "description": "Human-readable label. Optional; defaults to 'routine: <verb>'."},
					"verb":            map[string]interface{}{"type": "string", "description": "Registered ops verb name (e.g. 'run', 'build', 'workspace')."},
					"machine":         map[string]interface{}{"type": "string", "description": "'local', 'primary', 'auto', or a deviceId. Defaults to 'local'."},
					"payload":         map[string]interface{}{"type": "object", "description": "Verb-specific JSON payload. Shape depends on the chosen verb (see ops_verbs)."},
					"run_at":          commonScheduleProps["run_at"],
					"cron":            commonScheduleProps["cron"],
					"repeat_interval": commonScheduleProps["repeat_interval"],
					"max_runs":        commonScheduleProps["max_runs"],
				},
			},
		},
		{
			"name":        "routine_list",
			"description": "List all routines (Verb-mode schedules) with their next run time, run count, and last result. Excludes classic Task-mode schedules created via schedule_task — call list_schedules for those.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "routine_get",
			"description": "Fetch one routine by ID, including its full execution history (last 50 runs).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "routine_delete",
			"description": "Permanently remove a routine. In-flight verb dispatches already issued will still complete; future fires are cancelled.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "routine_pause",
			"description": "Pause a routine without deleting it. The next NextRunAt is preserved but the scheduler skips it until resumed.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "routine_resume",
			"description": "Resume a paused routine. NextRunAt is recomputed from cron/repeat_interval — a one-shot RunAt that's already in the past will fire on the next tick.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "routine_run_now",
			"description": "Fire a routine immediately, out of band. Does not reset its cron cadence — useful for testing the verb invocation without waiting.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "routine_update",
			"description": "Partial update of a routine's mutable fields. Supply only the fields you want to change; omitted fields are preserved. Setting cron/run_at/repeat_interval triggers an immediate NextRunAt recompute.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id":              map[string]interface{}{"type": "string"},
					"name":            map[string]interface{}{"type": "string"},
					"machine":         map[string]interface{}{"type": "string"},
					"payload":         map[string]interface{}{"type": "object"},
					"run_at":          map[string]interface{}{"type": "string"},
					"cron":            map[string]interface{}{"type": "string"},
					"repeat_interval": map[string]interface{}{"type": "integer"},
					"max_runs":        map[string]interface{}{"type": "integer"},
				},
			},
		},
	}
}

// scheduleSelfRunawayCap bounds an interval/cron self-schedule that didn't
// specify max_runs, so a runner can't spawn an unbounded fire loop. The user
// can override with an explicit max_runs (including a larger one).
const scheduleSelfRunawayCap = 100

// scheduleSelfToolSchema describes the schedule_self MCP tool. Appended to the
// master tools list by mcp_tools.go alongside routineToolSchemas.
func scheduleSelfToolSchema() map[string]interface{} {
	return map[string]interface{}{
		"name":        "schedule_self",
		"description": "Schedule a CONTINUATION of your own work to run later — the way to handle recurring or deferred tasks instead of looping in-process or busy-waiting. Pick exactly one cadence: `when` (one-shot RFC3339 UTC), `interval_minutes` (every N minutes), or `cron` (5-field expr; supports */N steps and @daily/@hourly macros). The next run starts as a FRESH process (no memory of this turn) so put everything it needs into `prompt` and `memo`. `memo` is carried verbatim into the next run's prompt. `runner` defaults to this agent's default; pass it to pin claude/codex/opencode/glm. Recurring schedules without max_runs are capped at 100 fires.",
		"inputSchema": map[string]interface{}{
			"type":     "object",
			"required": []string{"prompt"},
			"properties": map[string]interface{}{
				"prompt":           map[string]interface{}{"type": "string", "description": "The instruction the next run executes. Self-contained — the next process has no memory of the current turn."},
				"memo":             map[string]interface{}{"type": "string", "description": "Optional notes carried verbatim into the next run's prompt (state, findings, where you left off)."},
				"when":             map[string]interface{}{"type": "string", "description": "One-shot run time, RFC3339 UTC (e.g. 2026-06-17T09:00:00Z). Use this OR interval_minutes OR cron."},
				"interval_minutes": map[string]interface{}{"type": "integer", "description": "Repeat every N minutes (minimum 1). Use this OR when OR cron."},
				"cron":             map[string]interface{}{"type": "string", "description": "5-field cron expression (minute hour day month weekday), e.g. '*/30 * * * *' or '@daily'. Use this OR when OR interval_minutes."},
				"runner":           map[string]interface{}{"type": "string", "description": "Runner for the next run: claude | codex | opencode | glm. Defaults to this agent's default runner."},
				"model":            map[string]interface{}{"type": "string", "description": "Optional model override for the next run."},
				"title":            map[string]interface{}{"type": "string", "description": "Optional label. Defaults to a truncation of prompt."},
				"max_runs":         map[string]interface{}{"type": "integer", "description": "Stop after this many fires (0 = use the 100-fire safety cap for recurring; one-shot ignores this)."},
			},
		},
	}
}

// scheduleSelf creates a Task-mode schedule from a schedule_self call. Runaway
// guards: interval floor of 1 minute, and a max_runs cap on recurring
// schedules that didn't set one.
func (s *HTTPServer) scheduleSelf(raw json.RawMessage) interface{} {
	var args struct {
		Prompt          string `json:"prompt"`
		Memo            string `json:"memo"`
		When            string `json:"when"`
		IntervalMinutes int    `json:"interval_minutes"`
		Cron            string `json:"cron"`
		Runner          string `json:"runner"`
		Model           string `json:"model"`
		Title           string `json:"title"`
		MaxRuns         int    `json:"max_runs"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return mcpToolError("prompt is required — it is what the next run executes")
	}

	cadences := 0
	if args.When != "" {
		cadences++
	}
	if args.IntervalMinutes > 0 {
		cadences++
	}
	if strings.TrimSpace(args.Cron) != "" {
		cadences++
	}
	if cadences == 0 {
		return mcpToolError("provide one cadence: when (one-shot), interval_minutes, or cron")
	}
	if cadences > 1 {
		return mcpToolError("provide exactly one of when, interval_minutes, or cron")
	}
	if args.When != "" {
		if _, err := time.Parse(time.RFC3339, args.When); err != nil {
			return mcpToolError("when must be RFC3339 UTC (e.g. 2026-06-17T09:00:00Z): " + err.Error())
		}
	}
	if args.Runner != "" && !IsSupportedRunner(args.Runner) {
		return mcpToolError("unknown runner: " + args.Runner + " (use claude, codex, opencode, or glm)")
	}

	recurring := args.IntervalMinutes > 0 || strings.TrimSpace(args.Cron) != ""
	maxRuns := args.MaxRuns
	capped := false
	if recurring && maxRuns <= 0 {
		maxRuns = scheduleSelfRunawayCap
		capped = true
	}

	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = args.Prompt
		if len(title) > 60 {
			title = strings.TrimSpace(title[:60]) + "…"
		}
	}

	st := &ScheduledTask{
		Title:          title,
		Description:    args.Prompt,
		CarryNotes:     strings.TrimSpace(args.Memo),
		Runner:         normalizeRunnerID(args.Runner),
		Model:          strings.TrimSpace(args.Model),
		RunAt:          args.When,
		RepeatInterval: args.IntervalMinutes,
		Cron:           strings.TrimSpace(args.Cron),
		MaxRuns:        maxRuns,
	}
	if err := s.scheduler.AddSchedule(st); err != nil {
		return mcpToolError(err.Error())
	}

	resp := map[string]interface{}{
		"id":        st.ID,
		"title":     st.Title,
		"runner":    firstNonEmpty(st.Runner, "(agent default)"),
		"nextRunAt": st.NextRunAt,
		"recurring": recurring,
		"hasMemo":   st.CarryNotes != "",
	}
	if st.MaxRuns > 0 {
		resp["maxRuns"] = st.MaxRuns
	}
	if capped {
		resp["note"] = fmt.Sprintf("recurring schedule capped at %d fires (no max_runs given); pass max_runs to change", scheduleSelfRunawayCap)
	}
	return mcpToolResultJSON(resp)
}

// handleRoutineMCP dispatches a routine_* MCP tool call against the
// scheduler. Returns the same map[string]interface{} shape as the
// other MCP tool branches (mcpToolResult / mcpToolError). The caller
// (handleMCPToolCallWithAddr) routes here for any tool name starting
// with "routine_".
func (s *HTTPServer) handleRoutineMCP(name string, raw json.RawMessage) interface{} {
	if s.scheduler == nil {
		return mcpToolError("scheduler not available")
	}

	switch name {
	case "routine_create":
		return s.routineCreate(raw)
	case "routine_list":
		return s.routineList()
	case "routine_get":
		return s.routineGet(raw)
	case "routine_delete":
		return s.routineDelete(raw)
	case "routine_pause":
		return s.routinePause(raw)
	case "routine_resume":
		return s.routineResume(raw)
	case "routine_run_now":
		return s.routineRunNow(raw)
	case "routine_update":
		return s.routineUpdate(raw)
	}
	return mcpToolError("unknown routine tool: " + name)
}

func (s *HTTPServer) routineCreate(raw json.RawMessage) interface{} {
	var args struct {
		Name           string          `json:"name"`
		Verb           string          `json:"verb"`
		Machine        string          `json:"machine"`
		Payload        json.RawMessage `json:"payload"`
		RunAt          string          `json:"run_at"`
		Cron           string          `json:"cron"`
		RepeatInterval int             `json:"repeat_interval"`
		MaxRuns        int             `json:"max_runs"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	args.Verb = strings.TrimSpace(args.Verb)
	if args.Verb == "" {
		return mcpToolError("verb is required")
	}
	scheduleSet := 0
	if args.RunAt != "" {
		scheduleSet++
	}
	if args.Cron != "" {
		scheduleSet++
	}
	if args.RepeatInterval > 0 {
		scheduleSet++
	}
	if scheduleSet == 0 {
		return mcpToolError("provide one of run_at, cron, or repeat_interval")
	}
	if scheduleSet > 1 {
		return mcpToolError("provide exactly one of run_at, cron, or repeat_interval")
	}
	if args.RunAt != "" {
		if _, err := time.Parse(time.RFC3339, args.RunAt); err != nil {
			return mcpToolError("run_at must be RFC3339 (e.g. 2026-05-05T06:00:00Z): " + err.Error())
		}
	}
	machine := strings.TrimSpace(args.Machine)
	if machine == "" {
		machine = "local"
	}
	title := strings.TrimSpace(args.Name)
	if title == "" {
		title = "routine: " + args.Verb
	}
	st := &ScheduledTask{
		Title:          title,
		Verb:           args.Verb,
		Machine:        machine,
		OpsPayload:     args.Payload,
		RunAt:          args.RunAt,
		Cron:           args.Cron,
		RepeatInterval: args.RepeatInterval,
		MaxRuns:        args.MaxRuns,
	}
	if err := s.scheduler.AddSchedule(st); err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolResultJSON(map[string]interface{}{
		"id":         st.ID,
		"name":       st.Title,
		"verb":       st.Verb,
		"machine":    st.Machine,
		"nextRunAt":  st.NextRunAt,
		"status":     st.Status,
		"createdAt":  st.CreatedAt,
		"recurring":  st.Cron != "" || st.RepeatInterval > 0,
		"maxRuns":    st.MaxRuns,
		"hasPayload": len(st.OpsPayload) > 0,
	})
}

func (s *HTTPServer) routineList() interface{} {
	all := s.scheduler.ListSchedules()
	var routines []map[string]interface{}
	for _, st := range all {
		if st.Verb == "" {
			continue // classic task-mode schedule, not a routine
		}
		routines = append(routines, routineSummary(st))
	}
	sort.Slice(routines, func(i, j int) bool {
		return routines[i]["createdAt"].(string) < routines[j]["createdAt"].(string)
	})
	return mcpToolResultJSON(map[string]interface{}{
		"count":    len(routines),
		"routines": routines,
	})
}

func (s *HTTPServer) routineGet(raw json.RawMessage) interface{} {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	st, ok := s.scheduler.GetSchedule(args.ID)
	if !ok {
		return mcpToolError("routine not found: " + args.ID)
	}
	if st.Verb == "" {
		return mcpToolError("not a routine (was created via schedule_task): " + args.ID)
	}
	full := routineSummary(st)
	full["history"] = st.History
	if len(st.OpsPayload) > 0 {
		full["payload"] = json.RawMessage(st.OpsPayload)
	}
	return mcpToolResultJSON(full)
}

func (s *HTTPServer) routineDelete(raw json.RawMessage) interface{} {
	id, err := readRoutineID(raw)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if st, ok := s.scheduler.GetSchedule(id); !ok || st.Verb == "" {
		return mcpToolError("routine not found: " + id)
	}
	if err := s.scheduler.RemoveSchedule(id); err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolResult("Routine deleted: " + id)
}

func (s *HTTPServer) routinePause(raw json.RawMessage) interface{} {
	id, err := readRoutineID(raw)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if st, ok := s.scheduler.GetSchedule(id); !ok || st.Verb == "" {
		return mcpToolError("routine not found: " + id)
	}
	if err := s.scheduler.PauseSchedule(id); err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolResult("Routine paused: " + id)
}

func (s *HTTPServer) routineResume(raw json.RawMessage) interface{} {
	id, err := readRoutineID(raw)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if st, ok := s.scheduler.GetSchedule(id); !ok || st.Verb == "" {
		return mcpToolError("routine not found: " + id)
	}
	if err := s.scheduler.ResumeSchedule(id); err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolResult("Routine resumed: " + id)
}

func (s *HTTPServer) routineRunNow(raw json.RawMessage) interface{} {
	id, err := readRoutineID(raw)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if st, ok := s.scheduler.GetSchedule(id); !ok || st.Verb == "" {
		return mcpToolError("routine not found: " + id)
	}
	if err := s.scheduler.RunScheduleNow(id); err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolResult("Routine fired: " + id)
}

func (s *HTTPServer) routineUpdate(raw json.RawMessage) interface{} {
	var args struct {
		ID             string           `json:"id"`
		Name           *string          `json:"name"`
		Machine        *string          `json:"machine"`
		Payload        *json.RawMessage `json:"payload"`
		RunAt          *string          `json:"run_at"`
		Cron           *string          `json:"cron"`
		RepeatInterval *int             `json:"repeat_interval"`
		MaxRuns        *int             `json:"max_runs"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	if args.ID == "" {
		return mcpToolError("id is required")
	}
	st, ok := s.scheduler.GetSchedule(args.ID)
	if !ok || st.Verb == "" {
		return mcpToolError("routine not found: " + args.ID)
	}

	// applyRoutineUpdate mutates the ScheduledTask under the
	// scheduler's write lock so concurrent fires can't observe a
	// half-updated state. Returns an error for invalid combinations
	// (e.g. setting both cron + run_at, or an unparseable run_at).
	if err := s.scheduler.applyRoutineUpdate(args.ID, func(st *ScheduledTask) error {
		if args.Name != nil {
			st.Title = strings.TrimSpace(*args.Name)
			if st.Title == "" {
				st.Title = "routine: " + st.Verb
			}
		}
		if args.Machine != nil {
			st.Machine = strings.TrimSpace(*args.Machine)
			if st.Machine == "" {
				st.Machine = "local"
			}
		}
		if args.Payload != nil {
			st.OpsPayload = *args.Payload
		}
		if args.MaxRuns != nil {
			st.MaxRuns = *args.MaxRuns
		}

		// Schedule mutation: at most one of run_at / cron /
		// repeat_interval may be set in this update. We don't
		// require it though — the caller can leave them all nil
		// to update only metadata fields.
		scheduleFields := 0
		if args.RunAt != nil {
			scheduleFields++
		}
		if args.Cron != nil {
			scheduleFields++
		}
		if args.RepeatInterval != nil {
			scheduleFields++
		}
		if scheduleFields > 1 {
			return fmt.Errorf("update only one of run_at, cron, or repeat_interval at a time")
		}
		if args.RunAt != nil {
			if *args.RunAt != "" {
				if _, err := time.Parse(time.RFC3339, *args.RunAt); err != nil {
					return fmt.Errorf("run_at must be RFC3339: %w", err)
				}
			}
			st.RunAt = *args.RunAt
			st.Cron = ""
			st.RepeatInterval = 0
			if st.RunAt != "" {
				st.NextRunAt = st.RunAt
				st.Status = "scheduled"
			} else {
				st.NextRunAt = ""
			}
		}
		if args.Cron != nil {
			st.Cron = *args.Cron
			st.RunAt = ""
			st.RepeatInterval = 0
			if st.Cron != "" {
				next := nextCronRun(st.Cron)
				if !next.IsZero() {
					st.NextRunAt = next.UTC().Format(time.RFC3339)
				} else {
					st.NextRunAt = ""
				}
				st.Status = "scheduled"
			} else {
				st.NextRunAt = ""
			}
		}
		if args.RepeatInterval != nil {
			st.RepeatInterval = *args.RepeatInterval
			st.RunAt = ""
			st.Cron = ""
			if st.RepeatInterval > 0 {
				next := time.Now().Add(time.Duration(st.RepeatInterval) * time.Minute)
				st.NextRunAt = next.UTC().Format(time.RFC3339)
				st.Status = "scheduled"
			} else {
				st.NextRunAt = ""
			}
		}
		return nil
	}); err != nil {
		return mcpToolError(err.Error())
	}

	// Re-read for the response so the caller sees the post-update state.
	st, _ = s.scheduler.GetSchedule(args.ID)
	return mcpToolResultJSON(routineSummary(st))
}

// routineSummary is the JSON shape returned by list / get / update.
// Kept compact (no History by default — get includes it) so list
// stays cheap when there are many routines.
func routineSummary(st *ScheduledTask) map[string]interface{} {
	out := map[string]interface{}{
		"id":         st.ID,
		"name":       st.Title,
		"verb":       st.Verb,
		"machine":    st.Machine,
		"status":     st.Status,
		"runCount":   st.RunCount,
		"createdAt":  st.CreatedAt,
		"hasPayload": len(st.OpsPayload) > 0,
	}
	if st.NextRunAt != "" {
		out["nextRunAt"] = st.NextRunAt
	}
	if st.LastRunAt != "" {
		out["lastRunAt"] = st.LastRunAt
	}
	if st.Cron != "" {
		out["cron"] = st.Cron
	}
	if st.RunAt != "" {
		out["runAt"] = st.RunAt
	}
	if st.RepeatInterval > 0 {
		out["repeatIntervalMinutes"] = st.RepeatInterval
	}
	if st.MaxRuns > 0 {
		out["maxRuns"] = st.MaxRuns
	}
	if n := len(st.History); n > 0 {
		out["lastResult"] = st.History[n-1]
	}
	return out
}

func readRoutineID(raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	return args.ID, nil
}

// mcpToolResultJSON wraps a structured payload as the MCP text-content
// result the existing tools return. Pretty-printed for human + agent
// readability — MCP responses are the agent's primary feedback channel
// when there's no follow-up tool call.
func mcpToolResultJSON(v interface{}) interface{} {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpToolError("marshal failed: " + err.Error())
	}
	return mcpToolResult(string(body))
}
