package main

// appletv.go — Apple TV control engine. Drives an Apple TV over the LAN via
// pyatv (MRP/AirPlay/Companion — NO HDMI, NO IR). The agent embeds a small
// Python sidecar (appletv/yaver_atv_bridge.py), extracts it to ~/.yaver/appletv,
// and supervises it as a local-HTTP service on 127.0.0.1 — the same pattern the
// arm sim harness uses (arm/backend_sim.go). The sidecar is stateless; THIS file
// owns pairing credentials and keeps them in the encrypted vault (project
// "appletv"), never plaintext, never Convex.
//
// Surfaces (CLI / ops verbs / first-class MCP tools) all call into the engine
// methods here — one engine, three front doors.
//
// NON-GOAL (enforced, see docs/yaver-appletv-remote-control.md §9): no HDMI
// capture of the Apple TV, no HDCP workaround, no CarPlay video. Control +
// metadata only. The capture-card video path (capture.go) is a SEPARATE
// capability for the user's own non-protected sources.

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed appletv/yaver_atv_bridge.py
var atvBridgePy []byte

const (
	appletvVaultProject = "appletv"
	atvBridgePort       = 17645 // 127.0.0.1 only
)

type appleTVEngine struct {
	mu       sync.Mutex
	python   string
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	started  bool
	client   *http.Client
	pyatvOK  bool
	pyatvErr string
}

var appleTVEng = &appleTVEngine{client: &http.Client{Timeout: 30 * time.Second}}

// appletvDevice is the vault-stored record for a paired Apple TV.
type appletvDevice struct {
	Identifier  string            `json:"identifier"`
	Name        string            `json:"name"`
	Address     string            `json:"address"`
	Credentials map[string]string `json:"credentials"`
	Default     bool              `json:"default,omitempty"`
}

func atvDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver", "appletv")
	return dir, os.MkdirAll(dir, 0o755)
}

// bridgePath extracts the embedded sidecar, refreshing only when content changes
// (a binary upgrade ships a new bridge automatically).
func bridgePath() (string, error) {
	dir, err := atvDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(atvBridgePy)
	want := hex.EncodeToString(sum[:8])
	path := filepath.Join(dir, "yaver_atv_bridge.py")
	if cur, err := os.ReadFile(path); err == nil {
		curSum := sha256.Sum256(cur)
		if hex.EncodeToString(curSum[:8]) == want {
			return path, nil
		}
	}
	if err := os.WriteFile(path, atvBridgePy, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// pyatvImportable reports whether `import pyatv` succeeds under the given
// interpreter — used by `yaver doctor` to give an actionable install hint.
func pyatvImportable(python string) bool {
	if python == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, python, "-c", "import pyatv").Run() == nil
}

func findPython() string {
	for _, c := range []string{"python3", "python"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func (e *appleTVEngine) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", atvBridgePort)
}

// health pings the sidecar; returns (reachable, pyatvInstalled).
func (e *appleTVEngine) health(ctx context.Context) (bool, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.baseURL()+"/healthz", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var h struct {
		OK    bool   `json:"ok"`
		Pyatv bool   `json:"pyatv"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	e.mu.Lock()
	e.pyatvOK = h.Pyatv
	e.pyatvErr = h.Error
	e.mu.Unlock()
	return h.OK, h.Pyatv
}

// ensureBridge starts (or reuses) the sidecar and verifies pyatv is importable.
func (e *appleTVEngine) ensureBridge(ctx context.Context) error {
	// Reuse a running, healthy bridge (agent restart, manual launch).
	if reach, py := e.health(ctx); reach {
		if !py {
			return fmt.Errorf("pyatv not installed on this host — run `pip3 install pyatv` (see `yaver doctor`)")
		}
		return nil
	}

	// Critical section: start the process if we haven't. We do NOT hold the lock
	// during the readiness poll below — health() locks e.mu and the mutex isn't
	// reentrant.
	e.mu.Lock()
	needStart := !(e.started && e.cmd != nil)
	if needStart {
		py := findPython()
		if py == "" {
			e.mu.Unlock()
			return fmt.Errorf("python3 not found — install Python 3 to use Apple TV control")
		}
		script, err := bridgePath()
		if err != nil {
			e.mu.Unlock()
			return fmt.Errorf("extract bridge: %w", err)
		}
		bctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(bctx, py, script, "--port", fmt.Sprintf("%d", atvBridgePort))
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			cancel()
			e.mu.Unlock()
			return fmt.Errorf("start pyatv bridge: %w", err)
		}
		e.python = py
		e.cmd = cmd
		e.cancel = cancel
		e.started = true
		go func() { _ = cmd.Wait(); e.mu.Lock(); e.started = false; e.cmd = nil; e.mu.Unlock() }()
	}
	e.mu.Unlock()

	// Wait for readiness (~10s), like models.go::Serve.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		reach, py := e.health(ctx)
		if reach {
			if !py {
				return fmt.Errorf("pyatv not installed — run `pip3 install pyatv`")
			}
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("pyatv bridge did not become ready within 10s")
}

// call POSTs a JSON request to the sidecar and decodes the JSON response.
func (e *appleTVEngine) call(ctx context.Context, path string, req interface{}) (map[string]interface{}, error) {
	if err := e.ensureBridge(ctx); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", e.baseURL()+path, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if resp.StatusCode >= 400 {
		msg := "bridge error"
		if out != nil {
			if e, ok := out["error"].(string); ok {
				msg = e
			}
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

func (e *appleTVEngine) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.started = false
	e.cmd = nil
}

// ── vault-backed device store ────────────────────────────────────────────────

func appletvSaveDevice(d appletvDevice) error {
	vs, err := openVaultOptional()
	if err != nil {
		return fmt.Errorf("vault unavailable: %w", err)
	}
	b, _ := json.Marshal(d)
	return vs.Set(VaultEntry{
		Project:  appletvVaultProject,
		Name:     d.Identifier,
		Category: "custom",
		Value:    string(b),
		Notes:    "Apple TV pairing — " + d.Name,
	})
}

func appletvListDevices() ([]appletvDevice, error) {
	vs, err := openVaultOptional()
	if err != nil {
		return nil, err
	}
	out := []appletvDevice{}
	for _, sum := range vs.List(appletvVaultProject) {
		e, gerr := vs.Get(appletvVaultProject, sum.Name)
		if gerr != nil || e == nil || e.Value == "" {
			continue
		}
		var d appletvDevice
		if json.Unmarshal([]byte(e.Value), &d) == nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// appletvResolve picks a paired device by identifier; "" or "default" selects the
// default (or the only one).
func appletvResolve(ref string) (appletvDevice, error) {
	devs, err := appletvListDevices()
	if err != nil {
		return appletvDevice{}, err
	}
	if len(devs) == 0 {
		return appletvDevice{}, fmt.Errorf("no paired Apple TV — run `yaver appletv pair <id>` first")
	}
	ref = strings.TrimSpace(ref)
	if ref != "" && ref != "default" {
		for _, d := range devs {
			if d.Identifier == ref || strings.EqualFold(d.Name, ref) {
				return d, nil
			}
		}
		return appletvDevice{}, fmt.Errorf("no paired Apple TV matching %q", ref)
	}
	for _, d := range devs {
		if d.Default {
			return d, nil
		}
	}
	return devs[0], nil
}

// ── high-level operations (used by ops verbs, CLI, and first-class tools) ─────

func (e *appleTVEngine) Scan(ctx context.Context) (map[string]interface{}, error) {
	return e.call(ctx, "/scan", map[string]interface{}{"timeout": 5})
}

func (e *appleTVEngine) controlCall(ctx context.Context, path, ref string, extra map[string]interface{}) (map[string]interface{}, error) {
	d, err := appletvResolve(ref)
	if err != nil {
		return nil, err
	}
	req := map[string]interface{}{"address": d.Address, "credentials": d.Credentials}
	for k, v := range extra {
		req[k] = v
	}
	return e.call(ctx, path, req)
}

func (e *appleTVEngine) RemoteKey(ctx context.Context, ref, key string) (map[string]interface{}, error) {
	return e.controlCall(ctx, "/remote_key", ref, map[string]interface{}{"key": key})
}

func (e *appleTVEngine) Transport(ctx context.Context, ref, action string) (map[string]interface{}, error) {
	return e.controlCall(ctx, "/transport", ref, map[string]interface{}{"action": action})
}

func (e *appleTVEngine) Power(ctx context.Context, ref, state string) (map[string]interface{}, error) {
	return e.controlCall(ctx, "/power", ref, map[string]interface{}{"state": state})
}

func (e *appleTVEngine) Seek(ctx context.Context, ref string, seconds int) (map[string]interface{}, error) {
	return e.controlCall(ctx, "/seek", ref, map[string]interface{}{"seconds": seconds})
}

func (e *appleTVEngine) LaunchApp(ctx context.Context, ref, bundleID string) (map[string]interface{}, error) {
	return e.controlCall(ctx, "/launch_app", ref, map[string]interface{}{"bundle_id": bundleID})
}

func (e *appleTVEngine) NowPlaying(ctx context.Context, ref string) (map[string]interface{}, error) {
	return e.controlCall(ctx, "/now_playing", ref, nil)
}

// nowPlayingArtworkDataURL fetches now-playing and returns (metadata, data:URL or "").
func (e *appleTVEngine) nowPlayingArtworkDataURL(ctx context.Context, ref string) (map[string]interface{}, string) {
	np, err := e.NowPlaying(ctx, ref)
	if err != nil || np == nil {
		return map[string]interface{}{"error": fmt.Sprintf("%v", err)}, ""
	}
	b64, _ := np["artwork_b64"].(string)
	if b64 == "" {
		return np, ""
	}
	mt, _ := np["mimetype"].(string)
	if mt == "" {
		mt = "image/jpeg"
	}
	// Validate it decodes (cheap guard).
	if _, derr := base64.StdEncoding.DecodeString(b64); derr != nil {
		return np, ""
	}
	return np, "data:" + mt + ";base64," + b64
}

// appletvKnownKeys is the accepted remote-key vocabulary (mirrors the bridge).
var appletvKnownKeys = map[string]bool{
	"up": true, "down": true, "left": true, "right": true, "select": true,
	"menu": true, "home": true, "play": true, "pause": true, "stop": true,
	"next": true, "previous": true, "play_pause": true,
	"volume_up": true, "volume_down": true,
}

func appletvValidKey(k string) bool { return appletvKnownKeys[strings.ToLower(strings.TrimSpace(k))] }

// handleAppleTVNowPlayingStream pushes now-playing metadata deltas as SSE so a
// phone / car / glass surface can render a live now-playing card. Artwork is
// sent as a data: URL inline (small, 400px). Polls every 2s; only emits on
// change. ?device=<identifier|name> targets a non-default TV.
func (s *HTTPServer) handleAppleTVNowPlayingStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	device := r.URL.Query().Get("device")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	last := ""
	emit := func() {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		np, artURL := appleTVEng.nowPlayingArtworkDataURL(ctx, device)
		if artURL != "" {
			np["artwork"] = artURL
		}
		delete(np, "artwork_b64")
		b, _ := json.Marshal(np)
		if string(b) == last {
			return
		}
		last = string(b)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	emit()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			emit()
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
