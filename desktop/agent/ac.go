package main

// ac.go — WiFi air-conditioner engine (docs/yaver-single-kumanda.md §5 ladder).
// LOCAL control first (no vendor cloud): Tuya-local via tinytuya (most common on
// cheap WiFi ACs), Gree-local best-effort. Mirrors the IR engine (ir.go): a
// supervised python sidecar reached over 127.0.0.1. AC device records +
// credentials (Tuya local key) live in the VAULT, never Convex.
//
// AC is stateful (a command carries the whole target state), so it's its own
// verb family (ac_set / ac_status), not a logical-key device. If the AC has
// WiFi this path beats IR — it's bidirectional (real state + verify for free).

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

//go:embed ac/yaver_ac_bridge.py
var acBridgePy []byte

const (
	acBridgePort   = 17647 // 127.0.0.1 only
	acVaultProject = "ac_devices"
)

// acDevice is the vault-stored record for a WiFi AC. Kind is "tuya" or "gree".
// LocalKey (Tuya) is a secret — it only ever lives in the vault and is passed
// to the local sidecar per call.
type acDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	DevID    string `json:"devid,omitempty"`
	LocalKey string `json:"localkey,omitempty"`
	Version  string `json:"version,omitempty"`
}

type acEngine struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	started bool
	client  *http.Client
}

var acEng = &acEngine{client: &http.Client{Timeout: 25 * time.Second}}

func acDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver", "ac")
	return dir, os.MkdirAll(dir, 0o755)
}

func acBridgePath() (string, error) {
	dir, err := acDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(acBridgePy)
	want := hex.EncodeToString(sum[:8])
	path := filepath.Join(dir, "yaver_ac_bridge.py")
	if cur, err := os.ReadFile(path); err == nil {
		curSum := sha256.Sum256(cur)
		if hex.EncodeToString(curSum[:8]) == want {
			return path, nil
		}
	}
	if err := os.WriteFile(path, acBridgePy, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (e *acEngine) baseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", acBridgePort) }

func (e *acEngine) health(ctx context.Context) (bool, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.baseURL()+"/healthz", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var h struct {
		OK       bool `json:"ok"`
		Tinytuya bool `json:"tinytuya"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	return h.OK, h.Tinytuya
}

func (e *acEngine) ensureBridge(ctx context.Context) error {
	if reach, tt := e.health(ctx); reach {
		if !tt {
			return fmt.Errorf("tinytuya not installed — run `pip3 install tinytuya`")
		}
		return nil
	}
	e.mu.Lock()
	if !(e.started && e.cmd != nil) {
		py := findPython()
		if py == "" {
			e.mu.Unlock()
			return fmt.Errorf("python3 not found — install Python 3 to use AC control")
		}
		script, err := acBridgePath()
		if err != nil {
			e.mu.Unlock()
			return fmt.Errorf("extract ac bridge: %w", err)
		}
		bctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(bctx, py, script, "--port", fmt.Sprintf("%d", acBridgePort))
		if err := cmd.Start(); err != nil {
			cancel()
			e.mu.Unlock()
			return fmt.Errorf("start ac bridge: %w", err)
		}
		e.cmd = cmd
		e.cancel = cancel
		e.started = true
		go func() { _ = cmd.Wait(); e.mu.Lock(); e.started = false; e.cmd = nil; e.mu.Unlock() }()
	}
	e.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if reach, tt := e.health(ctx); reach {
			if !tt {
				return fmt.Errorf("tinytuya not installed — run `pip3 install tinytuya`")
			}
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("ac bridge did not become ready within 10s")
}

func (e *acEngine) call(ctx context.Context, path string, req interface{}) (map[string]interface{}, error) {
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
		msg := "ac bridge error"
		if out != nil {
			if e, ok := out["error"].(string); ok && e != "" {
				msg = e
			}
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

func (e *acEngine) reqFor(d acDevice, extra map[string]interface{}) map[string]interface{} {
	m := map[string]interface{}{
		"kind": d.Kind, "host": d.Host, "devid": d.DevID,
		"localkey": d.LocalKey, "version": d.Version,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// Set applies a target state {power,mode,temp,fan,swing} to the AC.
func (e *acEngine) Set(ctx context.Context, d acDevice, state map[string]interface{}) (map[string]interface{}, error) {
	return e.call(ctx, "/set", e.reqFor(d, map[string]interface{}{"state": state}))
}

// Status reads the AC's current state.
func (e *acEngine) Status(ctx context.Context, d acDevice) (map[string]interface{}, error) {
	return e.call(ctx, "/status", e.reqFor(d, nil))
}

// ── vault-backed device store ────────────────────────────────────────────────

func acSaveDevice(d acDevice) error {
	vs, err := openVaultOptional()
	if err != nil {
		return fmt.Errorf("vault unavailable: %w", err)
	}
	b, _ := json.Marshal(d)
	return vs.Set(VaultEntry{
		Project:  acVaultProject,
		Name:     d.ID,
		Category: "custom",
		Value:    string(b),
		Notes:    "WiFi AC — " + d.Name,
	})
}

func acGetDevice(id string) (acDevice, bool) {
	vs, err := openVaultOptional()
	if err != nil {
		return acDevice{}, false
	}
	e, gerr := vs.Get(acVaultProject, id)
	if gerr != nil || e == nil || e.Value == "" {
		return acDevice{}, false
	}
	var d acDevice
	if json.Unmarshal([]byte(e.Value), &d) != nil {
		return acDevice{}, false
	}
	return d, true
}

// acListDevices returns the registered ACs WITHOUT secrets (no local key).
func acListDevices() []acDevice {
	vs, err := openVaultOptional()
	if err != nil {
		return nil
	}
	var out []acDevice
	for _, sum := range vs.List(acVaultProject) {
		d, ok := acGetDevice(sum.Name)
		if !ok {
			continue
		}
		d.LocalKey = "" // never expose the secret in a list
		out = append(out, d)
	}
	return out
}
