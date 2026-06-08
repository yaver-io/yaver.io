package arm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yaver-io/agent/robot"
)

// --- fakes (in-memory backend + camera), per the repo's real-server test style ---

type fakeBackend struct {
	mu     sync.Mutex
	joints map[string]float64
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{joints: map[string]float64{"J1": 0, "J2": 0}}
}

func (b *fakeBackend) Name() string                       { return "fake" }
func (b *fakeBackend) Connect(context.Context) error      { return nil }
func (b *fakeBackend) Close() error                       { return nil }
func (b *fakeBackend) Describe(context.Context) (ArmInfo, error) {
	info := ArmInfo{Joints: []JointSpec{
		{Name: "J1", Type: JointRevolute, Min: -170, Max: 170, Unit: "deg"},
		{Name: "J2", Type: JointRevolute, Min: -120, Max: 120, Unit: "deg"},
	}}
	info.Normalize()
	return info, nil
}
func (b *fakeBackend) Status(context.Context) (ArmStatus, error) {
	return ArmStatus{OK: true, Backend: "fake", Connected: true, Enabled: true}, nil
}
func (b *fakeBackend) Enable(context.Context, bool) error { return nil }
func (b *fakeBackend) JointState(context.Context) ([]JointState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return []JointState{
		{Name: "J1", Position: b.joints["J1"], Unit: "deg"},
		{Name: "J2", Position: b.joints["J2"], Unit: "deg"},
	}, nil
}
func (b *fakeBackend) Pose(context.Context) (Pose, error) { return Pose{}, ErrNoCartesian }
func (b *fakeBackend) MoveJoints(_ context.Context, targets map[string]float64, _, _ int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, v := range targets {
		b.joints[k] = v
	}
	return nil
}
func (b *fakeBackend) MoveLinear(context.Context, Pose, int, int) error { return ErrNoCartesian }
func (b *fakeBackend) WaitIdle(context.Context) error                   { return nil }
func (b *fakeBackend) Stop(context.Context) error                       { return nil }
func (b *fakeBackend) EStop(context.Context) error                      { return nil }
func (b *fakeBackend) FreeDrive(context.Context, bool) error            { return nil }
func (b *fakeBackend) Raw(context.Context, string) (string, error)      { return "", nil }

type fakeCamera struct{}

func (fakeCamera) Available() bool { return true }
func (fakeCamera) Grab(context.Context) ([]byte, error) {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}, nil
}

func newFakeController() *Controller {
	c := NewController(newFakeBackend(), fakeCamera{}, robot.VisionConfig{}, Config{})
	return c
}

// policyServer is a fake served model: nudges J1 by +step each chunk until it
// reaches `goal`, then reports done. Optional out-of-range / big-jump injectors
// exercise the safety gate.
func policyServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/act", func(w http.ResponseWriter, r *http.Request) {
		var obs policyObs
		_ = json.NewDecoder(r.Body).Decode(&obs)
		cur := obs.State.Joints["J1"]
		var chunk PolicyChunk
		switch mode {
		case "out_of_range":
			chunk = PolicyChunk{Actions: []PolicyAction{{Joints: map[string]float64{"J1": 999}}}}
		case "big_jump":
			chunk = PolicyChunk{Actions: []PolicyAction{{Joints: map[string]float64{"J1": cur + 120}}}}
		default: // reach goal 30 in +10 steps
			next := cur + 10
			done := next >= 30
			if next > 30 {
				next = 30
			}
			chunk = PolicyChunk{Actions: []PolicyAction{{Joints: map[string]float64{"J1": next}}}, Done: done}
		}
		_ = json.NewEncoder(w).Encode(chunk)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunPolicyHappyPath(t *testing.T) {
	srv := policyServer(t, "reach")
	c := newFakeController()
	client := NewPolicyClient(srv.URL, "", 5*time.Second)
	res := c.RunPolicy(context.Background(), client, PolicyConfig{MaxStepDeg: 15, MaxSeconds: 5}, nil)
	if !res.OK || !res.Done {
		t.Fatalf("expected done, got %+v", res)
	}
	if res.Stopped != "done" {
		t.Errorf("stopped=%q want done", res.Stopped)
	}
	if res.Steps < 3 {
		t.Errorf("steps=%d, expected several", res.Steps)
	}
	if c.isEStopped() {
		t.Error("should not e-stop on a clean run")
	}
}

func TestRunPolicySafetyOutOfRange(t *testing.T) {
	srv := policyServer(t, "out_of_range")
	c := newFakeController()
	client := NewPolicyClient(srv.URL, "", 5*time.Second)
	res := c.RunPolicy(context.Background(), client, PolicyConfig{MaxStepDeg: 9999, MaxSeconds: 5}, nil)
	if res.Stopped != "safety" {
		t.Fatalf("expected safety stop, got %+v", res)
	}
	if !c.isEStopped() {
		t.Error("out-of-range policy target must latch e-stop")
	}
}

func TestRunPolicySafetyBigJump(t *testing.T) {
	srv := policyServer(t, "big_jump")
	c := newFakeController()
	client := NewPolicyClient(srv.URL, "", 5*time.Second)
	res := c.RunPolicy(context.Background(), client, PolicyConfig{MaxStepDeg: 15, MaxSeconds: 5}, nil)
	if res.Stopped != "safety" {
		t.Fatalf("expected safety stop on big jump, got %+v", res)
	}
	if !c.isEStopped() {
		t.Error("oversized step must latch e-stop")
	}
}

func TestRunPolicyStaleServer(t *testing.T) {
	c := newFakeController()
	// point at a dead port; short timeout = watchdog fires fast
	client := NewPolicyClient("http://127.0.0.1:1", "", 300*time.Millisecond)
	res := c.RunPolicy(context.Background(), client, PolicyConfig{MaxSeconds: 3}, nil)
	if res.Stopped != "policy_error" {
		t.Fatalf("expected policy_error on unreachable server, got %+v", res)
	}
}

func TestRunPolicyStopFunc(t *testing.T) {
	srv := policyServer(t, "reach")
	c := newFakeController()
	client := NewPolicyClient(srv.URL, "", 5*time.Second)
	res := c.RunPolicy(context.Background(), client, PolicyConfig{MaxStepDeg: 15, MaxSeconds: 5}, func() bool { return true })
	if res.Stopped != "stopped" {
		t.Fatalf("expected stopped, got %+v", res)
	}
}

func TestSafetyGate(t *testing.T) {
	info := ArmInfo{Joints: []JointSpec{{Name: "J1", Min: -90, Max: 90, Unit: "deg"}}}
	info.Normalize()
	g := NewSafetyGate(info, 20)
	if err := g.Check(map[string]float64{"j1": 0}, map[string]float64{"J1": 10}); err != nil {
		t.Errorf("in-range small step should pass: %v", err)
	}
	if err := g.Check(map[string]float64{"j1": 0}, map[string]float64{"J1": 200}); err == nil {
		t.Error("out-of-range must fail")
	}
	if err := g.Check(map[string]float64{"j1": 0}, map[string]float64{"J1": 50}); err == nil {
		t.Error("oversized jump must fail")
	}
	if err := g.Check(map[string]float64{"j1": 0}, map[string]float64{"J9": 5}); err == nil {
		t.Error("unknown joint must fail")
	}
}
