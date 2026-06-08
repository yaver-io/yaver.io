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

	"github.com/yaver-io/agent/arm"
)

// job.go — the GENERIC data-driven harness-cell engine. A Job is a wire-list (a
// table of WireRows) plus the cell's lanes/routes; running it executes the four
// stages — PREP (per-lane cut/strip/crimp/heatshrink) → ROUTE (lay the lead into
// the board) → TERMINATE (push-in seated by the arm, or flagged for an operator)
// → BUNDLE+TEST. The engine is domain-agnostic: it carries the SHAPE of a
// wire-list, never a specific harness's rows, board, or economics — those live
// with the operator (local) or in the private knowledge plane.

// WireFormat is how a lead end is terminated/prepared.
type WireFormat string

const (
	FormatFerruleSingle WireFormat = "ferrule_single"
	FormatFerruleTwin   WireFormat = "ferrule_twin" // two wires in one ferrule
	FormatBare          WireFormat = "bare"
	FormatPin           WireFormat = "pin"
)

// PoleType is the destination terminal style.
type PoleType string

const (
	PolePushIn PoleType = "push_in" // spring-cage — robot can seat a ferrule axially
	PoleScrew  PoleType = "screw"   // screw clamp — needs a driver end-effector
)

// WireRow is one row of the wire-list (the connection-table SCHEMA). It is the
// engine's generic shape; the actual rows are supplied per job by the operator.
type WireRow struct {
	ID           string     `json:"id"`
	Color        string     `json:"color,omitempty"`
	GaugeMm2     float64    `json:"gaugeMm2,omitempty"`
	LengthMm     float64    `json:"lengthMm,omitempty"`
	EndA         WireFormat `json:"endA,omitempty"`
	EndB         WireFormat `json:"endB,omitempty"`
	FerruleType  string     `json:"ferruleType,omitempty"`
	Mark         string     `json:"mark,omitempty"`
	SrcLane      int        `json:"srcLane,omitempty"`
	DstConnector string     `json:"dstConnector,omitempty"`
	DstPole      string     `json:"dstPole,omitempty"`
	PoleType     PoleType   `json:"poleType,omitempty"`
	RouteID      string     `json:"routeId,omitempty"`
	TiePoints    []string   `json:"tiePoints,omitempty"`
	TwinPartner  string     `json:"twinPartner,omitempty"` // wire id sharing a twin ferrule (blank = single)
}

// AutoTerminable reports whether the arm can terminate this wire end unattended:
// a single ferrule into a push-in (spring-cage) pole. Screw poles and twin
// ferrules are flagged for an operator (the documented automation boundary).
func (w WireRow) AutoTerminable() bool {
	return w.PoleType == PolePushIn && w.EndA == FormatFerruleSingle && w.TwinPartner == ""
}

// Lane is one prep lane: an ordered list of prep station ids (cut/strip, ferrule
// crimp, heatshrink apply/heat) and the nest pose the arm picks the finished lead
// from. A lane is typically fixed to one color/gauge so no recalibration is
// needed between wires of that lane.
type Lane struct {
	Index        int           `json:"index"`
	Label        string        `json:"label,omitempty"`
	PrepStations []string      `json:"prepStations,omitempty"` // ordered station ids
	Nest         *arm.Waypoint `json:"nest,omitempty"`         // pick pose for the finished lead
}

// RoutePath is a taught dress path: the waypoints the arm follows to lay a lead
// into the board's combs and present the end near its destination pole.
type RoutePath struct {
	ID        string         `json:"id"`
	Waypoints []arm.Waypoint `json:"waypoints,omitempty"`
}

// Job is a full data-driven run: the wire-list + the cell's lanes/routes + the
// bundle/test station ids.
type Job struct {
	ID          string      `json:"id"`
	Wires       []WireRow   `json:"wires,omitempty"`
	Lanes       []Lane      `json:"lanes,omitempty"`
	Routes      []RoutePath `json:"routes,omitempty"`
	TieStation  string      `json:"tieStation,omitempty"`  // station id of the auto tie-gun
	TestStation string      `json:"testStation,omitempty"` // station id of the continuity/pull rig
	CreatedAt   int64       `json:"createdAt,omitempty"`
	UpdatedAt   int64       `json:"updatedAt,omitempty"`
}

// JobOpts tunes a run (push-in seating params + dry-run).
type JobOpts struct {
	DryRun       bool       `json:"dryRun,omitempty"`
	InsertDir    arm.Axis6  `json:"insertDir,omitempty"`    // default "z"
	InsertForceN float64    `json:"insertForceN,omitempty"` // default 25
	InsertMaxMm  float64    `json:"insertMaxMm,omitempty"`  // default 15
}

func (o *JobOpts) normalize() {
	if strings.TrimSpace(string(o.InsertDir)) == "" {
		o.InsertDir = "z"
	}
	if o.InsertForceN <= 0 {
		o.InsertForceN = 25
	}
	if o.InsertMaxMm <= 0 {
		o.InsertMaxMm = 15
	}
}

// WireResult / JobResult report a run, wire by wire.
type WireResult struct {
	WireID   string          `json:"wireId"`
	OK       bool            `json:"ok"`
	Stage    string          `json:"stage,omitempty"` // prep|route|terminate|test
	Mode     string          `json:"mode,omitempty"`  // auto|operator
	Code     string          `json:"code,omitempty"`
	Error    string          `json:"error,omitempty"`
	Stations []StationResult `json:"stations,omitempty"`
}

type JobResult struct {
	JobID         string       `json:"jobId"`
	OK            bool         `json:"ok"`
	DryRun        bool         `json:"dryRun,omitempty"`
	Wires         []WireResult `json:"wires,omitempty"`
	OperatorFlags []string     `json:"operatorFlags,omitempty"` // wire ids needing a manual touch (screw / twin)
	Completed     int          `json:"completed"`
	Total         int          `json:"total"`
	TookMs        int64        `json:"tookMs"`
}

// ValidateJob checks structural integrity before a run: lanes/routes/stations
// referenced by the wire-list must exist. Returns the list of problems.
func ValidateJob(job Job, stations map[string]Station) []string {
	var probs []string
	lanes := map[int]Lane{}
	for _, l := range job.Lanes {
		lanes[l.Index] = l
	}
	routes := map[string]bool{}
	for _, r := range job.Routes {
		routes[r.ID] = true
	}
	seen := map[string]bool{}
	for _, w := range job.Wires {
		if w.ID == "" || seen[w.ID] {
			probs = append(probs, fmt.Sprintf("wire %q: missing or duplicate id", w.ID))
		}
		seen[w.ID] = true
		lane, ok := lanes[w.SrcLane]
		if !ok {
			probs = append(probs, fmt.Sprintf("wire %s: srcLane %d not defined", w.ID, w.SrcLane))
		} else {
			for _, stid := range lane.PrepStations {
				if _, ok := stations[stid]; !ok {
					probs = append(probs, fmt.Sprintf("lane %d: prep station %q not registered", w.SrcLane, stid))
				}
			}
		}
		if w.RouteID != "" && !routes[w.RouteID] {
			probs = append(probs, fmt.Sprintf("wire %s: route %q not defined", w.ID, w.RouteID))
		}
		if w.TwinPartner != "" && !seen[w.TwinPartner] {
			// partner may appear later; only a soft note if it never appears — checked below
		}
	}
	for _, w := range job.Wires {
		if w.TwinPartner != "" && !seen[w.TwinPartner] {
			probs = append(probs, fmt.Sprintf("wire %s: twinPartner %q not in wire-list", w.ID, w.TwinPartner))
		}
	}
	if job.TieStation != "" {
		if _, ok := stations[job.TieStation]; !ok {
			probs = append(probs, fmt.Sprintf("tieStation %q not registered", job.TieStation))
		}
	}
	if job.TestStation != "" {
		if _, ok := stations[job.TestStation]; !ok {
			probs = append(probs, fmt.Sprintf("testStation %q not registered", job.TestStation))
		}
	}
	return probs
}

// RunJob executes the four-stage data-driven sequence. A station failure flags
// that wire and continues (never hard-stops the cell for one fault, per the
// fault-handling contract); screw + twin-ferrule wires are flagged for the
// operator, not treated as faults.
func (o *Orchestrator) RunJob(ctx context.Context, job Job, stations map[string]Station, opts JobOpts) JobResult {
	start := time.Now()
	opts.normalize()
	res := JobResult{JobID: job.ID, DryRun: opts.DryRun, Total: len(job.Wires)}

	lanes := map[int]Lane{}
	for _, l := range job.Lanes {
		lanes[l.Index] = l
	}
	routes := map[string]RoutePath{}
	for _, r := range job.Routes {
		routes[r.ID] = r
	}

	// per-wire result, kept addressable across stages.
	wr := make([]*WireResult, len(job.Wires))
	for i, w := range job.Wires {
		wr[i] = &WireResult{WireID: w.ID, OK: true, Mode: "auto"}
	}
	alive := func(i int) bool { return wr[i].OK }
	failWire := func(i int, stage, code, msg string) {
		wr[i].OK, wr[i].Stage, wr[i].Code, wr[i].Error = false, stage, code, msg
	}

	// ---- STAGE 1: PREP (per-lane stations) ----
	for i, w := range job.Wires {
		lane, ok := lanes[w.SrcLane]
		if !ok {
			failWire(i, "prep", "no_lane", fmt.Sprintf("srcLane %d not defined", w.SrcLane))
			continue
		}
		for _, stid := range lane.PrepStations {
			st, ok := stations[stid]
			if !ok {
				failWire(i, "prep", "unknown_station", "prep station "+stid+" not registered")
				break
			}
			if opts.DryRun {
				wr[i].Stations = append(wr[i].Stations, StationResult{StationID: stid, Kind: st.Kind, OK: true, Phase: PhaseWithdraw})
				continue
			}
			sr := o.ServeStation(ctx, st)
			wr[i].Stations = append(wr[i].Stations, sr)
			if !sr.OK {
				failWire(i, "prep", sr.Code, sr.Error)
				break
			}
		}
	}

	// ---- STAGE 2: ROUTE (pick from nest, lay along the board) ----
	for i, w := range job.Wires {
		if !alive(i) {
			continue
		}
		lane := lanes[w.SrcLane]
		if !opts.DryRun {
			if lane.Nest != nil {
				if out := o.Arm.MoveWaypoint(ctx, *lane.Nest); !out.OK {
					failWire(i, "route", routeCode(out), nonEmpty(out.Error, "pick from nest failed"))
					continue
				}
			}
			if rp, ok := routes[w.RouteID]; ok {
				broke := false
				for _, wp := range rp.Waypoints {
					out := o.Arm.MoveWaypoint(ctx, wp)
					if !out.OK {
						failWire(i, "route", routeCode(out), nonEmpty(out.Error, "route move failed"))
						broke = true
						break
					}
				}
				if broke {
					continue
				}
			}
		}
		wr[i].Stage = "route"
	}

	// ---- STAGE 3: TERMINATE (auto push-in, or flag for operator) ----
	for i, w := range job.Wires {
		if !alive(i) {
			continue
		}
		if w.AutoTerminable() {
			if !opts.DryRun {
				out := o.Arm.ForceInsert(ctx, opts.InsertDir, opts.InsertForceN, opts.InsertMaxMm)
				if !out.OK {
					failWire(i, "terminate", nonEmpty(out.Code, "insert_failed"), nonEmpty(out.Error, "push-in did not seat"))
					continue
				}
			}
			wr[i].Stage, wr[i].Mode = "terminate", "auto"
		} else {
			// screw or twin-ferrule → operator station (NOT a fault).
			wr[i].Stage, wr[i].Mode = "terminate", "operator"
			res.OperatorFlags = append(res.OperatorFlags, w.ID)
		}
	}

	// ---- STAGE 4: BUNDLE + TEST (whole-harness stations) ----
	if job.TieStation != "" && !opts.DryRun {
		if st, ok := stations[job.TieStation]; ok {
			_ = o.ServeStation(ctx, st) // tie faults are recorded on the station, not a wire
		}
	}
	if job.TestStation != "" && !opts.DryRun {
		if st, ok := stations[job.TestStation]; ok {
			_ = o.ServeStation(ctx, st)
		}
	}

	for i := range job.Wires {
		res.Wires = append(res.Wires, *wr[i])
		// a wire "completed" if it didn't hard-fail; operator-flagged still counts
		// as completed by the cell (the manual touch happens at the operator station).
		if wr[i].OK {
			res.Completed++
		}
	}
	res.OK = res.Completed == res.Total
	res.TookMs = time.Since(start).Milliseconds()
	return res
}

func routeCode(o Outcome) string {
	if o.Obstruction {
		return "obstruction"
	}
	if o.Code != "" {
		return o.Code
	}
	return "route_failed"
}

// --- file-backed job store (local-first) ---

type JobStore struct{ dir string }

func DefaultJobStore() *JobStore {
	home, _ := os.UserHomeDir()
	return &JobStore{dir: filepath.Join(home, ".yaver", "cell-jobs")}
}

func (s *JobStore) path(id string) string { return filepath.Join(s.dir, safeName(id)+".json") }

func (s *JobStore) Save(j Job) error {
	if strings.TrimSpace(j.ID) == "" {
		return fmt.Errorf("job id required")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if j.CreatedAt == 0 {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	b, _ := json.MarshalIndent(j, "", "  ")
	return os.WriteFile(s.path(j.ID), b, 0o600)
}

func (s *JobStore) Get(id string) (Job, error) {
	var j Job
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return j, fmt.Errorf("job %q not found", id)
	}
	return j, json.Unmarshal(b, &j)
}

func (s *JobStore) Delete(id string) error { return os.Remove(s.path(id)) }

func (s *JobStore) List() []Job {
	out := []Job{}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(s.dir, e.Name())); err == nil {
			var j Job
			if json.Unmarshal(b, &j) == nil {
				out = append(out, j)
			}
		}
	}
	sort.Slice(out, func(i, j2 int) bool { return out[i].ID < out[j2].ID })
	return out
}
