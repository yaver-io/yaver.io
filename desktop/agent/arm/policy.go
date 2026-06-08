package arm

// policy.go — run a LEARNED policy (an imitation / VLA model served on a rented
// GPU) on the arm, safely. This is the "robotic model running" half of the
// video→policy→arm pipeline: a model trained from operator demos (ACT / Diffusion
// Policy / SmolVLA / π0 via LeRobot) is served behind an HTTP endpoint that maps
//   observation {camera frames + joint state + task prompt}  →  an ACTION CHUNK
//   (a short burst of joint targets).
// Yaver grabs the obs, calls the endpoint, and executes the chunk on the arm —
// with a LOCAL SAFETY GATE in front of every motion. The gate is non-negotiable:
// per the design (docs/yaver-video-to-policy-harness-cell.md), a remote model and
// the network are NEVER trusted to move the arm unchecked. Out-of-range targets,
// oversized jumps, a stale/unreachable server, or an e-stop all halt motion —
// refused, not clamped, matching the rest of the arm layer.
//
// Action chunking (predict N steps, execute the burst, then re-observe) is what
// makes a cloud GPU viable for control: the model only has to produce a chunk per
// cycle, not close a hard real-time loop over the internet. Today the chunk is
// executed as sequential blocking moves (WaitIdle) — correct and safe on a cobot
// or the sim; true high-rate (50 Hz) streaming needs a streaming backend and is a
// documented next step.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PolicyConfig is the per-run policy binding + safety envelope.
type PolicyConfig struct {
	Endpoint   string  `json:"endpoint"`             // policy server base URL (rented GPU / local Jetson)
	APIKey     string  `json:"apiKey,omitempty"`     // bearer token, if the server needs one
	Prompt     string  `json:"prompt,omitempty"`     // language/task conditioning ("seat the white wire in cavity 3")
	Model      string  `json:"model,omitempty"`      // informational: which policy (act/smolvla/pi0/...)
	MaxStepDeg float64 `json:"maxStepDeg,omitempty"` // SAFETY: refuse any single action that moves a joint more than this (deg/mm)
	MaxChunks  int     `json:"maxChunks,omitempty"`  // stop after N chunks (0 → until done/stop/maxSeconds)
	MaxSeconds int     `json:"maxSeconds,omitempty"` // wall-clock budget (0 → default 60s)
	VelPct     int     `json:"velPct,omitempty"`     // execution speed (0 → config default)
	Verify     string  `json:"verify,omitempty"`     // per-chunk camera verify cadence: "off" (default) | "frames" | "agent"
}

func (p *PolicyConfig) normalize() {
	if p.MaxStepDeg <= 0 {
		p.MaxStepDeg = 30 // a generous-but-finite per-step cap; tighten for contact work
	}
	if p.MaxSeconds <= 0 {
		p.MaxSeconds = 60
	}
	if p.Verify == "" {
		p.Verify = "off" // policy loops run fast; skip vision per-chunk by default
	}
}

// --- policy server wire types (the served-model contract) ---
//
// POST <endpoint>/act  { images:{name:dataURL}, state:{joints,pose?}, prompt? }
//   -> { actions:[ {joints:{name:val}} ... ], done?:bool }
// GET  <endpoint>/healthz -> 200
//
// This mirrors how openpi / a LeRobot policy server expose inference: an obs dict
// in, an action chunk out. A thin python shim (yaver_policy_server.py, documented)
// wraps any LeRobot/ACT/π0 checkpoint to speak it.

type policyObs struct {
	Images map[string]string `json:"images"` // camera name → "data:image/jpeg;base64,…"
	State  policyState       `json:"state"`
	Prompt string            `json:"prompt,omitempty"`
}

type policyState struct {
	Joints map[string]float64 `json:"joints"`
	Pose   *Pose              `json:"pose,omitempty"`
}

// PolicyAction is one step of a chunk: absolute joint targets (preferred) and/or
// a Cartesian pose. Joint targets are what the safety gate checks.
type PolicyAction struct {
	Joints map[string]float64 `json:"joints,omitempty"`
	Pose   *Pose              `json:"pose,omitempty"`
}

// PolicyChunk is one inference result: a short burst of actions plus an optional
// done flag the policy raises when it judges the task complete.
type PolicyChunk struct {
	Actions []PolicyAction `json:"actions"`
	Done    bool           `json:"done,omitempty"`
}

// PolicyClient talks to the served model.
type PolicyClient struct {
	base   string
	key    string
	client *http.Client
}

// NewPolicyClient builds a client. timeout doubles as the stale-server watchdog:
// if a chunk doesn't come back in time, the run stops (the arm never waits on a
// hung GPU mid-task).
func NewPolicyClient(endpoint, apiKey string, timeout time.Duration) *PolicyClient {
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base != "" && !strings.HasPrefix(base, "http") {
		base = "http://" + base
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &PolicyClient{base: base, key: strings.TrimSpace(apiKey), client: &http.Client{Timeout: timeout}}
}

func (p *PolicyClient) req(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	return p.client.Do(req)
}

// Health probes the policy server.
func (p *PolicyClient) Health(ctx context.Context) error {
	if p.base == "" {
		return fmt.Errorf("policy: no endpoint configured")
	}
	resp, err := p.req(ctx, "GET", "/healthz", nil)
	if err != nil {
		return fmt.Errorf("policy: unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("policy: server unhealthy (status %d)", resp.StatusCode)
	}
	return nil
}

// Act runs one inference: observation in, action chunk out.
func (p *PolicyClient) Act(ctx context.Context, obs policyObs) (PolicyChunk, error) {
	if p.base == "" {
		return PolicyChunk{}, fmt.Errorf("policy: no endpoint configured")
	}
	resp, err := p.req(ctx, "POST", "/act", obs)
	if err != nil {
		return PolicyChunk{}, fmt.Errorf("policy: act: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return PolicyChunk{}, fmt.Errorf("policy: act http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var chunk PolicyChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return PolicyChunk{}, fmt.Errorf("policy: bad act response: %w", err)
	}
	return chunk, nil
}

// SafetyGate is the local guard in front of every policy-commanded motion. It is
// built from the arm's own ArmInfo, so its limits are the robot's real joint
// limits, not the model's assumptions.
type SafetyGate struct {
	byName     map[string]JointSpec
	maxStepDeg float64
}

// NewSafetyGate builds a gate from the arm description + a per-step cap.
func NewSafetyGate(info ArmInfo, maxStepDeg float64) SafetyGate {
	by := map[string]JointSpec{}
	for _, j := range info.Joints {
		by[lowerStr(j.Name)] = j
	}
	if maxStepDeg <= 0 {
		maxStepDeg = 30
	}
	return SafetyGate{byName: by, maxStepDeg: maxStepDeg}
}

// Check validates an action's joint targets against (a) the joint range — refused,
// never clamped — and (b) the per-step jump from the current position. cur is the
// last known/applied joint position by name. Returns nil if safe.
func (g SafetyGate) Check(cur, targets map[string]float64) error {
	for name, v := range targets {
		j, ok := g.byName[lowerStr(name)]
		if !ok {
			return fmt.Errorf("policy commanded unknown joint %q", name)
		}
		if j.jtype() != JointContinuous {
			if v < j.Min || v > j.Max {
				return fmt.Errorf("policy target %s=%.2f out of range [%.2f,%.2f] %s", j.Name, v, j.Min, j.Max, j.unit())
			}
		}
		if c, have := cur[lowerStr(name)]; have {
			if d := v - c; d > g.maxStepDeg || d < -g.maxStepDeg {
				return fmt.Errorf("policy step on %s too large: %.2f (cap ±%.2f %s) — refusing", j.Name, d, g.maxStepDeg, j.unit())
			}
		}
	}
	return nil
}

// PolicyRunResult reports a policy run.
type PolicyRunResult struct {
	OK      bool     `json:"ok"`
	Chunks  int      `json:"chunks"`
	Steps   int      `json:"steps"`
	Done    bool     `json:"done"`    // policy raised done
	Stopped string   `json:"stopped"` // why the loop ended
	Code    string   `json:"code,omitempty"`
	Error   string   `json:"error,omitempty"`
	Verify  *Verdict `json:"verify,omitempty"`
	TookMs  int64    `json:"tookMs"`
}

// RunPolicy executes a served policy on the arm under the safety gate. stop()
// lets a caller interrupt (policy_stop); pass nil for none. It re-observes after
// each chunk (closed-loop at the chunk level) and refuses/halts on any safety
// violation, e-stop, stale server, or budget exhaustion.
func (c *Controller) RunPolicy(ctx context.Context, client *PolicyClient, cfg PolicyConfig, stop func() bool) PolicyRunResult {
	cfg.normalize()
	start := time.Now()
	if c.isEStopped() {
		return PolicyRunResult{Code: "estopped", Error: "e-stopped; call reset first"}
	}
	if c.Camera == nil || !c.Camera.Available() {
		return PolicyRunResult{Code: "no_camera", Error: "policy needs a camera (the model is vision-conditioned); configure a camera or use the sim's rendered frames"}
	}
	info, _ := c.Describe(ctx)
	if len(info.Joints) == 0 {
		return PolicyRunResult{Code: "no_joints", Error: "no joints defined"}
	}
	gate := NewSafetyGate(info, cfg.MaxStepDeg)
	vel, _ := c.velAcc(cfg.VelPct, 0)
	deadline := start.Add(time.Duration(cfg.MaxSeconds) * time.Second)

	res := PolicyRunResult{}
	for {
		if stop != nil && stop() {
			res.Stopped = "stopped"
			break
		}
		if c.isEStopped() {
			res.Stopped = "estopped"
			res.Code = "estopped"
			break
		}
		if time.Now().After(deadline) {
			res.Stopped = "max_seconds"
			break
		}
		if cfg.MaxChunks > 0 && res.Chunks >= cfg.MaxChunks {
			res.Stopped = "max_chunks"
			break
		}

		// observe
		frame, ferr := c.Camera.Grab(ctx)
		if ferr != nil {
			res.Code, res.Error, res.Stopped = "no_camera", ferr.Error(), "camera_error"
			break
		}
		js, pose, serr := c.State(ctx)
		if serr != nil {
			res.Code, res.Error, res.Stopped = "backend", serr.Error(), "state_error"
			break
		}
		curJoints := map[string]float64{}
		for _, j := range js {
			curJoints[lowerStr(j.Name)] = j.Position
		}
		obs := policyObs{
			Images: map[string]string{"main": jpegDataURL(frame)},
			State:  policyState{Joints: namedJoints(js), Pose: pose},
			Prompt: cfg.Prompt,
		}

		// infer (the http timeout is the stale-server watchdog)
		chunk, aerr := client.Act(ctx, obs)
		if aerr != nil {
			res.Code, res.Error, res.Stopped = "policy_error", aerr.Error(), "policy_error"
			break
		}
		res.Chunks++

		// execute the chunk through the safety gate
		for _, act := range chunk.Actions {
			if c.isEStopped() {
				break
			}
			if len(act.Joints) == 0 {
				continue // pose-only actions need backend IK; skipped in v1 (joint-space)
			}
			if err := gate.Check(curJoints, act.Joints); err != nil {
				_ = c.EStop(ctx) // safety violation → latch e-stop, do not move
				res.Code, res.Error, res.Stopped = "safety", err.Error(), "safety"
				res.TookMs = time.Since(start).Milliseconds()
				return res
			}
			if err := c.checkLimits(ctx, act.Joints); err != nil {
				_ = c.EStop(ctx)
				res.Code, res.Error, res.Stopped = "out_of_range", err.Error(), "safety"
				res.TookMs = time.Since(start).Milliseconds()
				return res
			}
			if err := c.Backend.MoveJoints(ctx, act.Joints, vel, vel); err != nil {
				res.Code, res.Error, res.Stopped = "backend", err.Error(), "backend_error"
				res.TookMs = time.Since(start).Milliseconds()
				return res
			}
			if err := c.Backend.WaitIdle(ctx); err != nil {
				res.Code, res.Error, res.Stopped = "backend", err.Error(), "backend_error"
				res.TookMs = time.Since(start).Milliseconds()
				return res
			}
			for n, v := range act.Joints {
				curJoints[lowerStr(n)] = v
			}
			res.Steps++
		}

		if chunk.Done {
			res.Done = true
			res.Stopped = "done"
			break
		}
	}

	// optional final verify
	if cfg.Verify == "frames" || cfg.Verify == "agent" {
		v := c.Verify(ctx, cfg.Prompt)
		res.Verify = v.Verify
	}
	res.OK = res.Stopped == "done" || res.Stopped == "max_chunks" || res.Stopped == "max_seconds" || res.Stopped == "stopped"
	res.TookMs = time.Since(start).Milliseconds()
	return res
}

// namedJoints flattens a JointState slice to name→position.
func namedJoints(js []JointState) map[string]float64 {
	out := make(map[string]float64, len(js))
	for _, j := range js {
		out[j.Name] = j.Position
	}
	return out
}
