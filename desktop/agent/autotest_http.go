package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/testkit"
)

type autotestRunState struct {
	Req      testkit.AutoTestRequest `json:"request"`
	Result   *testkit.AutoTestResult `json:"result,omitempty"`
	Events   []testkit.AutoTestEvent `json:"events"`
	Running  bool                    `json:"running"`
	Error    string                  `json:"error,omitempty"`
	Cancel   context.CancelFunc      `json:"-"`
	StreamID string                  `json:"streamId,omitempty"`
}

type autotestManager struct {
	mu     sync.Mutex
	runs   map[string]*autotestRunState
	latest string
}

var autotestGlobal = &autotestManager{runs: map[string]*autotestRunState{}}

func (s *HTTPServer) handleAutotestStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req testkit.AutoTestRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	res, code, err := startAutotestRun(r.Context(), s, req)
	if err != nil {
		jsonError(w, code, err.Error())
		return
	}
	jsonReply(w, http.StatusAccepted, res)
}

func (s *HTTPServer) handleAutotestStatus(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("runId")
	st, ok := autotestGlobal.get(runID)
	if !ok {
		jsonError(w, http.StatusNotFound, "autotest run not found")
		return
	}
	jsonReply(w, http.StatusOK, st)
}

func (s *HTTPServer) handleAutotestStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		RunID string `json:"runId"`
	}
	_ = decodeJSONBody(r, &body)
	st, ok := autotestGlobal.get(body.RunID)
	if !ok {
		jsonError(w, http.StatusNotFound, "autotest run not found")
		return
	}
	if st.Cancel != nil {
		st.Cancel()
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "runId": body.RunID})
}

func (s *HTTPServer) handleAutotestResults(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/autotest/results/")
	if runID == "" || runID == "/autotest/results" || runID == "latest" {
		runID = ""
	}
	st, ok := autotestGlobal.get(runID)
	if !ok {
		jsonError(w, http.StatusNotFound, "autotest results not found")
		return
	}
	jsonReply(w, http.StatusOK, st.Result)
}

func (s *HTTPServer) handleAutotestApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      false,
		"code":    "approval_pending",
		"message": "phase-1 records proposed fixes locally; branch merge approval is not wired yet",
	})
}

func (s *HTTPServer) handleAutotestSuite(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"viewports": testkit.AutoTestViewports,
	})
}

func startAutotestRun(ctx context.Context, s *HTTPServer, req testkit.AutoTestRequest) (map[string]interface{}, int, error) {
	if req.WorkDir == "" {
		req.WorkDir = "."
	}
	abs, err := filepath.Abs(req.WorkDir)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	req.WorkDir = abs
	if req.ACPowerOnly {
		hs := testkit.SnapshotHost()
		if ok, why := testkit.ShouldRun(hs, true, 0); !ok {
			return nil, http.StatusServiceUnavailable, fmt.Errorf("skipped: %s", why)
		}
	}

	runID := testkit.FormatTimestamp(time.Now())
	streamID := "autotest-" + runID
	runCtx, cancel := context.WithCancel(context.Background())
	req.RunID = runID
	st := &autotestRunState{Req: req, Running: true, Cancel: cancel, StreamID: streamID}

	autotestGlobal.mu.Lock()
	autotestGlobal.runs[runID] = st
	autotestGlobal.latest = runID
	autotestGlobal.mu.Unlock()

	stream := (*LogStream)(nil)
	if s != nil && s.streams != nil {
		stream = s.streams.Get(streamID)
	}
	go func() {
		emit := func(ev testkit.AutoTestEvent) {
			autotestGlobal.mu.Lock()
			st.Events = append(st.Events, ev)
			autotestGlobal.mu.Unlock()
			if stream != nil {
				stream.AppendEvent(map[string]interface{}{
					"type":          "autotest",
					"runId":         runID,
					"phase":         ev.Phase,
					"flow":          ev.Flow,
					"progress":      ev.Progress,
					"total":         ev.Total,
					"bugsFound":     ev.BugsFound,
					"proposed":      ev.Proposed,
					"nativeSkipped": ev.NativeSkipped,
					"message":       ev.Message,
				})
			}
		}
		res, err := testkit.RunAutoTest(runCtx, req, emit)
		autotestGlobal.mu.Lock()
		st.Result = res
		st.Running = false
		st.Cancel = nil
		if err != nil {
			st.Error = err.Error()
		}
		autotestGlobal.mu.Unlock()
		if stream != nil {
			stream.AppendEvent(map[string]interface{}{"type": "autotest_done", "runId": runID, "ok": err == nil && res != nil && res.Passed})
		}
	}()

	_ = ctx
	return map[string]interface{}{
		"ok":       true,
		"runId":    runID,
		"streamId": streamID,
		"workDir":  abs,
	}, http.StatusAccepted, nil
}

func (m *autotestManager) get(runID string) (*autotestRunState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runID == "" {
		runID = m.latest
	}
	st, ok := m.runs[runID]
	if !ok {
		return nil, false
	}
	cp := *st
	cp.Events = append([]testkit.AutoTestEvent(nil), st.Events...)
	return &cp, true
}

func decodeAutotestRequest(data []byte) (testkit.AutoTestRequest, error) {
	var req testkit.AutoTestRequest
	if len(data) > 0 {
		if err := json.Unmarshal(data, &req); err != nil {
			return req, err
		}
	}
	return req, nil
}
