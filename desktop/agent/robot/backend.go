package robot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Backend is the motion executor. Implementations must M400-gate internally
// where noted so the orchestration in control.go can trust a fresh Position().
type Backend interface {
	Name() string
	Status(ctx context.Context) (Status, error)
	Home(ctx context.Context, axes string) error
	Jog(ctx context.Context, axis string, dist float64, feed int) error
	Move(ctx context.Context, x, y, z *float64, feed int) error
	Tool(ctx context.Context, on bool) error
	// WaitMoves blocks until all queued motion completes (Marlin M400).
	WaitMoves(ctx context.Context) error
	// Position returns a FRESH readback (M114), not a cached value.
	Position(ctx context.Context) (Position, error)
	EStop(ctx context.Context) error
	// Raw sends an arbitrary G-code line (M42 GPIO, G1 E rotation, M302, …) and
	// waits for ok. Powers motor-rotation + GPIO control of the end-effector.
	Raw(ctx context.Context, line string) error
}

// BridgeBackend talks to the existing Python ender_ui server
// (docs/robot-protocol.md §5). Reuses the proven cell; swap for SerialBackend
// later without changing the protocol.
type BridgeBackend struct {
	Base   string // e.g. http://127.0.0.1:8330
	Client *http.Client
	// ToolMode selects how the end-effector is switched:
	//   "fan"  -> M106 S255 / M107 (screwdriver wired to the part-cooling FAN
	//             MOSFET, the klemens tool_trigger="fan" setup)
	//   "screw"-> M42 P<pin> via the bridge /api/screw (spare GPIO) [default]
	ToolMode string
}

func NewBridgeBackend(base string) *BridgeBackend {
	if base == "" {
		base = "http://127.0.0.1:8330"
	}
	return &BridgeBackend{
		Base:     strings.TrimRight(base, "/"),
		Client:   &http.Client{Timeout: 60 * time.Second},
		ToolMode: "screw",
	}
}

func (b *BridgeBackend) Name() string { return "ender_ui-bridge" }

func (b *BridgeBackend) post(ctx context.Context, path string, body any) (map[string]any, error) {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	} else {
		buf = []byte("{}")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", b.Base+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("bridge %s: http %d", path, resp.StatusCode)
	}
	// The bridge reports motion faults as {ok:false,error:"..."} with HTTP 200.
	if out != nil {
		if ok, found := out["ok"].(bool); found && !ok {
			msg, _ := out["error"].(string)
			return out, fmt.Errorf("bridge %s: %s", path, msg)
		}
	}
	return out, nil
}

func (b *BridgeBackend) get(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", b.Base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func (b *BridgeBackend) Home(ctx context.Context, axes string) error {
	_, err := b.post(ctx, "/api/home", map[string]any{"axes": axes})
	return err
}

func (b *BridgeBackend) Jog(ctx context.Context, axis string, dist float64, feed int) error {
	p := map[string]any{"axis": axis, "dist": dist}
	if feed > 0 {
		p["feed"] = feed
	}
	_, err := b.post(ctx, "/api/jog", p)
	return err
}

func (b *BridgeBackend) Move(ctx context.Context, x, y, z *float64, feed int) error {
	p := map[string]any{}
	if x != nil {
		p["x"] = *x
	}
	if y != nil {
		p["y"] = *y
	}
	if z != nil {
		p["z"] = *z
	}
	if feed > 0 {
		p["feed"] = feed
	}
	_, err := b.post(ctx, "/api/move", p)
	return err
}

func (b *BridgeBackend) Tool(ctx context.Context, on bool) error {
	// Fan-port screwdriver → M106 S255 / M107 (the bridge's /api/fan). Spare-pin
	// screwdriver → M42 via /api/screw.
	if b.ToolMode == "fan" {
		_, err := b.post(ctx, "/api/fan", map[string]any{"on": on})
		return err
	}
	_, err := b.post(ctx, "/api/screw", map[string]any{"on": on})
	return err
}

func (b *BridgeBackend) WaitMoves(ctx context.Context) error {
	_, err := b.post(ctx, "/api/gcode", map[string]any{"line": "M400"})
	return err
}

var m114re = regexp.MustCompile(`X:(-?\d+\.?\d*)\s+Y:(-?\d+\.?\d*)\s+Z:(-?\d+\.?\d*)`)

// Position issues a fresh M114 (NOT /api/status, whose cache lags while the
// bridge holds the move lock — verified live 2026-06-06) and parses the reply.
func (b *BridgeBackend) Position(ctx context.Context) (Position, error) {
	out, err := b.post(ctx, "/api/gcode", map[string]any{"line": "M114"})
	if err != nil {
		return Position{}, err
	}
	reply, _ := out["reply"].(string)
	m := m114re.FindStringSubmatch(reply)
	if m == nil {
		return Position{}, fmt.Errorf("could not parse M114: %q", reply)
	}
	x, _ := strconv.ParseFloat(m[1], 64)
	y, _ := strconv.ParseFloat(m[2], 64)
	z, _ := strconv.ParseFloat(m[3], 64)
	pos := Position{X: x, Y: y, Z: z, Homed: true}
	return pos, nil
}

func (b *BridgeBackend) EStop(ctx context.Context) error {
	_, err := b.post(ctx, "/api/estop", nil)
	return err
}

func (b *BridgeBackend) Raw(ctx context.Context, line string) error {
	_, err := b.post(ctx, "/api/gcode", map[string]any{"line": line})
	return err
}

func (b *BridgeBackend) Status(ctx context.Context) (Status, error) {
	out, err := b.get(ctx, "/api/status")
	if err != nil {
		return Status{Backend: b.Name()}, err
	}
	st := Status{OK: true, Backend: b.Name()}
	state, _ := out["state"].(map[string]any)
	if state != nil {
		st.Connected, _ = state["connected"].(bool)
		st.Tool, _ = state["screw"].(string)
		if homed, ok := state["homed"].(bool); ok {
			if pm, ok := state["position"].(map[string]any); ok {
				p := Position{Homed: homed}
				p.X, _ = pm["x"].(float64)
				p.Y, _ = pm["y"].(float64)
				p.Z, _ = pm["z"].(float64)
				st.Position = &p
			}
		}
	}
	return st, nil
}
