package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// AggregatedResponse is the universal shape for every /aggregated/* endpoint.
// Keys are the remote path (e.g. "/errors/groups"); rows is a flat merged,
// timestamp-sorted list; perMachine is the raw by-machine payload for debug.
type AggregatedResponse struct {
	Rows       []map[string]interface{} `json:"rows"`
	PerMachine map[string]interface{}   `json:"perMachine"`
	Errors     map[string]string        `json:"errors,omitempty"`
}

// fanOutFetch calls the same path on every connected machine (local + remote
// via relay) and returns a map of deviceID → parsed JSON.
func fanOutFetch(ctx context.Context, path string, body []byte) (map[string]interface{}, map[string]string) {
	machines := listAllMachines(ctx)
	results := map[string]interface{}{}
	errs := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, m := range machines {
		m := m
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := fetchOneMachine(ctx, m, path, body)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[m.Name] = err.Error()
				return
			}
			var parsed interface{}
			_ = json.Unmarshal(data, &parsed)
			results[m.Name] = parsed
		}()
	}
	wg.Wait()
	return results, errs
}

// fetchOneMachine retrieves `path` from a single machine. For the local one,
// we loopback-fetch (no relay hop); for remotes, we use the Convex-registered
// address. In both cases, forward our Authorization header.
func fetchOneMachine(ctx context.Context, m MachineInfo, path string, body []byte) ([]byte, error) {
	base := localAgentBase()
	if !m.IsLocal && m.QuicHost != "" {
		// For remote agents, hit the LAN/Tailscale address directly if we can.
		base = fmt.Sprintf("http://%s:%d", m.QuicHost, m.QuicPort)
	}
	method := "GET"
	var reader io.Reader
	if len(body) > 0 {
		method = "POST"
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	// Reuse the owner's token. (Server-side; we're inside the auth-ed agent.)
	// Cheap way: re-read the config.
	if cfg, err := LoadConfig(); err == nil && cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 8 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return data, fmt.Errorf("%d %s", res.StatusCode, string(data))
	}
	return data, nil
}

func localAgentBase() string {
	// Default — callers can override via YAVER_LOCAL_BASE.
	return "http://127.0.0.1:18080"
}

// mergeRows pulls a named array from each machine's response and sorts all
// entries descending by ts-like field. Field names are best-effort across
// responses (ts, lastSeen, receivedAt, timestamp).
func mergeRows(perMachine map[string]interface{}, arrayKey string) []map[string]interface{} {
	var rows []map[string]interface{}
	for machine, resp := range perMachine {
		m, ok := resp.(map[string]interface{})
		if !ok {
			continue
		}
		arr, _ := m[arrayKey].([]interface{})
		for _, r := range arr {
			row, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			row["_machine"] = machine
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rowTS(rows[i]) > rowTS(rows[j])
	})
	return rows
}

func rowTS(r map[string]interface{}) string {
	for _, k := range []string{"ts", "receivedAt", "lastSeen", "timestamp", "createdAt", "startedAt"} {
		if v, ok := r[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ---- HTTP handlers ----

// handleAggregatedLogs aggregates /logs/search across every machine.
func (s *HTTPServer) handleAggregatedLogs(w http.ResponseWriter, r *http.Request) {
	path := "/logs/search?" + r.URL.RawQuery
	results, errs := fanOutFetch(r.Context(), path, nil)
	writeJSON(w, http.StatusOK, AggregatedResponse{
		Rows:       mergeRows(results, "hits"),
		PerMachine: results,
		Errors:     errs,
	})
}

// handleAggregatedErrors aggregates /errors/groups.
func (s *HTTPServer) handleAggregatedErrors(w http.ResponseWriter, r *http.Request) {
	path := "/errors/groups?" + r.URL.RawQuery
	results, errs := fanOutFetch(r.Context(), path, nil)
	writeJSON(w, http.StatusOK, AggregatedResponse{
		Rows:       mergeRows(results, "groups"),
		PerMachine: results,
		Errors:     errs,
	})
}

// handleAggregatedAudit aggregates /audit/list.
func (s *HTTPServer) handleAggregatedAudit(w http.ResponseWriter, r *http.Request) {
	path := "/audit/list?" + r.URL.RawQuery
	results, errs := fanOutFetch(r.Context(), path, nil)
	writeJSON(w, http.StatusOK, AggregatedResponse{
		Rows:       mergeRows(results, "entries"),
		PerMachine: results,
		Errors:     errs,
	})
}

// handleAggregatedUptime aggregates /uptime/list.
func (s *HTTPServer) handleAggregatedUptime(w http.ResponseWriter, r *http.Request) {
	path := "/uptime/list?" + r.URL.RawQuery
	results, errs := fanOutFetch(r.Context(), path, nil)
	writeJSON(w, http.StatusOK, AggregatedResponse{
		Rows:       mergeRows(results, "monitors"),
		PerMachine: results,
		Errors:     errs,
	})
}

// handleAggregatedDeploys aggregates /deploy/list per project.
func (s *HTTPServer) handleAggregatedDeploys(w http.ResponseWriter, r *http.Request) {
	path := "/deploy/list?" + r.URL.RawQuery
	results, errs := fanOutFetch(r.Context(), path, nil)
	writeJSON(w, http.StatusOK, AggregatedResponse{
		Rows:       mergeRows(results, "deploys"),
		PerMachine: results,
		Errors:     errs,
	})
}
