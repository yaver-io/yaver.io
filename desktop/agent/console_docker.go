package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// ConsoleContainer is the universal container descriptor returned by the console API.
type ConsoleContainer struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	State   string            `json:"state"` // running, exited, paused, etc.
	Status  string            `json:"status"` // "Up 14 days"
	Ports   []ConsolePort     `json:"ports"`
	Labels  map[string]string `json:"labels,omitempty"`
	Project string            `json:"project,omitempty"` // yaver project hint
	CPUPct  float64           `json:"cpuPct,omitempty"`
	RAMMB   float64           `json:"ramMb,omitempty"`
	CreatedAt string          `json:"createdAt"`
}

type ConsolePort struct {
	Private int    `json:"private"`
	Public  int    `json:"public,omitempty"`
	Type    string `json:"type"`
}

// dockerClient singleton. Opens /var/run/docker.sock by default.
var (
	dockerOnce   sync.Once
	dockerCli    *client.Client
	dockerCliErr error
)

func getDocker() (*client.Client, error) {
	dockerOnce.Do(func() {
		dockerCli, dockerCliErr = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	})
	return dockerCli, dockerCliErr
}

// ListContainers returns every container visible to the local Docker daemon.
func ListContainers(ctx context.Context, includeAll bool) ([]ConsoleContainer, error) {
	cli, err := getDocker()
	if err != nil {
		return nil, err
	}
	list, err := cli.ContainerList(ctx, container.ListOptions{All: includeAll})
	if err != nil {
		return nil, err
	}
	out := make([]ConsoleContainer, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		ports := make([]ConsolePort, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, ConsolePort{Private: int(p.PrivatePort), Public: int(p.PublicPort), Type: p.Type})
		}
		out = append(out, ConsoleContainer{
			ID: c.ID[:12], Name: name, Image: c.Image,
			State: c.State, Status: c.Status, Ports: ports,
			Labels:    c.Labels,
			Project:   c.Labels["com.docker.compose.project"],
			CreatedAt: time.Unix(c.Created, 0).Format(time.RFC3339),
		})
	}
	return out, nil
}

// ContainerAction runs start/stop/restart/pause/unpause/remove against one container.
func ContainerAction(ctx context.Context, id, action string) error {
	cli, err := getDocker()
	if err != nil {
		return err
	}
	switch action {
	case "start":
		return cli.ContainerStart(ctx, id, container.StartOptions{})
	case "stop":
		return cli.ContainerStop(ctx, id, container.StopOptions{})
	case "restart":
		return cli.ContainerRestart(ctx, id, container.StopOptions{})
	case "pause":
		return cli.ContainerPause(ctx, id)
	case "unpause":
		return cli.ContainerUnpause(ctx, id)
	case "remove":
		return cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
	}
	return fmt.Errorf("unknown action %q", action)
}

// ContainerStats returns a single-sample CPU/RAM snapshot for one container.
func ContainerStats(ctx context.Context, id string) (map[string]interface{}, error) {
	cli, err := getDocker()
	if err != nil {
		return nil, err
	}
	stats, err := cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return nil, err
	}
	defer stats.Body.Close()
	data, _ := io.ReadAll(stats.Body)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"cpuPct": calcCPUPct(raw),
		"ramMb":  calcRAMMB(raw),
		"raw":    raw,
	}, nil
}

func calcCPUPct(raw map[string]interface{}) float64 {
	cpu, _ := raw["cpu_stats"].(map[string]interface{})
	pre, _ := raw["precpu_stats"].(map[string]interface{})
	if cpu == nil || pre == nil {
		return 0
	}
	cpuUsage, _ := cpu["cpu_usage"].(map[string]interface{})
	preUsage, _ := pre["cpu_usage"].(map[string]interface{})
	if cpuUsage == nil || preUsage == nil {
		return 0
	}
	tu, _ := cpuUsage["total_usage"].(float64)
	pu, _ := preUsage["total_usage"].(float64)
	sys, _ := cpu["system_cpu_usage"].(float64)
	psys, _ := pre["system_cpu_usage"].(float64)
	if sys-psys <= 0 || tu-pu <= 0 {
		return 0
	}
	cpus, _ := cpu["online_cpus"].(float64)
	if cpus == 0 {
		cpus = 1
	}
	return ((tu - pu) / (sys - psys)) * cpus * 100
}

func calcRAMMB(raw map[string]interface{}) float64 {
	m, _ := raw["memory_stats"].(map[string]interface{})
	if m == nil {
		return 0
	}
	usage, _ := m["usage"].(float64)
	return usage / 1024 / 1024
}

// PullImage pulls an image with progress reporting back to a writer.
func PullImage(ctx context.Context, ref string) (string, error) {
	cli, err := getDocker()
	if err != nil {
		return "", err
	}
	r, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return "", err
	}
	defer r.Close()
	data, _ := io.ReadAll(r)
	return string(data), nil
}

// ListImages returns image summaries.
func ListImages(ctx context.Context) ([]map[string]interface{}, error) {
	cli, err := getDocker()
	if err != nil {
		return nil, err
	}
	imgs, err := cli.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(imgs))
	for _, i := range imgs {
		out = append(out, map[string]interface{}{
			"id":       i.ID[:min(19, len(i.ID))],
			"repoTags": i.RepoTags,
			"size":     i.Size,
			"created":  time.Unix(i.Created, 0).Format(time.RFC3339),
		})
	}
	return out, nil
}

// ListVolumes returns volumes.
func ListVolumes(ctx context.Context) (interface{}, error) {
	cli, err := getDocker()
	if err != nil {
		return nil, err
	}
	list, err := cli.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs()})
	if err != nil {
		return nil, err
	}
	return list, nil
}

// PruneUnused runs image + container + volume prune.
func PruneUnused(ctx context.Context) (map[string]interface{}, error) {
	cli, err := getDocker()
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if r, err := cli.ImagesPrune(ctx, filters.NewArgs()); err == nil {
		out["images"] = map[string]interface{}{"reclaimed": r.SpaceReclaimed, "deleted": len(r.ImagesDeleted)}
	}
	if r, err := cli.ContainersPrune(ctx, filters.NewArgs()); err == nil {
		out["containers"] = map[string]interface{}{"reclaimed": r.SpaceReclaimed, "deleted": len(r.ContainersDeleted)}
	}
	if r, err := cli.VolumesPrune(ctx, filters.NewArgs()); err == nil {
		out["volumes"] = map[string]interface{}{"reclaimed": r.SpaceReclaimed, "deleted": len(r.VolumesDeleted)}
	}
	return out, nil
}

// ---- HTTP ----

func (s *HTTPServer) handleConsoleContainers(w http.ResponseWriter, r *http.Request) {
	includeAll := r.URL.Query().Get("all") == "1"
	list, err := ListContainers(r.Context(), includeAll)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"containers": list})
}

func (s *HTTPServer) handleConsoleContainerAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := ContainerAction(r.Context(), b.ID, b.Action); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleConsoleContainerStats(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}
	stats, err := ContainerStats(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *HTTPServer) handleConsoleImages(w http.ResponseWriter, r *http.Request) {
	list, err := ListImages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"images": list})
}

func (s *HTTPServer) handleConsoleVolumes(w http.ResponseWriter, r *http.Request) {
	list, err := ListVolumes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *HTTPServer) handleConsolePrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	res, err := PruneUnused(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
