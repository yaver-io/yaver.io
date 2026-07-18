package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

func autorunStateTopic(deviceID, slot string) string {
	deviceID = strings.TrimSpace(deviceID)
	slot = recapSlotLabel(strings.TrimSpace(slot))
	if deviceID == "" || slot == "" {
		return ""
	}
	return "autorun/" + deviceID + "/" + slot
}

func autorunCurrentState(opts autorunOptions, runnerID, kind, status, finishReason string, summary autorunRunSummary) autorunStateEvent {
	now := time.Now().UnixMilli()
	ev := autorunStateEvent{
		RunID:     strings.TrimSpace(opts.SessionID),
		Slot:      recapSlotLabel(opts.Slot),
		Task:      autorunTaskName(opts.TaskPath),
		Kind:      kind,
		Status:    status,
		Iteration: summary.Iterations,
		MaxIters:  opts.MaxIters,
		Runner:    strings.TrimSpace(runnerID),
		Master:    strings.TrimSpace(summary.Master),
		Commits:   summary.Commits,
		Heals:     len(summary.Heals),
		Finish:    strings.TrimSpace(finishReason),
		At:        now,
	}
	if ev.Slot == "" {
		ev.Slot = recapSlotLabel(autorunSlotKey(opts.TaskPath, opts.Runner))
	}
	if ev.Runner == "" {
		ev.Runner = strings.TrimSpace(summary.Runner)
	}
	return ev
}

func publishAutorunState(ctx context.Context, opts autorunOptions, runnerID, kind, status, finishReason string, summary autorunRunSummary) {
	b := bus()
	if b == nil {
		return
	}
	topic := autorunStateTopic(localDeviceID(), opts.Slot)
	if topic == "" {
		return
	}
	ev := autorunCurrentState(opts, runnerID, kind, status, finishReason, summary)
	if _, err := b.Publish(ctx, topic, ev, autorunStateRetainSec, 1); err != nil {
		log.Printf("[autorun-bus] publish %s failed for %s: %v", kind, topic, err)
	}
}

type autorunRunCacheRow struct {
	DeviceID     string `json:"deviceId"`
	RunID        string `json:"runId"`
	Slot         string `json:"slot"`
	Task         string `json:"task"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Iteration    int    `json:"iteration"`
	MaxIters     int    `json:"maxIters"`
	Runner       string `json:"runner"`
	Master       string `json:"master,omitempty"`
	Commits      int    `json:"commits"`
	Heals        int    `json:"heals"`
	FinishReason string `json:"finishReason,omitempty"`
	At           int64  `json:"at"`
	AgeMs        int64  `json:"ageMs"`
}

// autorunRefreshView is the strict whitelist the cache-refresh path accepts
// from autorun_status. It deliberately omits every free-text field
// (progressTail, heal detail, workDir, progressPath, errors) so a remote
// runner's authored bytes never enter the retained autorun cache path.
type autorunRefreshView struct {
	ID           string     `json:"id"`
	Slot         string     `json:"slot"`
	Task         string     `json:"task"`
	Runner       string     `json:"runner"`
	MaxIters     int        `json:"maxIters"`
	Status       string     `json:"status"`
	Iterations   int        `json:"iterations"`
	Commits      int        `json:"commits"`
	FinishReason string     `json:"finishReason,omitempty"`
	ActiveRunner string     `json:"activeRunner,omitempty"`
	Master       string     `json:"master,omitempty"`
	Heals        []struct{} `json:"heals,omitempty"`
}

func autorunStateFromBusEvent(evt BusEvent) (autorunRunCacheRow, bool) {
	var st autorunStateEvent
	if err := json.Unmarshal(evt.Payload, &st); err != nil {
		return autorunRunCacheRow{}, false
	}
	deviceID, ok := autorunDeviceFromTopic(evt.Topic)
	if !ok {
		return autorunRunCacheRow{}, false
	}
	publishedAt := evt.PublishedAt
	if publishedAt == 0 {
		publishedAt = st.At
	}
	age := time.Now().UnixMilli() - publishedAt
	if age < 0 {
		age = 0
	}
	return autorunRunCacheRow{
		DeviceID:     deviceID,
		RunID:        st.RunID,
		Slot:         recapSlotLabel(st.Slot),
		Task:         autorunTaskName(st.Task),
		Kind:         st.Kind,
		Status:       st.Status,
		Iteration:    st.Iteration,
		MaxIters:     st.MaxIters,
		Runner:       st.Runner,
		Master:       st.Master,
		Commits:      st.Commits,
		Heals:        st.Heals,
		FinishReason: st.Finish,
		At:           publishedAt,
		AgeMs:        age,
	}, true
}

func autorunDeviceFromTopic(topic string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(topic), "/")
	if len(parts) != 3 || parts[0] != "autorun" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return parts[1], true
}

func autorunRunsFromCache(machine string) ([]autorunRunCacheRow, map[string]int64) {
	b := bus()
	if b == nil {
		return nil, map[string]int64{}
	}
	machine = strings.TrimSpace(machine)
	if machine == "" {
		machine = "all"
	}
	rows := make([]autorunRunCacheRow, 0)
	ages := map[string]int64{}
	for _, evt := range b.Retained("autorun/") {
		row, ok := autorunStateFromBusEvent(evt)
		if !ok {
			continue
		}
		if machine != "all" && !(machine == "local" && row.DeviceID == localDeviceID()) && row.DeviceID != machine {
			continue
		}
		rows = append(rows, row)
		ages[row.DeviceID+"/"+row.Slot] = row.AgeMs
	}
	sortAutorunCacheRows(rows)
	return rows, ages
}

func sortAutorunCacheRows(rows []autorunRunCacheRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DeviceID != rows[j].DeviceID {
			return rows[i].DeviceID < rows[j].DeviceID
		}
		if rows[i].Slot != rows[j].Slot {
			return rows[i].Slot < rows[j].Slot
		}
		return rows[i].RunID < rows[j].RunID
	})
}

func autorunRefreshTargets(machine string, rows []autorunRunCacheRow) []string {
	machine = strings.TrimSpace(machine)
	if machine == "" || machine == "all" {
		seen := map[string]struct{}{}
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.DeviceID == "" {
				continue
			}
			if _, ok := seen[row.DeviceID]; ok {
				continue
			}
			seen[row.DeviceID] = struct{}{}
			out = append(out, row.DeviceID)
		}
		if len(out) == 0 {
			if lt := globalLeader; lt != nil {
				for _, peer := range lt.Peers() {
					deviceID := strings.TrimSpace(peer.DeviceID)
					if deviceID == "" {
						continue
					}
					if _, ok := seen[deviceID]; ok {
						continue
					}
					seen[deviceID] = struct{}{}
					out = append(out, deviceID)
				}
			}
			if dev := strings.TrimSpace(localDeviceID()); dev != "" {
				if _, ok := seen[dev]; !ok {
					out = append(out, dev)
				}
			}
		}
		return out
	}
	if machine == "local" {
		if dev := strings.TrimSpace(localDeviceID()); dev != "" {
			return []string{dev}
		}
	}
	return []string{machine}
}

func refreshAutorunRunsAsync(c OpsContext, targets []string) {
	if len(targets) == 0 {
		return
	}
	headers := http.Header{}
	if c.RequestHeaders != nil {
		headers = c.RequestHeaders.Clone()
	}
	server := c.Server
	ctx := context.WithoutCancel(c.Ctx)
	go func() {
		refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		for _, target := range targets {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			if err := refreshAutorunRunsFromMachine(refreshCtx, server, headers, target); err != nil {
				log.Printf("[autorun-bus] refresh %s failed: %v", target, err)
			}
		}
	}()
}

func refreshAutorunRunsFromMachine(ctx context.Context, server *HTTPServer, headers http.Header, machine string) error {
	if machine == localDeviceID() || machine == "local" {
		mergeRefreshedAutorunViews(machine, autorunSessions.refreshViews())
		return nil
	}
	reqBody, err := json.Marshal(map[string]any{
		"machine": machine,
		"verb":    "autorun_status",
		"payload": map[string]any{},
	})
	if err != nil {
		return err
	}
	userBearer := ""
	if headers != nil {
		userBearer = strings.TrimSpace(strings.TrimPrefix(headers.Get("Authorization"), "Bearer "))
	}
	status, body, err := proxyToDeviceAs(ctx, "ops:autorun_runs_refresh", machine, "POST", "/ops", reqBody, userBearer)
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("peer returned HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	var res OpsResult
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("peer returned unreadable ops body: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("%s", res.Error)
	}
	initial, ok := res.Initial.(map[string]any)
	if !ok {
		raw, err := json.Marshal(res.Initial)
		if err != nil {
			return fmt.Errorf("autorun_status initial body missing")
		}
		if err := json.Unmarshal(raw, &initial); err != nil {
			return fmt.Errorf("autorun_status initial body unreadable: %w", err)
		}
	}
	rawSessions, ok := initial["sessions"]
	if !ok {
		return nil
	}
	sessionsJSON, err := json.Marshal(rawSessions)
	if err != nil {
		return err
	}
	var sessions []autorunRefreshView
	if err := json.Unmarshal(sessionsJSON, &sessions); err != nil {
		return err
	}
	mergeRefreshedAutorunViews(machine, sessions)
	return nil
}

func pruneAutorunCacheDevice(deviceID string) {
	b := bus()
	if b == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || deviceID == "local" {
		deviceID = localDeviceID()
	}
	if deviceID == "" {
		return
	}
	prefix := "autorun/" + deviceID + "/"
	b.mu.Lock()
	defer b.mu.Unlock()
	for topic := range b.retained {
		if topicMatches(topic, prefix) {
			delete(b.retained, topic)
		}
	}
}

func mergeRefreshedAutorunViews(deviceID string, views []autorunRefreshView) {
	pruneAutorunCacheDevice(deviceID)
	for _, view := range views {
		cacheAutorunRefresh(deviceID, view)
	}
}

func cacheAutorunRefresh(deviceID string, view autorunRefreshView) {
	b := bus()
	if b == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || deviceID == "local" {
		deviceID = localDeviceID()
	}
	topic := autorunStateTopic(deviceID, recapSlotLabel(view.Slot))
	if topic == "" {
		return
	}
	runner := strings.TrimSpace(view.ActiveRunner)
	if runner == "" {
		runner = strings.TrimSpace(view.Runner)
	}
	ev := autorunStateEvent{
		RunID:     strings.TrimSpace(view.ID),
		Slot:      recapSlotLabel(view.Slot),
		Task:      autorunTaskName(view.Task),
		Kind:      autorunKindFromRefreshView(view),
		Status:    strings.TrimSpace(view.Status),
		Iteration: view.Iterations,
		MaxIters:  view.MaxIters,
		Runner:    runner,
		Master:    strings.TrimSpace(view.Master),
		Commits:   view.Commits,
		Heals:     len(view.Heals),
		Finish:    strings.TrimSpace(view.FinishReason),
		At:        time.Now().UnixMilli(),
	}
	injectRetainedBusEvent(BusEvent{
		ID:          generateBusEventID(deviceID, time.Now()),
		Topic:       topic,
		Publisher:   deviceID,
		PublishedAt: ev.At,
		TTL:         autorunStateRetainSec,
		QoS:         1,
		Payload:     mustMarshalRaw(ev),
	})
}

func mustMarshalRaw(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func injectRetainedBusEvent(evt BusEvent) {
	b := bus()
	if b == nil {
		return
	}
	if evt.Publisher == localDeviceID() {
		b.deliverLocal(evt)
		b.mu.Lock()
		b.retained[evt.Topic] = evt
		b.mu.Unlock()
		return
	}
	b.Receive(evt)
}

func autorunKindFromView(view autorunSessionView) string {
	switch strings.TrimSpace(view.Status) {
	case "running":
		return "iteration"
	case "stopping", "stopped":
		return "stopped"
	case "failed":
		if strings.TrimSpace(view.FinishReason) == autorunReasonGate {
			return "gate_fail"
		}
		return "failed"
	case "completed":
		switch strings.TrimSpace(view.FinishReason) {
		case autorunReasonDone:
			return "done"
		case autorunReasonConverged:
			return "converged"
		default:
			return "done"
		}
	}
	return "iteration"
}

func autorunKindFromRefreshView(view autorunRefreshView) string {
	switch strings.TrimSpace(view.Status) {
	case "running":
		return "iteration"
	case "stopping", "stopped":
		return "stopped"
	case "failed":
		if strings.TrimSpace(view.FinishReason) == autorunReasonGate {
			return "gate_fail"
		}
		return "failed"
	case "completed":
		switch strings.TrimSpace(view.FinishReason) {
		case autorunReasonDone:
			return "done"
		case autorunReasonConverged:
			return "converged"
		default:
			return "done"
		}
	}
	return "iteration"
}

func autorunKindForFinish(reason string) string {
	switch strings.TrimSpace(reason) {
	case autorunReasonDone:
		return "done"
	case autorunReasonConverged:
		return "converged"
	case autorunReasonGate:
		return "gate_fail"
	case autorunReasonStopped:
		return "stopped"
	case autorunReasonRunner, autorunReasonScope, autorunReasonResources:
		return "failed"
	case autorunReasonMaxIters:
		return "done"
	default:
		return "failed"
	}
}

func autorunStatusForFinish(reason string) string {
	switch strings.TrimSpace(reason) {
	case autorunReasonStopped:
		return "stopped"
	case autorunReasonGate, autorunReasonRunner, autorunReasonScope, autorunReasonResources:
		return "failed"
	default:
		return "completed"
	}
}

func opsAutorunRunsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Machine string `json:"machine"`
		Refresh bool   `json:"refresh"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	rows, ages := autorunRunsFromCache(p.Machine)
	refreshed := []string{}
	if p.Refresh {
		refreshed = autorunRefreshTargets(p.Machine, rows)
		refreshAutorunRunsAsync(c, refreshed)
	}
	return OpsResult{OK: true, Initial: map[string]any{
		"runs":      rows,
		"fromCache": true,
		"ages":      ages,
		"refreshed": refreshed,
	}}
}
