package robot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Teach-and-repeat: the phone jogs the robot and records each action as a Step;
// the program is saved on the edge and replayed (camera/encoder-verified) to run
// a screwdriving sequence. docs/yaver-robot-teach-mode.md.

// Step is one recorded action. Type ∈ home|move|jog|tool|dwell|screw.
type Step struct {
	Type    string   `json:"type"`
	Axis    string   `json:"axis,omitempty"`    // jog
	Dist    float64  `json:"dist,omitempty"`    // jog
	X       *float64 `json:"x,omitempty"`       // move / screw pole
	Y       *float64 `json:"y,omitempty"`       // move / screw pole
	Z       *float64 `json:"z,omitempty"`       // move
	Feed    int      `json:"feed,omitempty"`    // move/jog
	On      *bool    `json:"on,omitempty"`      // tool
	Ms      int      `json:"ms,omitempty"`      // dwell
	Turns   float64  `json:"turns,omitempty"`   // rotate (screwdriver)
	Rpm     int      `json:"rpm,omitempty"`     // rotate
	Ccw     bool     `json:"ccw,omitempty"`     // rotate (reverse)
	Zengage float64  `json:"zEngage,omitempty"` // screw (0 → use calibration)
	Zsafe   float64  `json:"zSafe,omitempty"`   // screw (0 → use calibration)
	Torque  float64  `json:"torque,omitempty"`  // screw seat torque N·mm (0 → use calibration)
	DwellMs int      `json:"dwellMs,omitempty"` // screw torque dwell
	Label   string   `json:"label,omitempty"`   // optional human note
}

// Program is a named, ordered recording.
type Program struct {
	Name      string `json:"name"`
	Steps     []Step `json:"steps"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// StepResult is one step's playback outcome.
type StepResult struct {
	Index int          `json:"index"`
	Step  Step         `json:"step"`
	OK    bool         `json:"ok"`
	Code  string       `json:"code,omitempty"`
	Error string       `json:"error,omitempty"`
	Pos   *Position    `json:"position,omitempty"`
	Cross *CrossCheck  `json:"cross,omitempty"`
	Screw *ScrewResult `json:"screw,omitempty"` // torque/seat result for screw steps
}

// RunResult is the whole playback verdict.
type RunResult struct {
	OK        bool         `json:"ok"`
	Program   string       `json:"program"`
	Completed int          `json:"completed"`
	Total     int          `json:"total"`
	Steps     []StepResult `json:"steps"`
	Error     string       `json:"error,omitempty"`
	TookMs    int64        `json:"tookMs"`
}

var programNameRe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// ProgramStore persists programs as JSON files on the edge.
type ProgramStore struct {
	Dir string
	mu  sync.Mutex
}

func DefaultProgramStore() *ProgramStore {
	home, _ := os.UserHomeDir()
	return &ProgramStore{Dir: filepath.Join(home, ".yaver", "robot-programs")}
}

func (s *ProgramStore) path(name string) string {
	safe := programNameRe.ReplaceAllString(strings.TrimSpace(name), "_")
	return filepath.Join(s.Dir, safe+".json")
}

func (s *ProgramStore) Save(p Program) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("program name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(p.Name), b, 0o644)
}

func (s *ProgramStore) Get(name string) (Program, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		return Program{}, fmt.Errorf("program %q not found", name)
	}
	var p Program
	if err := json.Unmarshal(b, &p); err != nil {
		return Program{}, err
	}
	return p, nil
}

func (s *ProgramStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Remove(s.path(name))
}

// List returns programs (name + step count + timestamps), newest first.
func (s *ProgramStore) List() []Program {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	out := make([]Program, 0, len(entries))
	for _, e := range entries {
		b, err := os.ReadFile(e)
		if err != nil {
			continue
		}
		var p Program
		if json.Unmarshal(b, &p) == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// RunProgram replays a program step-by-step through the Controller, each step
// camera/encoder-verified, halting on the first failure or e-stop. This is the
// "repeat" half of teach-and-repeat.
func (c *Controller) RunProgram(ctx context.Context, p Program, verifyMode string) RunResult {
	start := time.Now()
	res := RunResult{Program: p.Name, Total: len(p.Steps)}
	for i, st := range p.Steps {
		select {
		case <-ctx.Done():
			res.Error = "cancelled"
			res.TookMs = time.Since(start).Milliseconds()
			return res
		default:
		}
		sr := c.runStep(ctx, i, st, verifyMode)
		res.Steps = append(res.Steps, sr)
		if !sr.OK {
			res.Error = fmt.Sprintf("step %d (%s) failed: %s", i, st.Type, sr.Error)
			res.TookMs = time.Since(start).Milliseconds()
			return res
		}
		res.Completed++
	}
	res.OK = true
	res.TookMs = time.Since(start).Milliseconds()
	return res
}

func (c *Controller) runStep(ctx context.Context, i int, st Step, verifyMode string) StepResult {
	sr := StepResult{Index: i, Step: st}
	var mr MoveResponse
	switch st.Type {
	case "home":
		mr = c.Home(ctx, "", verifyMode, st.Label)
	case "move":
		mr = c.Move(ctx, st.X, st.Y, st.Z, st.Feed, verifyMode, st.Label)
	case "jog":
		mr = c.Jog(ctx, st.Axis, st.Dist, st.Feed, verifyMode, st.Label)
	case "tool":
		on := st.On != nil && *st.On
		mr = c.Tool(ctx, on)
	case "rotate":
		mr = c.Rotate(ctx, st.Turns, st.Rpm, st.Ccw, c.EPerTurn)
	case "dwell":
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(st.Ms) * time.Millisecond):
		}
		sr.OK = true
		return sr
	case "screw":
		x := derefOr(st.X, 0)
		y := derefOr(st.Y, 0)
		p := c.screwParamsFromCalibration(x, y, st.Zengage, st.Zsafe, st.Torque)
		p.AtCurrent = st.X == nil && st.Y == nil // no pole given → plunge in place (rail indexer)
		if st.DwellMs > 0 {
			p.DwellMs = st.DwellMs
		}
		scr := c.DriveScrew(ctx, p, verifyMode, st.Label)
		sr.OK = scr.OK
		sr.Code = scr.Code
		sr.Error = scr.Error
		sr.Pos = scr.Position
		sr.Screw = &scr
		return sr
	default:
		sr.Code = "bad_step"
		sr.Error = "unknown step type " + st.Type
		return sr
	}
	sr.OK = mr.OK
	sr.Code = mr.Code
	sr.Error = mr.Error
	sr.Pos = mr.Position
	sr.Cross = mr.Cross
	return sr
}

func derefOr(p *float64, d float64) float64 {
	if p != nil {
		return *p
	}
	return d
}
