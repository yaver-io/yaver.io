package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (s *HTTPServer) handleOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"operations": GlobalOperationStore().List(OperationFilter{
			Kind:        q.Get("kind"),
			Status:      q.Get("status"),
			DeviceID:    q.Get("device"),
			ProjectPath: q.Get("projectPath"),
			Limit:       limit,
		}),
	})
}

func (s *HTTPServer) handleOperationsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, snapshot, cancel := GlobalOperationStore().Subscribe()
	defer cancel()

	for _, op := range snapshot {
		if !operationMatchesStreamFilter(op, r) {
			continue
		}
		if data, err := json.Marshal(op); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case op, ok := <-ch:
			if !ok {
				return
			}
			if !operationMatchesStreamFilter(op, r) {
				continue
			}
			if data, err := json.Marshal(op); err == nil {
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}

func operationMatchesStreamFilter(op OperationState, r *http.Request) bool {
	q := r.URL.Query()
	if q.Get("kind") != "" && op.Kind != q.Get("kind") {
		return false
	}
	if q.Get("status") != "" && op.Status != q.Get("status") {
		return false
	}
	if q.Get("device") != "" && op.DeviceID != q.Get("device") {
		return false
	}
	if q.Get("projectPath") != "" && op.ProjectPath != q.Get("projectPath") {
		return false
	}
	return true
}
