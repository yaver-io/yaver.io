package arm

// backend_sim.go — the SIMULATOR backend. It implements the same arm.Backend
// interface as the real-hardware drivers, so a simulated robot is driven by the
// exact same arm_* verbs, teach-and-repeat, and (crucially) the same camera
// path: the sim renders frames the existing snapshot/vision loop consumes, so the
// mobile/web UI shows the arm moving with no special casing.
//
// Mechanically it is the proven bridge pattern (a thin local process owns the
// physics; Yaver speaks JSON over HTTP to it) plus three things the bridge
// doesn't do:
//   1. lifecycle — Yaver SPAWNS the harness (a PyBullet process) and tears it
//      down, instead of expecting one to already be running;
//   2. rendering — the harness serves a JPEG at /frame.jpg, which armCamera wires
//      as an HTTPCamera so snapshot/look/verify render the simulation;
//   3. model swap / reset — load a different robot or re-home without restart.
//
// The harness script is EMBEDDED in the binary (sim_harness.py) and extracted to
// ~/.yaver/sim on first use, so it travels with the agent — no repo checkout
// needed on the box. Engine is PyBullet today; "mujoco" is a config seam.

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed sim_harness.py
var simHarnessPy []byte

// SimBackend manages a headless physics-sim subprocess and drives it.
type SimBackend struct {
	cfg    Config
	engine string
	model  string
	port   int
	python string
	gui    bool

	bridge *BridgeArmBackend // JSON-over-HTTP contract to the harness
	client *http.Client

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	spawned bool
	stderr  *bytes.Buffer
}

// NewSimBackend builds (but does not start) the simulator backend.
func NewSimBackend(cfg Config) *SimBackend {
	cfg.Normalize()
	port := cfg.Sim.Port
	if port <= 0 {
		port = SimDefaultPort
	}
	python := strings.TrimSpace(cfg.Sim.Python)
	if python == "" {
		python = "python3"
	}
	model := strings.TrimSpace(cfg.Sim.Model)
	if model == "" {
		model = "builtin:arm6"
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	bcfg := cfg
	bcfg.Addr = base
	return &SimBackend{
		cfg:    cfg,
		engine: cfg.Sim.Engine,
		model:  model,
		port:   port,
		python: python,
		gui:    cfg.Sim.GUI,
		bridge: NewBridgeArmBackend(bcfg, base),
		client: &http.Client{Timeout: 15 * time.Second},
		stderr: &bytes.Buffer{},
	}
}

func (s *SimBackend) Name() string { return "sim" }

// FrameURL is the harness's single-frame JPEG endpoint; armCamera wires this as
// an HTTPCamera so the existing snapshot/vision path renders the sim.
func (s *SimBackend) FrameURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/frame.jpg", s.port)
}

// simHarnessPath extracts the embedded harness to ~/.yaver/sim, refreshing it
// only when the embedded content changes (so a binary upgrade ships a new
// harness automatically). Returns the on-disk path.
func simHarnessPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver", "sim")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(simHarnessPy)
	want := hex.EncodeToString(sum[:8])
	path := filepath.Join(dir, "sim_harness.py")
	if cur, err := os.ReadFile(path); err == nil {
		curSum := sha256.Sum256(cur)
		if hex.EncodeToString(curSum[:8]) == want {
			return path, nil // up to date
		}
	}
	if err := os.WriteFile(path, simHarnessPy, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// reachable reports whether a harness is already answering on our port.
func (s *SimBackend) reachable(ctx context.Context) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", s.port)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func (s *SimBackend) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reuse a harness that's already up (agent restart, or a manually-launched
	// one) instead of fighting over the port.
	if s.portBusy() {
		quick, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if s.reachable(quick) {
			return s.bridge.Connect(ctx)
		}
		return fmt.Errorf("sim: port %d is in use but not answering as a Yaver harness; set a different sim.port", s.port)
	}
	if _, err := exec.LookPath(s.python); err != nil {
		return fmt.Errorf("sim: %q not found — install Python 3 (the sim harness needs python3 + pybullet: pip install pybullet)", s.python)
	}
	script, err := simHarnessPath()
	if err != nil {
		return fmt.Errorf("sim: extract harness: %w", err)
	}
	args := []string{script, "--port", strconv.Itoa(s.port), "--model", s.model}
	if s.engine != "" {
		args = append(args, "--engine", s.engine)
	}
	if s.gui {
		args = append(args, "--gui")
	}
	cctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cctx, s.python, args...)
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("sim: start harness: %w", err)
	}
	s.cmd, s.cancel, s.spawned = cmd, cancel, true

	// Wait for readiness. First load of a "desc:" model may download assets, so
	// be generous. If the process dies, surface its stderr.
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("sim: harness exited: %s", s.tailStderr())
		}
		probe, pcancel := context.WithTimeout(ctx, 2*time.Second)
		ok := s.reachable(probe)
		pcancel()
		if ok {
			return s.bridge.Connect(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	return fmt.Errorf("sim: harness did not become ready on :%d: %s", s.port, s.tailStderr())
}

func (s *SimBackend) portBusy() bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

func (s *SimBackend) tailStderr() string {
	out := strings.TrimSpace(s.stderr.String())
	if len(out) > 800 {
		out = "…" + out[len(out)-800:]
	}
	if out == "" {
		out = "(no output)"
	}
	return out
}

func (s *SimBackend) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spawned && s.cancel != nil {
		s.cancel()
		if s.cmd != nil {
			_ = s.cmd.Wait()
		}
		s.spawned = false
	}
	return nil
}

// --- Backend contract: delegate to the harness over the bridge ---

func (s *SimBackend) Describe(ctx context.Context) (ArmInfo, error) {
	return s.bridge.Describe(ctx)
}
func (s *SimBackend) Status(ctx context.Context) (ArmStatus, error) {
	st, err := s.bridge.Status(ctx)
	st.Backend = "sim"
	return st, err
}
func (s *SimBackend) Enable(ctx context.Context, on bool) error { return s.bridge.Enable(ctx, on) }
func (s *SimBackend) JointState(ctx context.Context) ([]JointState, error) {
	return s.bridge.JointState(ctx)
}
func (s *SimBackend) Pose(ctx context.Context) (Pose, error) { return s.bridge.Pose(ctx) }
func (s *SimBackend) MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int) error {
	return s.bridge.MoveJoints(ctx, targets, velPct, accPct)
}
func (s *SimBackend) MoveLinear(ctx context.Context, p Pose, velPct, accPct int) error {
	return s.bridge.MoveLinear(ctx, p, velPct, accPct)
}
func (s *SimBackend) WaitIdle(ctx context.Context) error { return s.bridge.WaitIdle(ctx) }
func (s *SimBackend) Stop(ctx context.Context) error     { return s.bridge.Stop(ctx) }
func (s *SimBackend) EStop(ctx context.Context) error    { return s.bridge.EStop(ctx) }
func (s *SimBackend) FreeDrive(ctx context.Context, on bool) error {
	return s.bridge.FreeDrive(ctx, on)
}
func (s *SimBackend) Raw(ctx context.Context, cmd string) (string, error) {
	return s.bridge.Raw(ctx, cmd)
}

// --- sim-only extras (sim_* verbs) ---

// Reset re-homes the robot in the sim (clears any taught/jogged state).
func (s *SimBackend) Reset(ctx context.Context) error {
	return s.post(ctx, "/reset", map[string]any{}, nil)
}

// LoadModel swaps the robot loaded in the running sim and returns its read-back
// ArmInfo (exact joints from the URDF, via the harness).
func (s *SimBackend) LoadModel(ctx context.Context, source string) (ArmInfo, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return ArmInfo{}, fmt.Errorf("sim: model source required")
	}
	var out struct {
		Info ArmInfo `json:"info"`
	}
	if err := s.post(ctx, "/load", map[string]any{"model": source}, &out); err != nil {
		return ArmInfo{}, err
	}
	s.model = source
	out.Info.Normalize()
	return out.Info, nil
}

func (s *SimBackend) post(ctx context.Context, path string, body, out any) error {
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d%s", s.port, path), bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sim %s: %w", path, err)
	}
	defer resp.Body.Close()
	var probe struct {
		OK    *bool  `json:"ok"`
		Error string `json:"error"`
	}
	raw := &bytes.Buffer{}
	_, _ = raw.ReadFrom(resp.Body)
	_ = json.Unmarshal(raw.Bytes(), &probe)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sim %s: http %d: %s", path, resp.StatusCode, strings.TrimSpace(raw.String()))
	}
	if probe.OK != nil && !*probe.OK {
		return fmt.Errorf("sim %s: %s", path, probe.Error)
	}
	if out != nil && raw.Len() > 0 {
		return json.Unmarshal(raw.Bytes(), out)
	}
	return nil
}

// compile-time assertion: SimBackend is a full arm.Backend.
var _ Backend = (*SimBackend)(nil)
