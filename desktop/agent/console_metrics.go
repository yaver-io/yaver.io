package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	netps "github.com/shirou/gopsutil/v3/net"
)

// HostMetrics is a single-tick snapshot of the host's vitals.
type HostMetrics struct {
	Timestamp string  `json:"ts"`
	CPUPct    float64 `json:"cpuPct"`
	RAMUsed   uint64  `json:"ramUsed"`
	RAMTotal  uint64  `json:"ramTotal"`
	RAMPct    float64 `json:"ramPct"`
	DiskUsed  uint64  `json:"diskUsed"`
	DiskTotal uint64  `json:"diskTotal"`
	DiskPct   float64 `json:"diskPct"`
	NetRxBps  uint64  `json:"netRxBps"`
	NetTxBps  uint64  `json:"netTxBps"`
	Uptime    uint64  `json:"uptime"`
	Hostname  string  `json:"hostname"`
	OS        string  `json:"os"`
	Cores     int     `json:"cores"`
}

// sampleHostMetrics returns a single metric snapshot.
func sampleHostMetrics(ctx context.Context, lastNet map[string]uint64) (*HostMetrics, map[string]uint64) {
	m := &HostMetrics{Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	if c, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(c) > 0 {
		m.CPUPct = c[0]
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.RAMUsed = v.Used
		m.RAMTotal = v.Total
		m.RAMPct = v.UsedPercent
	}
	if d, err := disk.UsageWithContext(ctx, "/"); err == nil {
		m.DiskUsed = d.Used
		m.DiskTotal = d.Total
		m.DiskPct = d.UsedPercent
	}
	if h, err := host.InfoWithContext(ctx); err == nil {
		m.Hostname = h.Hostname
		m.OS = h.Platform + " " + h.PlatformVersion
		m.Uptime = h.Uptime
	}
	if cores, err := cpu.CountsWithContext(ctx, true); err == nil {
		m.Cores = cores
	}
	// Network delta from last sample.
	nextNet := map[string]uint64{}
	if nets, err := netps.IOCountersWithContext(ctx, false); err == nil && len(nets) > 0 {
		rx := nets[0].BytesRecv
		tx := nets[0].BytesSent
		nextNet["rx"] = rx
		nextNet["tx"] = tx
		if lastNet != nil {
			if r0, ok := lastNet["rx"]; ok && rx > r0 {
				m.NetRxBps = rx - r0
			}
			if t0, ok := lastNet["tx"]; ok && tx > t0 {
				m.NetTxBps = tx - t0
			}
		}
	}
	return m, nextNet
}

// Metrics snapshot HTTP handler.
func (s *HTTPServer) handleMetricsSnapshot(w http.ResponseWriter, r *http.Request) {
	m, _ := sampleHostMetrics(r.Context(), nil)
	writeJSON(w, http.StatusOK, m)
}

// ---- WebSocket ----

var wsUpgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

// handleMetricsStream: WS /ws/metrics — emits HostMetrics every 2 seconds.
func (s *HTTPServer) handleMetricsStream(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	var last map[string]uint64
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sample, next := sampleHostMetrics(r.Context(), last)
			last = next
			data, _ := json.Marshal(sample)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}

// handleContainerLogsStream: WS /ws/logs/{id} — streams docker logs -f.
func (s *HTTPServer) handleContainerLogsStream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "id query param required")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	cli, err := getDocker()
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("docker unavailable: "+err.Error()))
		return
	}
	reader, err := cli.ContainerLogs(r.Context(), id, containerLogsOpts())
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("log open failed: "+err.Error()))
		return
	}
	defer reader.Close()
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if werr := conn.WriteMessage(websocket.TextMessage, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
