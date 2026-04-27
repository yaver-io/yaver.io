package main

import (
	"net/http"
	"os"
	"path/filepath"
)

// CostEstimate is a per-target monthly cost estimate for the user's current
// project footprint.
type CostEstimate struct {
	Target       string  `json:"target"`
	Label        string  `json:"label"`
	Monthly      float64 `json:"monthly"` // USD
	FreeTierOK   bool    `json:"freeTierOk"`
	Notes        string  `json:"notes,omitempty"`
	Tier         string  `json:"tier"` // "free", "starter", "pro"
}

// ProjectUsage captures the measurements we need to estimate cost.
type ProjectUsage struct {
	DBSizeMB     float64 `json:"dbSizeMb"`
	StorageMB    float64 `json:"storageMb"`
	RowsApprox   int64   `json:"rowsApprox"`
	HasCompute   bool    `json:"hasCompute"` // project needs app hosting, not just DB
}

// estimateUsage looks at the project directory to guess DB + storage size.
// Best-effort; callers can override.
func estimateUsage(projectDir string) ProjectUsage {
	var u ProjectUsage
	// SQLite file size
	for _, name := range []string{"local.db", "dev.db", "database.db"} {
		if info, err := os.Stat(filepath.Join(projectDir, name)); err == nil {
			u.DBSizeMB = float64(info.Size()) / 1024 / 1024
			break
		}
	}
	// Convex storage size
	if info, err := os.Stat(filepath.Join(projectDir, "convex_local_backend.sqlite3")); err == nil {
		u.DBSizeMB = float64(info.Size()) / 1024 / 1024
	}
	// Local filesystem storage
	if info, err := os.Stat(filepath.Join(projectDir, "uploads")); err == nil && info.IsDir() {
		u.StorageMB = dirSizeMB(filepath.Join(projectDir, "uploads"))
	}
	// Assume apps with a package.json need compute hosting.
	if _, err := os.Stat(filepath.Join(projectDir, "package.json")); err == nil {
		u.HasCompute = true
	}
	return u
}

func dirSizeMB(path string) float64 {
	var bytes int64
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			bytes += info.Size()
		}
		return nil
	})
	return float64(bytes) / 1024 / 1024
}

// CompareCosts returns estimates for every switch target.
func CompareCosts(projectDir string) map[string]interface{} {
	u := estimateUsage(projectDir)
	var out []CostEstimate
	for _, t := range SwitchTargets() {
		out = append(out, estimateTargetCost(t, u))
	}
	return map[string]interface{}{"usage": u, "estimates": out}
}

func estimateTargetCost(t SwitchTarget, u ProjectUsage) CostEstimate {
	est := CostEstimate{Target: t.ID, Label: t.Label}
	switch t.Host {
	case HostLocalDocker, HostLocalProcess:
		est.Monthly = 0
		est.Tier = "free"
		est.FreeTierOK = true
		est.Notes = "Runs on your machine"
	case HostConvexCloud:
		est.Tier = "free"
		if u.DBSizeMB < 500 && u.StorageMB < 1000 {
			est.FreeTierOK = true
			est.Monthly = 0
		} else {
			est.Monthly = 25
			est.Tier = "pro"
		}
	case HostSupabaseCloud:
		est.Tier = "free"
		if u.DBSizeMB < 500 && u.StorageMB < 1000 {
			est.FreeTierOK = true
		} else {
			est.Monthly = 25
			est.Tier = "pro"
		}
	case HostYaverCloud:
		est.Monthly = 9
		est.Tier = "managed"
	case HostVercel:
		est.Tier = "free"
		est.FreeTierOK = true
		est.Notes = "Hobby tier; Pro is $20/seat for commercial use"
	case HostCFWorkers:
		est.Tier = "free"
		est.FreeTierOK = true
	}
	return est
}

// ---- MCP / HTTP ----

func mcpSwitchCost(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return CompareCosts(dir)
}

func (s *HTTPServer) handleSwitchCost(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpSwitchCost(s.dirParam(r)))
}

