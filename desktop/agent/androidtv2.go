package main

// androidtv2.go — Android TV Remote v2 engine (Mi Box / Google TV) via the
// androidtvremote2 protocol (TLS 6466/6467 — what the Google TV phone app uses).
// More robust than ADB keyevent: no developer mode, survives reboots, real
// power. Mirrors the IR/AC sidecar pattern (supervised python sidecar over
// 127.0.0.1). Paired-device names live in the vault; the per-host cert/key are
// persisted on disk by the sidecar.
//
// In the home router an `androidtv` device routes its logical keys here (an
// alternative to the `mibox`/ADB transport).

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

//go:embed androidtv/yaver_atv2_bridge.py
var atv2BridgePy []byte

const (
	atv2BridgePort   = 17648 // 127.0.0.1 only
	atv2VaultProject = "androidtv"
)

type atv2Engine struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	started bool
	client  *http.Client
}

var atv2Eng = &atv2Engine{client: &http.Client{Timeout: 30 * time.Second}}

func atv2Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver", "androidtv")
	return dir, os.MkdirAll(dir, 0o755)
}

func atv2BridgePath() (string, error) {
	dir, err := atv2Dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(atv2BridgePy)
	want := hex.EncodeToString(sum[:8])
	path := filepath.Join(dir, "yaver_atv2_bridge.py")
	if cur, err := os.ReadFile(path); err == nil {
		curSum := sha256.Sum256(cur)
		if hex.EncodeToString(curSum[:8]) == want {
			return path, nil
		}
	}
	if err := os.WriteFile(path, atv2BridgePy, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (e *atv2Engine) baseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", atv2BridgePort) }

func (e *atv2Engine) health(ctx context.Context) (bool, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.baseURL()+"/healthz", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var h struct {
		OK   bool `json:"ok"`
		ATV2 bool `json:"atv2"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	return h.OK, h.ATV2
}

func (e *atv2Engine) ensureBridge(ctx context.Context) error {
	if reach, ok := e.health(ctx); reach {
		if !ok {
			return fmt.Errorf("androidtvremote2 not installed — run `pip3 install androidtvremote2`")
		}
		return nil
	}
	e.mu.Lock()
	if !(e.started && e.cmd != nil) {
		py := findPython()
		if py == "" {
			e.mu.Unlock()
			return fmt.Errorf("python3 not found — install Python 3 to use Android TV control")
		}
		script, err := atv2BridgePath()
		if err != nil {
			e.mu.Unlock()
			return fmt.Errorf("extract androidtv bridge: %w", err)
		}
		bctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(bctx, py, script, "--port", fmt.Sprintf("%d", atv2BridgePort))
		if err := cmd.Start(); err != nil {
			cancel()
			e.mu.Unlock()
			return fmt.Errorf("start androidtv bridge: %w", err)
		}
		e.cmd = cmd
		e.cancel = cancel
		e.started = true
		go func() { _ = cmd.Wait(); e.mu.Lock(); e.started = false; e.cmd = nil; e.mu.Unlock() }()
	}
	e.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if reach, ok := e.health(ctx); reach {
			if !ok {
				return fmt.Errorf("androidtvremote2 not installed — run `pip3 install androidtvremote2`")
			}
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("androidtv bridge did not become ready within 10s")
}

func (e *atv2Engine) call(ctx context.Context, path string, req interface{}) (map[string]interface{}, error) {
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
		msg := "androidtv bridge error"
		if out != nil {
			if e, ok := out["error"].(string); ok && e != "" {
				msg = e
			}
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

func (e *atv2Engine) PairBegin(ctx context.Context, host string) error {
	_, err := e.call(ctx, "/pair_begin", map[string]interface{}{"host": host})
	return err
}

func (e *atv2Engine) PairFinish(ctx context.Context, host, code string) error {
	_, err := e.call(ctx, "/pair_finish", map[string]interface{}{"host": host, "code": code})
	return err
}

func (e *atv2Engine) Key(ctx context.Context, host, key string) error {
	_, err := e.call(ctx, "/key", map[string]interface{}{"host": host, "key": key})
	return err
}

func (e *atv2Engine) Launch(ctx context.Context, host, app string) error {
	_, err := e.call(ctx, "/launch", map[string]interface{}{"host": host, "app": app})
	return err
}

// atv2KeyName maps a canonical logical key to the androidtvremote2 key name.
func atv2KeyName(key string) (string, bool) {
	m := map[string]string{
		"up": "DPAD_UP", "down": "DPAD_DOWN", "left": "DPAD_LEFT", "right": "DPAD_RIGHT",
		"ok": "DPAD_CENTER", "select": "DPAD_CENTER",
		"back": "BACK", "home": "HOME", "menu": "MENU",
		"play": "MEDIA_PLAY", "pause": "MEDIA_PAUSE", "play_pause": "MEDIA_PLAY_PAUSE",
		"stop": "MEDIA_STOP", "next": "MEDIA_NEXT", "previous": "MEDIA_PREVIOUS",
		"vol_up": "VOLUME_UP", "vol_down": "VOLUME_DOWN", "mute": "MUTE",
		"power": "POWER", "power_on": "POWER", "power_off": "POWER",
		"channel_up": "CHANNEL_UP", "channel_down": "CHANNEL_DOWN",
		"0": "0", "1": "1", "2": "2", "3": "3", "4": "4", "5": "5", "6": "6", "7": "7", "8": "8", "9": "9",
	}
	v, ok := m[key]
	return v, ok
}

// ── vault-backed paired-device store (host + name) ───────────────────────────

type atv2Device struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Host string `json:"host"`
}

func atv2SaveDevice(d atv2Device) error {
	vs, err := openVaultOptional()
	if err != nil {
		return fmt.Errorf("vault unavailable: %w", err)
	}
	b, _ := json.Marshal(d)
	return vs.Set(VaultEntry{
		Project:  atv2VaultProject,
		Name:     d.ID,
		Category: "custom",
		Value:    string(b),
		Notes:    "Android TV — " + d.Name,
	})
}

func atv2ListDevices() []atv2Device {
	vs, err := openVaultOptional()
	if err != nil {
		return nil
	}
	var out []atv2Device
	for _, sum := range vs.List(atv2VaultProject) {
		e, gerr := vs.Get(atv2VaultProject, sum.Name)
		if gerr != nil || e == nil || e.Value == "" {
			continue
		}
		var d atv2Device
		if json.Unmarshal([]byte(e.Value), &d) == nil {
			out = append(out, d)
		}
	}
	return out
}
