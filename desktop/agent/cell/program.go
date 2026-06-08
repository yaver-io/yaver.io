package cell

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

// FeedSpec is the cut/strip job for one lead, sourced from a Talos kesimRows row
// (lengthMm + outer/inner strip + qty). The actual param write to the feed
// machine is done by the ops layer through the feed station's machine.Driver
// before the feed station is served.
type FeedSpec struct {
	OuterStripMm float64 `json:"outerStripMm,omitempty"`
	InnerStripMm float64 `json:"innerStripMm,omitempty"`
	Qty          int     `json:"qty,omitempty"`
}

// EndStep names a station to serve for one lead end, in order. Per-step overrides
// can be added later (e.g. screw torque, force limit); for now the station's own
// config carries them.
type EndStep struct {
	StationID string `json:"stationId"`
	Note      string `json:"note,omitempty"`
}

// Lead is one cut wire and its per-end operation sequence. EndA is processed,
// then the arm regrips (Route names the regrip fixture, design §10), then EndB.
type Lead struct {
	Color    string    `json:"color,omitempty"` // supports multi-color "Sarı/Yeşil"
	LengthMm float64   `json:"lengthMm,omitempty"`
	Cores    int       `json:"cores,omitempty"`
	Feed     FeedSpec  `json:"feed,omitempty"`
	EndA     []EndStep `json:"endA,omitempty"`
	EndB     []EndStep `json:"endB,omitempty"`
	Route    string    `json:"route,omitempty"` // regrip fixture name (informational for now)
}

// CellProgram is the taught artifact for one SKU: the per-lead station sequences
// plus the positionMap (which color lands in which connector cavity) that a
// source drawing often omits — captured by demonstration (design §7).
type CellProgram struct {
	SKU         string            `json:"sku"`
	StationMap  string            `json:"stationMap,omitempty"`
	Leads       []Lead            `json:"leads,omitempty"`
	PositionMap map[string]string `json:"positionMap,omitempty"` // cavity/position -> color
	CreatedAt   int64             `json:"createdAt,omitempty"`
	UpdatedAt   int64             `json:"updatedAt,omitempty"`
}

// LeadResult / RunResult report a replay.
type LeadResult struct {
	Index    int             `json:"index"`
	Color    string          `json:"color,omitempty"`
	OK       bool            `json:"ok"`
	Stations []StationResult `json:"stations,omitempty"`
}

type RunResult struct {
	SKU       string       `json:"sku"`
	OK        bool         `json:"ok"`
	DryRun    bool         `json:"dryRun,omitempty"`
	Completed int          `json:"completed"` // leads fully completed
	Total     int          `json:"total"`
	Error     string       `json:"error,omitempty"`
	Leads     []LeadResult `json:"leads,omitempty"`
	TookMs    int64        `json:"tookMs"`
}

// ValidateSequence checks a program's per-end station order against each
// station's SeqConstraints (design §9). Returns the list of violations (empty =
// valid). stations maps station id -> Station.
func ValidateSequence(prog CellProgram, stations map[string]Station) []string {
	var problems []string
	check := func(leadIdx int, end string, steps []EndStep) {
		kinds := make([]StationKind, len(steps))
		for i, s := range steps {
			st, ok := stations[s.StationID]
			if !ok {
				problems = append(problems, fmt.Sprintf("lead %d end %s: unknown station %q", leadIdx, end, s.StationID))
				continue
			}
			kinds[i] = st.Kind
		}
		for i, s := range steps {
			st, ok := stations[s.StationID]
			if !ok {
				continue
			}
			for _, mf := range st.Constraints.MustFollow {
				if !seenBefore(kinds, i, mf) {
					problems = append(problems, fmt.Sprintf("lead %d end %s: %s must follow %s", leadIdx, end, st.Kind, mf))
				}
			}
			for _, mp := range st.Constraints.MustPrecede {
				if !seenAfter(kinds, i, mp) {
					problems = append(problems, fmt.Sprintf("lead %d end %s: %s must precede %s", leadIdx, end, st.Kind, mp))
				}
			}
		}
	}
	for li, lead := range prog.Leads {
		check(li, "A", lead.EndA)
		check(li, "B", lead.EndB)
	}
	return problems
}

func seenBefore(kinds []StationKind, idx int, want StationKind) bool {
	for i := 0; i < idx; i++ {
		if kinds[i] == want {
			return true
		}
	}
	return false
}

func seenAfter(kinds []StationKind, idx int, want StationKind) bool {
	for i := idx + 1; i < len(kinds); i++ {
		if kinds[i] == want {
			return true
		}
	}
	return false
}

// Run replays a cell program: for each lead, serve EndA stations, then EndB
// stations, in order. Stops the current lead on the first station failure and
// records it; other leads continue. The feed (length/strip param-set + cut) is
// the caller's responsibility before Run (it owns the machine.Driver write); Run
// orchestrates the station ring. dryRun skips arm motion at the ops layer (here
// it is recorded for the report).
func (o *Orchestrator) Run(ctx context.Context, prog CellProgram, stations map[string]Station, dryRun bool) RunResult {
	start := time.Now()
	res := RunResult{SKU: prog.SKU, DryRun: dryRun, Total: len(prog.Leads)}

	serveEnd := func(lr *LeadResult, steps []EndStep) bool {
		for _, step := range steps {
			st, ok := stations[step.StationID]
			if !ok {
				lr.Stations = append(lr.Stations, StationResult{StationID: step.StationID, OK: false, Code: "unknown_station", Error: "station not in station map"})
				return false
			}
			sr := o.ServeStation(ctx, st)
			lr.Stations = append(lr.Stations, sr)
			if !sr.OK {
				return false
			}
		}
		return true
	}

	for li, lead := range prog.Leads {
		lr := LeadResult{Index: li, Color: lead.Color}
		okA := serveEnd(&lr, lead.EndA)
		okB := false
		if okA {
			okB = serveEnd(&lr, lead.EndB)
		}
		lr.OK = okA && okB
		if lr.OK {
			res.Completed++
		}
		res.Leads = append(res.Leads, lr)
	}
	res.OK = res.Completed == res.Total
	res.TookMs = time.Since(start).Milliseconds()
	return res
}

// --- file-backed store (local-first, mirrors arm.ProgramStore) ---

type ProgramStore struct{ dir string }

func DefaultProgramStore() *ProgramStore {
	home, _ := os.UserHomeDir()
	return &ProgramStore{dir: filepath.Join(home, ".yaver", "cell-programs")}
}

func (s *ProgramStore) path(sku string) string { return filepath.Join(s.dir, safeName(sku)+".json") }

func (s *ProgramStore) Save(p CellProgram) error {
	if strings.TrimSpace(p.SKU) == "" {
		return fmt.Errorf("cell program sku required")
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
	return os.WriteFile(s.path(p.SKU), b, 0o600)
}

func (s *ProgramStore) Get(sku string) (CellProgram, error) {
	var p CellProgram
	b, err := os.ReadFile(s.path(sku))
	if err != nil {
		return p, fmt.Errorf("cell program %q not found", sku)
	}
	return p, json.Unmarshal(b, &p)
}

func (s *ProgramStore) Delete(sku string) error { return os.Remove(s.path(sku)) }

func (s *ProgramStore) List() []CellProgram {
	out := []CellProgram{}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(s.dir, e.Name())); err == nil {
			var p CellProgram
			if json.Unmarshal(b, &p) == nil {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
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
