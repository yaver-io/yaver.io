package main

// ir.go — IR/RF learn+blast engine for the "single kumanda" home surface
// (docs/yaver-single-kumanda.md §6). Mirrors the pyatv engine (appletv.go): a
// supervised python sidecar (python-broadlink) reached over 127.0.0.1 JSON.
// Learned codes are stored in the VAULT (local, never Convex), keyed by the
// home-device id + logical key, so an `ir` device in the home store routes its
// keys to the right captured frame.
//
// IR is line-of-sight in the user's OWN room, for the user's OWN gear — benign,
// content-agnostic, no mains, no third party. The phone cannot learn IR (no
// receiver); the learner is this Broadlink-class device on the LAN.

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

//go:embed ir/yaver_ir_bridge.py
var irBridgePy []byte

const (
	irBridgePort    = 17646 // 127.0.0.1 only
	irVaultProject  = "ir_codes"
	irLearnTimeoutS = 30
)

type irEngine struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	started bool
	client  *http.Client
}

var irEng = &irEngine{client: &http.Client{Timeout: 40 * time.Second}}

func irDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver", "ir")
	return dir, os.MkdirAll(dir, 0o755)
}

// irBridgePath extracts the embedded sidecar, refreshing on content change.
func irBridgePath() (string, error) {
	dir, err := irDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(irBridgePy)
	want := hex.EncodeToString(sum[:8])
	path := filepath.Join(dir, "yaver_ir_bridge.py")
	if cur, err := os.ReadFile(path); err == nil {
		curSum := sha256.Sum256(cur)
		if hex.EncodeToString(curSum[:8]) == want {
			return path, nil
		}
	}
	if err := os.WriteFile(path, irBridgePy, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (e *irEngine) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", irBridgePort)
}

// health pings the sidecar; returns (reachable, broadlinkInstalled).
func (e *irEngine) health(ctx context.Context) (bool, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.baseURL()+"/healthz", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var h struct {
		OK        bool `json:"ok"`
		Broadlink bool `json:"broadlink"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	return h.OK, h.Broadlink
}

func (e *irEngine) ensureBridge(ctx context.Context) error {
	if reach, bl := e.health(ctx); reach {
		if !bl {
			return fmt.Errorf("python-broadlink not installed — run `pip3 install broadlink`")
		}
		return nil
	}
	e.mu.Lock()
	if !(e.started && e.cmd != nil) {
		py := findPython()
		if py == "" {
			e.mu.Unlock()
			return fmt.Errorf("python3 not found — install Python 3 to use IR control")
		}
		script, err := irBridgePath()
		if err != nil {
			e.mu.Unlock()
			return fmt.Errorf("extract ir bridge: %w", err)
		}
		bctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(bctx, py, script, "--port", fmt.Sprintf("%d", irBridgePort))
		if err := cmd.Start(); err != nil {
			cancel()
			e.mu.Unlock()
			return fmt.Errorf("start ir bridge: %w", err)
		}
		e.cmd = cmd
		e.cancel = cancel
		e.started = true
		go func() { _ = cmd.Wait(); e.mu.Lock(); e.started = false; e.cmd = nil; e.mu.Unlock() }()
	}
	e.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if reach, bl := e.health(ctx); reach {
			if !bl {
				return fmt.Errorf("python-broadlink not installed — run `pip3 install broadlink`")
			}
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("ir bridge did not become ready within 10s")
}

func (e *irEngine) call(ctx context.Context, path string, req interface{}) (map[string]interface{}, error) {
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
		msg := "ir bridge error"
		if out != nil {
			if e, ok := out["error"].(string); ok && e != "" {
				msg = e
			}
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

// Scan discovers Broadlink learners/blasters on the LAN.
func (e *irEngine) Scan(ctx context.Context) (map[string]interface{}, error) {
	return e.call(ctx, "/scan", map[string]interface{}{})
}

// Learn captures one IR frame from the blaster at host (user presses a button
// on the real remote). Returns the base64 code.
func (e *irEngine) Learn(ctx context.Context, host string) (string, error) {
	out, err := e.call(ctx, "/learn", map[string]interface{}{"host": host, "timeout": irLearnTimeoutS})
	if err != nil {
		return "", err
	}
	code, _ := out["code"].(string)
	if code == "" {
		return "", fmt.Errorf("no code captured")
	}
	return code, nil
}

// Blast sends a base64 code through the blaster at host.
func (e *irEngine) Blast(ctx context.Context, host, code string) error {
	_, err := e.call(ctx, "/blast", map[string]interface{}{"host": host, "code": code})
	return err
}

// ── vault-backed code store (per home-device id + logical key) ───────────────

func irCodeName(deviceID, key string) string { return deviceID + "/" + key }

func irSaveCode(deviceID, key, code string) error {
	vs, err := openVaultOptional()
	if err != nil {
		return fmt.Errorf("vault unavailable: %w", err)
	}
	return vs.Set(VaultEntry{
		Project:  irVaultProject,
		Name:     irCodeName(deviceID, key),
		Category: "custom",
		Value:    code,
		Notes:    "IR code — " + deviceID + " / " + key,
	})
}

func irGetCode(deviceID, key string) (string, bool) {
	vs, err := openVaultOptional()
	if err != nil {
		return "", false
	}
	e, gerr := vs.Get(irVaultProject, irCodeName(deviceID, key))
	if gerr != nil || e == nil || e.Value == "" {
		return "", false
	}
	return e.Value, true
}

// irListKeys returns the logical keys learned for a device.
func irListKeys(deviceID string) []string {
	vs, err := openVaultOptional()
	if err != nil {
		return nil
	}
	prefix := deviceID + "/"
	var keys []string
	for _, sum := range vs.List(irVaultProject) {
		if len(sum.Name) > len(prefix) && sum.Name[:len(prefix)] == prefix {
			keys = append(keys, sum.Name[len(prefix):])
		}
	}
	return keys
}
