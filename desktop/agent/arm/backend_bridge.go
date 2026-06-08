package arm

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

// BridgeArmBackend speaks a simple JSON-over-HTTP interface to a local "arm
// bridge" server that owns the real robot driver. This is the same proven
// pattern the Cartesian cell uses (robot.BridgeBackend → ender_ui): a thin
// process next to the hardware exposes /describe /state /movej /movel /enable
// /stop, and Yaver drives THAT. It is the correct integration for robots whose
// official control stack is a library/daemon we should reuse rather than
// reimplement — e.g. PAROL6 (driver "parol6" → scripts/parol6_bridge.py, which
// wraps the official PAROL6 headless_commander UDP client).
//
// Bridge contract (all JSON; {ok:false,error} on failure):
//
//	GET  /describe -> ArmInfo
//	GET  /state    -> {joints:[{name,position,unit}], pose?:{x,y,z,roll,pitch,yaw}}
//	POST /enable   {on:bool}
//	POST /movej    {targets:{<joint>:deg}, velPct, accPct}
//	POST /movel    {pose:{x,y,z,roll,pitch,yaw}, velPct, accPct}
//	POST /home     {velPct, accPct}
//	POST /stop     {}
//	POST /estop    {}
type BridgeArmBackend struct {
	base   string
	cfg    Config
	info   ArmInfo
	client *http.Client
}

func NewBridgeArmBackend(cfg Config, defaultBase string) *BridgeArmBackend {
	base := strings.TrimSpace(cfg.Addr)
	if base == "" {
		base = defaultBase
	}
	if base != "" && !strings.HasPrefix(base, "http") {
		base = "http://" + base
	}
	info := cfg.Info
	info.Normalize()
	return &BridgeArmBackend{
		base:   strings.TrimRight(base, "/"),
		cfg:    cfg,
		info:   info,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (b *BridgeArmBackend) Name() string { return "bridge" }

func (b *BridgeArmBackend) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("arm bridge %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("arm bridge %s %s: http %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	// surface {ok:false,error}
	var probe struct {
		OK    *bool  `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.OK != nil && !*probe.OK {
		return fmt.Errorf("arm bridge %s: %s", path, probe.Error)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (b *BridgeArmBackend) Connect(ctx context.Context) error {
	if b.base == "" {
		return fmt.Errorf("arm bridge: no addr (set the bridge base URL / host:port)")
	}
	return b.do(ctx, "GET", "/describe", nil, nil)
}

func (b *BridgeArmBackend) Close() error { return nil }

func (b *BridgeArmBackend) Describe(ctx context.Context) (ArmInfo, error) {
	var info ArmInfo
	if err := b.do(ctx, "GET", "/describe", nil, &info); err != nil || len(info.Joints) == 0 {
		info = b.info
		if info.Source == "" {
			info.Source = "config"
		}
	} else {
		info.Source = "robot"
	}
	info.Normalize()
	b.info = info
	return info, nil
}

type bridgeState struct {
	Joints []JointState `json:"joints"`
	Pose   *Pose        `json:"pose"`
}

func (b *BridgeArmBackend) Status(ctx context.Context) (ArmStatus, error) {
	var s bridgeState
	if err := b.do(ctx, "GET", "/state", nil, &s); err != nil {
		return ArmStatus{Backend: b.Name(), Error: err.Error()}, err
	}
	return ArmStatus{OK: true, Backend: b.Name(), Connected: true, Enabled: true, Joints: s.Joints, Pose: s.Pose}, nil
}

func (b *BridgeArmBackend) Enable(ctx context.Context, on bool) error {
	return b.do(ctx, "POST", "/enable", map[string]any{"on": on}, nil)
}

func (b *BridgeArmBackend) JointState(ctx context.Context) ([]JointState, error) {
	var s bridgeState
	if err := b.do(ctx, "GET", "/state", nil, &s); err != nil {
		return nil, err
	}
	return s.Joints, nil
}

func (b *BridgeArmBackend) Pose(ctx context.Context) (Pose, error) {
	var s bridgeState
	if err := b.do(ctx, "GET", "/state", nil, &s); err != nil {
		return Pose{}, err
	}
	if s.Pose == nil {
		return Pose{}, ErrNoCartesian
	}
	return *s.Pose, nil
}

func (b *BridgeArmBackend) MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int) error {
	return b.do(ctx, "POST", "/movej", map[string]any{"targets": targets, "velPct": velPct, "accPct": accPct}, nil)
}

func (b *BridgeArmBackend) MoveLinear(ctx context.Context, p Pose, velPct, accPct int) error {
	return b.do(ctx, "POST", "/movel", map[string]any{"pose": p, "velPct": velPct, "accPct": accPct}, nil)
}

// WaitIdle: the bridge's move endpoints block until motion completes (the shim
// owns Ruckig/streaming), so this is a no-op.
func (b *BridgeArmBackend) WaitIdle(ctx context.Context) error { return nil }

func (b *BridgeArmBackend) Stop(ctx context.Context) error {
	return b.do(ctx, "POST", "/stop", map[string]any{}, nil)
}

func (b *BridgeArmBackend) EStop(ctx context.Context) error {
	return b.do(ctx, "POST", "/estop", map[string]any{}, nil)
}

func (b *BridgeArmBackend) FreeDrive(ctx context.Context, on bool) error {
	return b.do(ctx, "POST", "/freedrive", map[string]any{"on": on}, nil)
}

func (b *BridgeArmBackend) Raw(ctx context.Context, cmd string) (string, error) {
	var out map[string]any
	err := b.do(ctx, "POST", "/raw", map[string]any{"cmd": cmd}, &out)
	if err != nil {
		return "", err
	}
	buf, _ := json.Marshal(out)
	return string(buf), nil
}
