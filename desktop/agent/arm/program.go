package arm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Waypoint is one taught pose in a program — either a joint target (MoveJ) or a
// Cartesian pose (MoveL). Captured by hand-guiding the arm (free-drive) and
// reading its state, then replayed.
type Waypoint struct {
	Joints  map[string]float64 `json:"joints,omitempty"`
	Pose    *Pose              `json:"pose,omitempty"`
	VelPct  int                `json:"velPct,omitempty"`
	AccPct  int                `json:"accPct,omitempty"`
	DwellMs int                `json:"dwellMs,omitempty"`
	Verify  string             `json:"verify,omitempty"`      // "" | off | frames | agent
	Expect  string             `json:"expectation,omitempty"` // for camera verify
	Label   string             `json:"label,omitempty"`
}

// Program is a taught sequence — the "guide & repeat" unit, robot-agnostic.
type Program struct {
	Name      string     `json:"name"`
	Waypoints []Waypoint `json:"waypoints"`
	CreatedAt int64      `json:"createdAt,omitempty"`
	UpdatedAt int64      `json:"updatedAt,omitempty"`
}

// RunResult reports a replay.
type RunResult struct {
	OK        bool         `json:"ok"`
	Program   string       `json:"program"`
	Completed int          `json:"completed"`
	Total     int          `json:"total"`
	Error     string       `json:"error,omitempty"`
	Steps     []StepResult `json:"steps,omitempty"`
	TookMs    int64        `json:"tookMs"`
}

type StepResult struct {
	Index  int      `json:"index"`
	OK     bool     `json:"ok"`
	Code   string   `json:"code,omitempty"`
	Error  string   `json:"error,omitempty"`
	Verify *Verdict `json:"verify,omitempty"`
}

// --- file-backed store (local-first, mirrors robot.DefaultProgramStore) ---

type ProgramStore struct{ dir string }

func DefaultProgramStore() *ProgramStore {
	home, _ := os.UserHomeDir()
	return &ProgramStore{dir: filepath.Join(home, ".yaver", "arm-programs")}
}

func (s *ProgramStore) path(name string) string {
	return filepath.Join(s.dir, safeName(name)+".json")
}

func (s *ProgramStore) Save(p Program) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("program name required")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(s.path(p.Name), b, 0o600)
}

func (s *ProgramStore) Get(name string) (Program, error) {
	var p Program
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		return p, fmt.Errorf("program %q not found", name)
	}
	return p, json.Unmarshal(b, &p)
}

func (s *ProgramStore) Delete(name string) error { return os.Remove(s.path(name)) }

func (s *ProgramStore) List() []Program {
	out := []Program{}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(s.dir, e.Name())); err == nil {
			var p Program
			if json.Unmarshal(b, &p) == nil {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func safeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "program"
	}
	return b.String()
}

// --- controller: free-drive + capture + replay ---

// FreeDrive toggles hand-guiding (learning mode). Reading state while in
// free-drive + Capture() is the "teach" half of teach-and-repeat.
func (c *Controller) FreeDrive(ctx context.Context, on bool) MoveResult {
	if err := c.Backend.FreeDrive(ctx, on); err != nil {
		code := "backend"
		if err == ErrNoFreeDrive {
			code = "no_freedrive"
		}
		return MoveResult{OK: false, Code: code, Kind: "freedrive", Error: err.Error()}
	}
	return MoveResult{OK: true, Kind: "freedrive"}
}

// Capture reads the current joint state (and pose, if available) as a Waypoint —
// call it while hand-guiding to teach a point.
func (c *Controller) Capture(ctx context.Context, velPct, accPct, dwellMs int, label string) (Waypoint, error) {
	js, err := c.Backend.JointState(ctx)
	if err != nil {
		return Waypoint{}, err
	}
	wp := Waypoint{Joints: map[string]float64{}, VelPct: velPct, AccPct: accPct, DwellMs: dwellMs, Label: label}
	for _, j := range js {
		wp.Joints[j.Name] = j.Position
	}
	if p, perr := c.Backend.Pose(ctx); perr == nil {
		wp.Pose = &p
	}
	return wp, nil
}

// RunProgram replays a taught program, each waypoint soft-limit checked and
// camera-verified (the "repeat" half). Stops on the first hard failure.
func (c *Controller) RunProgram(ctx context.Context, prog Program, verify string) RunResult {
	start := time.Now()
	res := RunResult{Program: prog.Name, Total: len(prog.Waypoints)}
	for i, wp := range prog.Waypoints {
		v := wp.Verify
		if verify != "" {
			v = verify
		}
		var mr MoveResult
		switch {
		case len(wp.Joints) > 0:
			mr = c.MoveJoints(ctx, wp.Joints, wp.VelPct, wp.AccPct, v, wp.Expect)
		case wp.Pose != nil:
			mr = c.MovePose(ctx, *wp.Pose, wp.VelPct, wp.AccPct, v, wp.Expect)
		default:
			mr = MoveResult{OK: false, Code: "bad_waypoint", Error: "waypoint has neither joints nor pose"}
		}
		res.Steps = append(res.Steps, StepResult{Index: i, OK: mr.OK, Code: mr.Code, Error: mr.Error, Verify: mr.Verify})
		if !mr.OK {
			res.Error = fmt.Sprintf("waypoint %d failed: %s", i, mr.Error)
			res.TookMs = time.Since(start).Milliseconds()
			return res
		}
		res.Completed++
		if wp.DwellMs > 0 {
			select {
			case <-ctx.Done():
				res.Error = ctx.Err().Error()
				res.TookMs = time.Since(start).Milliseconds()
				return res
			case <-time.After(time.Duration(wp.DwellMs) * time.Millisecond):
			}
		}
	}
	res.OK = true
	res.TookMs = time.Since(start).Milliseconds()
	return res
}
