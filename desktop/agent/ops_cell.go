package main

// ops_cell.go — the wire-harness CELL as native ops verbs: the Fairino arm
// serves a ring of cheap semiautomatic stations (ferrule crimp, terminal press,
// seal, heatshrink apply/heat, mark, klemens insert, connector insert, pull-test)
// it presents the cut lead end to. Stations are registered once (present-pose +
// trigger/done handshake), then a per-SKU cell program is taught and replayed.
//
// This is the sibling of ops_arm.go / ops_machine.go: it reuses the arm
// Controller (present-pose moves + vision verify) and the machine engine (modbus
// trigger/done) through the cell package's Presenter/StationIO seams. See
// docs/yaver-arm-served-harness-cell.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/arm"
	"github.com/yaver-io/agent/cell"
	"github.com/yaver-io/agent/machine"
	"github.com/yaver-io/agent/robot"
)

const cellVaultProject = "robot"
const cellVaultStationsName = "cell-stations"

var (
	cellStore     = cell.DefaultProgramStore()
	cellJobStore  = cell.DefaultJobStore()
	cellStationMu sync.Mutex
	cellStations  map[string]cell.Station // cached station map (id -> Station)
)

func cellStationsFilePath() string {
	home, _ := os.UserHomeDir()
	return home + "/.yaver/cell-stations.json"
}

// cellStationsGet loads the registered station map (vault first, then file).
func cellStationsGet() map[string]cell.Station {
	cellStationMu.Lock()
	defer cellStationMu.Unlock()
	if cellStations != nil {
		return cloneStations(cellStations)
	}
	m := map[string]cell.Station{}
	loaded := false
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(cellVaultProject, cellVaultStationsName); gerr == nil && e != nil && e.Value != "" {
			if json.Unmarshal([]byte(e.Value), &m) == nil {
				loaded = true
			}
		}
	}
	if !loaded {
		if b, err := os.ReadFile(cellStationsFilePath()); err == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	if m == nil {
		m = map[string]cell.Station{}
	}
	cellStations = m
	return cloneStations(m)
}

func cellStationsSave(m map[string]cell.Station) error {
	b, _ := json.Marshal(m)
	var vaultErr error
	if vs, err := openVaultOptional(); err == nil {
		vaultErr = vs.Set(VaultEntry{Project: cellVaultProject, Name: cellVaultStationsName, Category: "custom", Value: string(b), Notes: "Yaver harness cell — station map (present-pose + handshake)"})
	} else {
		vaultErr = err
	}
	if vaultErr != nil {
		if ferr := os.WriteFile(cellStationsFilePath(), b, 0o600); ferr != nil {
			return ferr
		}
	}
	cellStationMu.Lock()
	cellStations = cloneStations(m)
	cellStationMu.Unlock()
	return nil
}

func cloneStations(m map[string]cell.Station) map[string]cell.Station {
	out := make(map[string]cell.Station, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// armPresenter adapts *arm.Controller to cell.Presenter.
type armPresenter struct{ c *arm.Controller }

func (a armPresenter) MoveWaypoint(ctx context.Context, wp arm.Waypoint) cell.Outcome {
	var mr arm.MoveResult
	switch {
	case len(wp.Joints) > 0:
		mr = a.c.MoveJoints(ctx, wp.Joints, wp.VelPct, wp.AccPct, wp.Verify, wp.Expect)
	case wp.Pose != nil:
		mr = a.c.MovePose(ctx, *wp.Pose, wp.VelPct, wp.AccPct, wp.Verify, wp.Expect)
	default:
		return cell.Outcome{OK: false, Code: "bad_waypoint", Error: "waypoint has neither joints nor pose"}
	}
	return outcomeFromMove(mr)
}

func (a armPresenter) Verify(ctx context.Context, expectation string) cell.Outcome {
	return outcomeFromMove(a.c.Verify(ctx, expectation))
}

func (a armPresenter) ForceInsert(ctx context.Context, dir arm.Axis6, limitN, maxDistMm float64) cell.Outcome {
	fr := a.c.ForceMove(ctx, dir, limitN, maxDistMm, 0)
	o := cell.Outcome{OK: fr.OK && fr.Seated, Code: fr.Code, Error: fr.Error}
	if fr.OK && !fr.Seated && o.Error == "" {
		o.Error = "push-in did not reach the seating force within travel"
	}
	return o
}

func (a armPresenter) EStop(ctx context.Context) error { return a.c.EStop(ctx) }

func outcomeFromMove(mr arm.MoveResult) cell.Outcome {
	o := cell.Outcome{OK: mr.OK, Code: mr.Code, Error: mr.Error}
	if mr.Verify != nil && mr.Verify.Obstruction {
		o.Obstruction = true
	}
	return o
}

// cellIOFactory builds a StationIO from a station's handshake: a modbus coil
// write / register poll via the machine engine, a vision check via the arm
// camera, or a no-op for manual/timeout. Resolved lazily so a cell with only
// manual stations needs no machine engine.
func cellIOFactory(c OpsContext, ctrl *arm.Controller) cell.IOFactory {
	return func(st cell.Station) (cell.StationIO, error) {
		h := st.Handshake
		io := cell.FuncIO{}

		// Trigger
		switch h.Trigger {
		case cell.TriggerModbus:
			drv, err := cellDriver(c, st.DriverID)
			if err != nil {
				return nil, err
			}
			tag, val := h.TriggerTag, h.TriggerValue
			if val == 0 {
				val = 1
			}
			io.TrigFn = func(ctx context.Context) error {
				if tag == "" {
					return fmt.Errorf("station %q: modbus trigger needs triggerTag", st.ID)
				}
				return drv.Write(ctx, []machine.TagWrite{{Ref: machine.TagRef{Name: tag}, Value: val}})
			}
		default: // none / manual → no electrical trigger
		}

		// Done
		switch h.Done {
		case cell.DoneModbus:
			drv, err := cellDriver(c, st.DriverID)
			if err != nil {
				return nil, err
			}
			tag, want := h.DoneTag, h.DoneValue
			io.DoneFn = func(ctx context.Context) (bool, error) {
				if tag == "" {
					return true, nil
				}
				s, err := drv.Read(ctx, []machine.TagRef{{Name: tag}})
				if err != nil {
					return false, err
				}
				if len(s) == 0 {
					return false, nil
				}
				if want == 0 {
					return s[0].Value != 0, nil
				}
				return s[0].Value == want, nil
			}
		case cell.DoneVision:
			expect := h.DoneExpect
			io.DoneFn = func(ctx context.Context) (bool, error) {
				return cellVisionDone(ctx, ctrl, expect)
			}
		default: // timeout / manual handled by the orchestrator's dwell
		}
		return io, nil
	}
}

func cellDriver(c OpsContext, id string) (machine.Driver, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("station has no driverId for its modbus handshake")
	}
	eng, err := c.Server.ensureMachine()
	if err != nil {
		return nil, fmt.Errorf("machine engine unavailable: %w", err)
	}
	drv, ok := eng.GetDriver(id)
	if !ok {
		return nil, fmt.Errorf("machine driver %q not connected (machine_connect it first)", id)
	}
	return drv, nil
}

// cellVisionDone asks the on-device vision model a yes/no about the station's
// done expectation (e.g. "green OK lamp lit"). Coarse but zero-integration — the
// last-resort done-sense for a machine with no electrical handshake (design §4).
func cellVisionDone(ctx context.Context, ctrl *arm.Controller, expect string) (bool, error) {
	if ctrl.Camera == nil || !ctrl.Camera.Available() {
		return false, fmt.Errorf("vision done-sense needs a camera")
	}
	frame, err := ctrl.Camera.Grab(ctx)
	if err != nil {
		return false, err
	}
	prompt := "Answer strictly yes or no. Is the following true about the machine in view? " + expect
	ans, err := robot.AskVision(ctx, ctrl.Vision, frame, prompt)
	if err != nil {
		return false, err
	}
	a := strings.ToLower(strings.TrimSpace(ans))
	return strings.HasPrefix(a, "yes") || strings.HasPrefix(a, "true") || strings.HasPrefix(a, "done"), nil
}

type cellPayload struct {
	Station *cell.Station     `json:"station"`
	Program *cell.CellProgram `json:"program"`
	Job     *cell.Job         `json:"job"`
	Opts    *cell.JobOpts     `json:"opts"`
	ID      string            `json:"id"`
	SKU     string            `json:"sku"`
	Slot    string            `json:"slot"` // teach target: "present" (default) | "approach" | "withdraw"
	Label   string            `json:"label"`
	DwellMs int               `json:"dwellMs"`
	VelPct  int               `json:"velPct"`
	AccPct  int               `json:"accPct"`
	DryRun  bool              `json:"dryRun"`
}

func parseCell(p json.RawMessage) cellPayload {
	var cp cellPayload
	if len(p) > 0 {
		_ = json.Unmarshal(p, &cp)
	}
	return cp
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	// --- station registry ---
	reg("cell_station_add", "Register/update a harness-cell station {station:{id,kind,driverId,present,handshake,...}}", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		if cp.Station == nil || strings.TrimSpace(cp.Station.ID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "station.id required"}
		}
		m := cellStationsGet()
		m[cp.Station.ID] = *cp.Station
		if err := cellStationsSave(m); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"station": *cp.Station, "count": len(m)}}
	})
	reg("cell_station_list", "List registered stations", func(c OpsContext, _ json.RawMessage) OpsResult {
		m := cellStationsGet()
		out := make([]cell.Station, 0, len(m))
		for _, s := range m {
			out = append(out, s)
		}
		return OpsResult{OK: true, Initial: map[string]any{"stations": out}}
	})
	reg("cell_station_get", "Get one station by id", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		m := cellStationsGet()
		s, ok := m[cp.ID]
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "station " + cp.ID + " not found"}
		}
		return OpsResult{OK: true, Initial: map[string]any{"station": s}}
	})
	reg("cell_station_delete", "Delete a station by id", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		m := cellStationsGet()
		if _, ok := m[cp.ID]; !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "station " + cp.ID + " not found"}
		}
		delete(m, cp.ID)
		if err := cellStationsSave(m); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"deleted": cp.ID, "count": len(m)}}
	})
	reg("cell_station_teach", "Capture the arm's current pose (while hand-guiding) as a station present/approach/withdraw pose {id, slot, label}", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		cp := parseCell(payload)
		m := cellStationsGet()
		st, ok := m[cp.ID]
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "station " + cp.ID + " not found"}
		}
		wp, err := ctrl.Capture(c.Ctx, cp.VelPct, cp.AccPct, cp.DwellMs, cp.Label)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		switch strings.ToLower(cp.Slot) {
		case "approach":
			st.Approach = &wp
		case "withdraw":
			st.Withdraw = &wp
		default:
			st.Present = append(st.Present, wp)
		}
		m[cp.ID] = st
		if err := cellStationsSave(m); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"station": st, "captured": wp}}
	})
	reg("cell_station_test", "Dry-run a station handshake (trigger + wait done) with NO arm motion — verifies wiring {id}", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		cp := parseCell(payload)
		m := cellStationsGet()
		st, ok := m[cp.ID]
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "station " + cp.ID + " not found"}
		}
		io, err := cellIOFactory(c, ctrl)(st)
		if err != nil {
			return OpsResult{OK: false, Code: "io_unavailable", Error: err.Error()}
		}
		start := time.Now()
		if err := io.Trigger(c.Ctx); err != nil {
			return OpsResult{OK: false, Code: "trigger_failed", Error: err.Error()}
		}
		done, derr := cellTestWaitDone(c.Ctx, st, io)
		res := map[string]any{"triggered": true, "done": done, "tookMs": time.Since(start).Milliseconds()}
		if derr != nil {
			res["doneError"] = derr.Error()
			return OpsResult{OK: false, Code: "done_timeout", Error: derr.Error(), Initial: res}
		}
		return OpsResult{OK: true, Initial: res}
	})

	// --- cell programs (per-SKU taught sequence) ---
	reg("cell_program_save", "Save a per-SKU cell program {program:{sku,leads,positionMap}} (validated against the station map)", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		if cp.Program == nil || strings.TrimSpace(cp.Program.SKU) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "program.sku required"}
		}
		if probs := cell.ValidateSequence(*cp.Program, cellStationsGet()); len(probs) > 0 {
			return OpsResult{OK: false, Code: "constraint_violation", Error: strings.Join(probs, "; "), Initial: map[string]any{"problems": probs}}
		}
		if err := cellStore.Save(*cp.Program); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"saved": cp.Program.SKU, "leads": len(cp.Program.Leads)}}
	})
	reg("cell_program_list", "List taught cell programs", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"programs": cellStore.List()}}
	})
	reg("cell_program_get", "Get a cell program by sku", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		prog, err := cellStore.Get(cp.SKU)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: prog}
	})
	reg("cell_program_delete", "Delete a cell program by sku", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		if err := cellStore.Delete(cp.SKU); err != nil {
			return OpsResult{OK: false, Code: "delete_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"deleted": cp.SKU}}
	})
	reg("cell_program_run", "Replay a cell program end-to-end: arm serves each station for each lead end (rendezvous state machine) {sku, dryRun?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		cp := parseCell(payload)
		prog := cell.CellProgram{SKU: cp.SKU}
		if cp.Program != nil {
			prog = *cp.Program
		}
		if strings.TrimSpace(prog.SKU) != "" && len(prog.Leads) == 0 {
			loaded, err := cellStore.Get(prog.SKU)
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			prog = loaded
		}
		stations := cellStationsGet()
		if probs := cell.ValidateSequence(prog, stations); len(probs) > 0 {
			return OpsResult{OK: false, Code: "constraint_violation", Error: strings.Join(probs, "; "), Initial: map[string]any{"problems": probs}}
		}
		orch := cell.NewOrchestrator(armPresenter{c: ctrl}, cellIOFactory(c, ctrl))
		res := orch.Run(c.Ctx, prog, stations, cp.DryRun)
		return OpsResult{OK: res.OK, Initial: res}
	})

	// --- data-driven jobs (wire-list → 4-stage run) ---
	reg("cell_job_save", "Save a data-driven harness job {job:{id,wires,lanes,routes,tieStation,testStation}} (validated against the station map)", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		if cp.Job == nil || strings.TrimSpace(cp.Job.ID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "job.id required"}
		}
		if probs := cell.ValidateJob(*cp.Job, cellStationsGet()); len(probs) > 0 {
			return OpsResult{OK: false, Code: "invalid_job", Error: strings.Join(probs, "; "), Initial: map[string]any{"problems": probs}}
		}
		if err := cellJobStore.Save(*cp.Job); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"saved": cp.Job.ID, "wires": len(cp.Job.Wires)}}
	})
	reg("cell_job_list", "List saved harness jobs", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"jobs": cellJobStore.List()}}
	})
	reg("cell_job_get", "Get a harness job by id", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		j, err := cellJobStore.Get(cp.ID)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: j}
	})
	reg("cell_job_delete", "Delete a harness job by id", func(c OpsContext, payload json.RawMessage) OpsResult {
		cp := parseCell(payload)
		if err := cellJobStore.Delete(cp.ID); err != nil {
			return OpsResult{OK: false, Code: "delete_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"deleted": cp.ID}}
	})
	reg("cell_job_run", "Run a data-driven harness job: PREP lanes → ROUTE → TERMINATE (push-in auto / screw+twin flagged for operator) → BUNDLE+TEST {id|job, dryRun?, opts?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		cp := parseCell(payload)
		job := cell.Job{ID: cp.ID}
		if cp.Job != nil {
			job = *cp.Job
		}
		if strings.TrimSpace(job.ID) != "" && len(job.Wires) == 0 {
			loaded, err := cellJobStore.Get(job.ID)
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			job = loaded
		}
		stations := cellStationsGet()
		if probs := cell.ValidateJob(job, stations); len(probs) > 0 {
			return OpsResult{OK: false, Code: "invalid_job", Error: strings.Join(probs, "; "), Initial: map[string]any{"problems": probs}}
		}
		opts := cell.JobOpts{DryRun: cp.DryRun}
		if cp.Opts != nil {
			opts = *cp.Opts
			if cp.DryRun {
				opts.DryRun = true
			}
		}
		orch := cell.NewOrchestrator(armPresenter{c: ctrl}, cellIOFactory(c, ctrl))
		res := orch.RunJob(c.Ctx, job, stations, opts)
		return OpsResult{OK: res.OK, Initial: res}
	})

	reg("cell_status", "Harness-cell status: arm + registered stations + taught programs", func(c OpsContext, _ json.RawMessage) OpsResult {
		m := cellStationsGet()
		out := map[string]any{"stations": len(m), "programs": len(cellStore.List()), "jobs": len(cellJobStore.List())}
		if armEnabled() {
			if ctrl, err := ensureArm(); err == nil && ctrl != nil {
				st, _ := ctrl.Status(c.Ctx)
				out["arm"] = st
			}
		}
		return OpsResult{OK: true, Initial: out}
	})
}

// cellTestWaitDone is cell_station_test's small done-waiter (the orchestrator's
// waitDone is unexported; this mirrors it for the dry handshake test).
func cellTestWaitDone(ctx context.Context, st cell.Station, io cell.StationIO) (bool, error) {
	h := st.Handshake
	switch h.Done {
	case cell.DoneModbus, cell.DoneVision:
		timeout := h.TimeoutMs
		if timeout <= 0 {
			timeout = 30000
		}
		poll := h.PollMs
		if poll <= 0 {
			poll = 250
		}
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		for {
			done, err := io.Done(ctx)
			if err != nil {
				return false, err
			}
			if done {
				return true, nil
			}
			if time.Now().After(deadline) {
				return false, fmt.Errorf("no done within %dms", timeout)
			}
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(time.Duration(poll) * time.Millisecond):
			}
		}
	default:
		ms := h.DwellMs
		if ms <= 0 {
			ms = h.TimeoutMs
		}
		if ms > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(time.Duration(ms) * time.Millisecond):
			}
		}
		return true, nil
	}
}
