package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// handleLogsStream is an SSE endpoint that tails a specific Docker-Compose
// service's logs. Subscribers receive one `data:` line per log entry.
//
//   GET /logs/stream?service=postgres&tail=50
//
// Piggybacks on `docker compose logs -f` so whatever stdout/stderr the service
// writes flows through.
func (s *HTTPServer) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service == "" {
		jsonError(w, http.StatusBadRequest, "service param required")
		return
	}
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "50"
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	dir := s.dirParam(r)
	sm := NewServicesManager(dir)

	// Prefer docker compose logs -f. For binary services we tail the .yaver/logs file.
	cfg, _ := sm.LoadConfig()
	if cfg != nil {
		if svc, ok := cfg.Services[service]; ok && svc.Binary != "" {
			streamBinaryLog(w, flusher, r, dir, service)
			return
		}
	}
	streamDockerLogs(w, flusher, r, sm, service, tail)
}

func streamDockerLogs(w http.ResponseWriter, flusher http.Flusher, r *http.Request, sm *ServicesManager, service, tail string) {
	cmd := exec.CommandContext(r.Context(), "docker", "compose",
		"-p", "yaver-services", "-f", sm.composePath(),
		"logs", "-f", "--tail", tail, "--no-color", service,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
		if r.Context().Err() != nil {
			return
		}
	}
}

func streamBinaryLog(w http.ResponseWriter, flusher http.Flusher, r *http.Request, dir, service string) {
	// Poll the log file every 500ms. Not perfect but dependency-free.
	sm := NewServicesManager(dir)
	seen := int64(0)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			data, err := sm.Logs(service, 500)
			if err != nil {
				continue
			}
			if int64(len(data)) <= seen {
				continue
			}
			fresh := data[seen:]
			for _, line := range strings.Split(strings.TrimSpace(string(fresh)), "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
			seen = int64(len(data))
		}
	}
}

