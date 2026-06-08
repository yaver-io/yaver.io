package arm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/yaver-io/agent/robot"
)

// TestSimPolicyE2E runs the WHOLE video→policy→arm spine for real on this machine:
// the real SimBackend spawns the kinematic harness (no pybullet/GPU needed), the
// reference policy server stands in for a rented-GPU model, and RunPolicy drives
// the sim arm through the safety gate while rendering frames via HTTPCamera.
//
// Gated behind SIM_E2E=1 (it spawns python processes + needs Pillow for frames),
// so the normal `go test ./arm/` stays fast and dependency-free.
//
//	SIM_E2E=1 go test ./arm/ -run TestSimPolicyE2E -v
func TestSimPolicyE2E(t *testing.T) {
	if os.Getenv("SIM_E2E") == "" {
		t.Skip("set SIM_E2E=1 to run the live sim+policy end-to-end (spawns python)")
	}
	py := "python3"
	if _, err := exec.LookPath(py); err != nil {
		t.Skipf("no %s on PATH", py)
	}
	simPort, polPort := freePort(t), freePort(t)

	// 1) reference policy server: drive J1→30, J2→-20
	pol := exec.Command(py, "yaver_policy_server.py", "--port", itoaT(polPort),
		"--goal", `{"J1":30,"J2":-20}`, "--step", "8")
	pol.Stderr = os.Stderr
	if err := pol.Start(); err != nil {
		t.Fatalf("start policy server: %v", err)
	}
	defer func() { _ = pol.Process.Kill(); _ = pol.Wait() }()
	waitHealthy(t, fmt.Sprintf("http://127.0.0.1:%d/healthz", polPort), 10*time.Second)

	// 2) real SimBackend (kinematic engine) — spawns the embedded harness
	cfg := Config{Driver: "sim"}
	cfg.Sim.Engine = "kinematic"
	cfg.Sim.Model = "builtin:arm6"
	cfg.Sim.Port = simPort
	cfg.Normalize()
	sb := NewSimBackend(cfg)
	ctx := context.Background()
	if err := sb.Connect(ctx); err != nil {
		t.Fatalf("sim connect: %v", err)
	}
	defer sb.Close()

	// 3) controller with the sim's rendered frames as the camera
	cam := robot.NewHTTPCamera(sb.FrameURL())
	if _, err := cam.Grab(ctx); err != nil {
		t.Fatalf("camera grab (sim render): %v", err)
	}
	ctrl := NewController(sb, cam, robot.VisionConfig{}, cfg)

	info, err := ctrl.Describe(ctx)
	if err != nil || info.DOF != 6 {
		t.Fatalf("describe: dof=%d err=%v", info.DOF, err)
	}
	t.Logf("sim arm: %s, %d DOF", info.Model, info.DOF)

	// 4) run the served policy through the safety gate
	client := NewPolicyClient(fmt.Sprintf("http://127.0.0.1:%d", polPort), "", 10*time.Second)
	if err := client.Health(ctx); err != nil {
		t.Fatalf("policy health: %v", err)
	}
	res := ctrl.RunPolicy(ctx, client, PolicyConfig{MaxStepDeg: 15, MaxSeconds: 30}, nil)
	t.Logf("RunPolicy: %+v", res)
	if !res.OK || !res.Done {
		t.Fatalf("expected the policy to complete (done), got %+v", res)
	}
	if res.Steps == 0 {
		t.Fatal("no steps executed")
	}

	// 5) the sim arm actually reached the commanded goal
	js, _, err := ctrl.State(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	pos := map[string]float64{}
	for _, j := range js {
		pos[j.Name] = j.Position
	}
	if d := pos["J1"] - 30; d > 1.5 || d < -1.5 {
		t.Errorf("J1=%.2f, expected ~30", pos["J1"])
	}
	if d := pos["J2"] - (-20); d > 1.5 || d < -1.5 {
		t.Errorf("J2=%.2f, expected ~-20", pos["J2"])
	}
	t.Logf("final joints: J1=%.2f J2=%.2f (goal 30/-20) — reached via %d safety-gated steps", pos["J1"], pos["J2"], res.Steps)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitHealthy(t *testing.T, url string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("server at %s never became healthy", url)
}

func itoaT(n int) string { return fmt.Sprintf("%d", n) }
