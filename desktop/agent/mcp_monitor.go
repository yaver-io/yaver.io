package main

// mcp_monitor.go — MCP tool handlers for the Monitor features
// (errors, flags, releases, uptime, analytics). Each tool is a
// thin wrapper around the same stores the HTTP layer uses so
// Claude Code / Cursor / any MCP client can drive the same
// actions a phone tap drives.
//
// Shape: every handler takes the raw JSON arguments, unmarshals
// into a small struct, calls into the underlying store, and
// returns a mcpToolResult / mcpToolError.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// --- Errors ------------------------------------------------------------

func (s *HTTPServer) mcpErrorList(rawArgs json.RawMessage) interface{} {
	var args struct {
		IncludeResolved bool `json:"include_resolved"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	store := GlobalErrorStore()
	records := store.List(args.IncludeResolved)
	stats := store.Stats()
	if len(records) == 0 {
		return mcpToolResult(fmt.Sprintf("No errors. Stats: %+v", stats))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Stats: open=%d resolved=%d last24h=%d total=%d\n\n",
		stats["open"], stats["resolved"], stats["openLast24h"], stats["totalDistinct"]))
	for i, r := range records {
		if i >= 20 {
			sb.WriteString(fmt.Sprintf("... +%d more (include_resolved=%v)\n", len(records)-i, args.IncludeResolved))
			break
		}
		marker := " "
		if r.Fatal {
			marker = "💥"
		}
		if r.Resolved {
			marker = "✓"
		}
		sb.WriteString(fmt.Sprintf("%s [%s] ×%d  %s\n    devices=%d  last=%s  fp=%s\n",
			marker, truncateForMCP(r.Message, 60), r.Count,
			firstStackLine(r.Stack), len(r.DeviceIDs), r.LastSeenAt, r.Fingerprint))
	}
	return mcpToolResult(sb.String())
}

func (s *HTTPServer) mcpErrorResolve(rawArgs json.RawMessage) interface{} {
	var args struct {
		Fingerprint string `json:"fingerprint"`
		Note        string `json:"note"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.Fingerprint == "" {
		return mcpToolError("fingerprint is required")
	}
	if !GlobalErrorStore().MarkResolved(args.Fingerprint, args.Note) {
		return mcpToolError("fingerprint not found: " + args.Fingerprint)
	}
	return mcpToolResult("✓ resolved " + args.Fingerprint)
}

// --- Flags -------------------------------------------------------------

func (s *HTTPServer) mcpFlagList() interface{} {
	list := globalFlagStore().List()
	if len(list) == 0 {
		return mcpToolResult("No flags yet.")
	}
	var sb strings.Builder
	for _, f := range list {
		val := "false"
		if f.Type == "bool" {
			val = fmt.Sprintf("%v", f.DefaultBool)
		} else {
			val = "\"" + f.DefaultString + "\""
		}
		sb.WriteString(fmt.Sprintf("- %s  type=%s  default=%s  rollout=%d%%  overrides=%d  %s\n",
			f.Key, f.Type, val, f.RolloutPercent, len(f.Overrides), f.Description))
	}
	return mcpToolResult(sb.String())
}

func (s *HTTPServer) mcpFlagSet(rawArgs json.RawMessage) interface{} {
	var args struct {
		Key            string `json:"key"`
		Type           string `json:"type"`
		DefaultBool    bool   `json:"defaultBool"`
		DefaultString  string `json:"defaultString"`
		RolloutPercent int    `json:"rolloutPercent"`
		Description    string `json:"description"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.Key == "" {
		return mcpToolError("key is required")
	}
	if args.Type == "" {
		args.Type = "bool"
	}
	if args.RolloutPercent < 0 || args.RolloutPercent > 100 {
		return mcpToolError("rolloutPercent must be 0..100")
	}
	globalFlagStore().Set(Flag{
		Key:            args.Key,
		Type:           args.Type,
		DefaultBool:    args.DefaultBool,
		DefaultString:  args.DefaultString,
		RolloutPercent: args.RolloutPercent,
		Description:    args.Description,
	})
	return mcpToolResult(fmt.Sprintf("✓ flag %s set (type=%s rollout=%d%%)",
		args.Key, args.Type, args.RolloutPercent))
}

func (s *HTTPServer) mcpFlagEvaluate(rawArgs json.RawMessage) interface{} {
	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.UserID == "" {
		args.UserID = "anonymous"
	}
	values := globalFlagStore().EvaluateAll(args.UserID)
	if len(values) == 0 {
		return mcpToolResult("No flags to evaluate.")
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("user=%s\n", args.UserID))
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("  %s = %v\n", k, values[k]))
	}
	return mcpToolResult(sb.String())
}

// --- Releases ----------------------------------------------------------

func (s *HTTPServer) mcpReleaseList(rawArgs json.RawMessage) interface{} {
	var args struct {
		Channel string `json:"channel"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	if args.Channel == "" {
		args.Channel = "production"
	}
	m, err := loadManifest(args.Channel)
	if err != nil {
		return mcpToolError("load manifest: " + err.Error())
	}
	if len(m.Releases) == 0 {
		return mcpToolResult(fmt.Sprintf("No releases in channel %q yet.", args.Channel))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("channel=%s latest=%s rollout=%d%%\n\n",
		args.Channel, m.Latest, m.RolloutPercent))
	for i, r := range m.Releases {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("... +%d older releases\n", len(m.Releases)-i))
			break
		}
		marker := " "
		if r.Semver == m.Latest {
			marker = "→"
		}
		sb.WriteString(fmt.Sprintf("%s %s  %d bytes  bc%d  %s\n",
			marker, r.Semver, r.Size, r.HermesBCVersion, r.PublishedAt))
		if r.Notes != "" {
			sb.WriteString("    " + r.Notes + "\n")
		}
	}
	return mcpToolResult(sb.String())
}

func (s *HTTPServer) mcpReleaseRollout(rawArgs json.RawMessage) interface{} {
	var args struct {
		Channel string `json:"channel"`
		Percent int    `json:"percent"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.Channel == "" || args.Percent < 0 || args.Percent > 100 {
		return mcpToolError("channel and 0..100 percent required")
	}
	releaseMu.Lock()
	defer releaseMu.Unlock()
	m, err := loadManifest(args.Channel)
	if err != nil {
		return mcpToolError("load manifest: " + err.Error())
	}
	m.RolloutPercent = args.Percent
	if err := saveManifest(args.Channel, m); err != nil {
		return mcpToolError("save manifest: " + err.Error())
	}
	return mcpToolResult(fmt.Sprintf("✓ %s rollout → %d%%", args.Channel, args.Percent))
}

func (s *HTTPServer) mcpReleaseRollback(rawArgs json.RawMessage) interface{} {
	var args struct {
		Channel string `json:"channel"`
		Semver  string `json:"semver"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.Channel == "" || args.Semver == "" {
		return mcpToolError("channel and semver required")
	}
	releaseMu.Lock()
	defer releaseMu.Unlock()
	m, err := loadManifest(args.Channel)
	if err != nil {
		return mcpToolError("load manifest: " + err.Error())
	}
	found := false
	for _, r := range m.Releases {
		if r.Semver == args.Semver {
			found = true
			break
		}
	}
	if !found {
		return mcpToolError(fmt.Sprintf("release %q not in channel %q", args.Semver, args.Channel))
	}
	m.Latest = args.Semver
	if err := saveManifest(args.Channel, m); err != nil {
		return mcpToolError("save manifest: " + err.Error())
	}
	return mcpToolResult(fmt.Sprintf("✓ %s latest → %s", args.Channel, args.Semver))
}

// --- Uptime monitors ---------------------------------------------------

func (s *HTTPServer) mcpMonitorList() interface{} {
	list, err := loadMonitors()
	if err != nil {
		return mcpToolError("load monitors: " + err.Error())
	}
	if len(list) == 0 {
		return mcpToolResult("No monitors. Use monitor_add to create one.")
	}
	var sb strings.Builder
	for _, m := range list {
		state := m.State
		if m.Paused {
			state = "paused"
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s %s  every %s  streak %d  last %s\n",
			state, m.Name, m.URL, m.Interval, m.Streak, m.LastCheckAt))
	}
	return mcpToolResult(sb.String())
}

func (s *HTTPServer) mcpMonitorAdd(rawArgs json.RawMessage) interface{} {
	var args struct {
		URL      string `json:"url"`
		Name     string `json:"name"`
		Interval string `json:"interval"`
		Method   string `json:"method"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.URL == "" {
		return mcpToolError("url is required")
	}
	if !strings.HasPrefix(args.URL, "http") {
		args.URL = "https://" + args.URL
	}
	if args.Interval == "" {
		args.Interval = "60s"
	}
	if args.Method == "" {
		args.Method = "GET"
	}
	monitorMu.Lock()
	defer monitorMu.Unlock()
	list, err := loadMonitors()
	if err != nil {
		return mcpToolError("load monitors: " + err.Error())
	}
	m := &Monitor{
		ID:        randomID(),
		Name:      args.Name,
		URL:       args.URL,
		Interval:  args.Interval,
		Method:    strings.ToUpper(args.Method),
		State:     "unknown",
		CreatedAt: nowRFC3339(),
	}
	if m.Name == "" {
		m.Name = deriveMonitorName(args.URL)
	}
	list = append(list, m)
	if err := saveMonitors(list); err != nil {
		return mcpToolError("save monitors: " + err.Error())
	}
	return mcpToolResult(fmt.Sprintf("✓ added %s %s  every %s", m.ID, m.URL, m.Interval))
}

func (s *HTTPServer) mcpMonitorRemove(rawArgs json.RawMessage) interface{} {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments")
	}
	if args.ID == "" {
		return mcpToolError("id is required")
	}
	monitorMu.Lock()
	defer monitorMu.Unlock()
	list, err := loadMonitors()
	if err != nil {
		return mcpToolError("load monitors: " + err.Error())
	}
	filtered := list[:0]
	hit := false
	for _, m := range list {
		if m.ID == args.ID || m.Name == args.ID {
			hit = true
			continue
		}
		filtered = append(filtered, m)
	}
	if !hit {
		return mcpToolError("monitor not found: " + args.ID)
	}
	if err := saveMonitors(filtered); err != nil {
		return mcpToolError("save monitors: " + err.Error())
	}
	return mcpToolResult("✓ removed " + args.ID)
}

// --- Analytics events --------------------------------------------------

func (s *HTTPServer) mcpAnalyticsEvents(rawArgs json.RawMessage) interface{} {
	var args struct {
		Since int64 `json:"since"`
		Limit int   `json:"limit"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	events := analyticsTail(args.Since, args.Limit)
	if len(events) == 0 {
		return mcpToolResult("No recent track events.")
	}
	var sb strings.Builder
	for i, ev := range events {
		if i >= 30 {
			sb.WriteString(fmt.Sprintf("... +%d more\n", len(events)-i))
			break
		}
		sb.WriteString(fmt.Sprintf("- %d  %s  route=%s  device=%s\n    props=%v\n",
			ev.Timestamp, ev.Name, ev.Route, ev.DeviceID, ev.Props))
	}
	return mcpToolResult(sb.String())
}

// truncateForMCP keeps a string under n runes-ish for tool output.
func truncateForMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
