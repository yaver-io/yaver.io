package main

// ssh_bootstrap.go — auto-fill ~/.ssh/authorized_keys on a remote yaver
// box using the same-Convex-user trust channel.
//
// Triggered from runSSHWrap when ssh fails with "Permission denied
// (publickey)" against a device that is registered under the caller's
// own Yaver account. Idempotent: repeat calls with the same key get a
// 200 + alreadyPresent=true.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sshBootstrapResult is the structured outcome of a bootstrap attempt.
// Lets runSSHWrap print one of three flavors:
//   - bootstrapped (push succeeded, retry ssh)
//   - already-present (no-op, retry ssh anyway — first ssh might have
//     failed for a transient reason)
//   - skipped (different owner, agent unreachable, no keygen) — caller
//     surfaces SkipReason and lets the original ssh error stand.
type sshBootstrapResult struct {
	Pushed         bool
	AlreadyPresent bool
	Fingerprint    string
	KeyType        string
	LocalPubKey    string
	RemoteName     string
	// RemoteOSUser is the OS user the remote agent is running as
	// (read from /info.osUser). The CLI should SSH as this user
	// when retrying — the appended pubkey lives in THIS user's
	// ~/.ssh/authorized_keys, not necessarily root's.
	RemoteOSUser string
	SkipReason   string // non-empty when bootstrap was deliberately not attempted
}

// sshBootstrapDevice runs the full bootstrap routine for a deviceID:
// verify same-user, load-or-generate the local pubkey, push to the
// remote agent's /auth/ssh/authorized-keys.
//
// Errors mean "we tried and it failed" — caller should print + give
// up. SkipReason in the result means "we chose not to try" — caller
// should fall back to the original ssh error message.
func sshBootstrapDevice(ctx context.Context, deviceID string) (*sshBootstrapResult, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return &sshBootstrapResult{SkipReason: "not signed in"}, nil
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}

	// Step 1: ping the remote, fail fast if unreachable or different user.
	probeCtx, probeCancel := context.WithTimeout(ctx, 8*time.Second)
	report, err := fetchRemoteAgentStatusByDeviceID(probeCtx, deviceID)
	probeCancel()
	if err != nil {
		return &sshBootstrapResult{SkipReason: fmt.Sprintf("agent unreachable: %v", err)}, nil
	}
	if report == nil || report.HTTPStatusInfo == 0 {
		return &sshBootstrapResult{SkipReason: "agent did not respond to /info"}, nil
	}
	remoteName := strings.TrimSpace(report.Name)
	if remoteName == "" {
		remoteName = deviceID
	}

	// Same-user gate. If the remote belongs to a different account, do
	// NOT push the pubkey. The /auth/ssh/authorized-keys endpoint is
	// already gated by s.auth() on the remote side, but checking here
	// gives a clearer error than a 401 from a transport candidate.
	myUser := callerUserID(cfg)
	var remoteOwner string
	if report.Info != nil {
		if v, ok := report.Info["ownerUserId"].(string); ok {
			remoteOwner = v
		}
	}
	if remoteOwner == "" {
		return &sshBootstrapResult{SkipReason: "remote agent did not report ownerUserId"}, nil
	}
	if myUser == "" {
		return &sshBootstrapResult{SkipReason: "could not verify caller identity (Convex /auth/me failed)"}, nil
	}
	if myUser != remoteOwner {
		return &sshBootstrapResult{SkipReason: fmt.Sprintf("remote owner is a different account (%s); refusing to push key", firstNonEmpty(stringFromInfo(report.Info, "ownerEmail"), remoteOwner))}, nil
	}

	// Step 2: load (or generate) the local pubkey.
	pub, err := defaultPublicKey()
	if err != nil {
		// No keys yet — generate the canonical ed25519 pair so the next
		// boot, ssh, or scp uses the same key. Equivalent to what a
		// user would do manually with `ssh-keygen`.
		if genErr := generateDefaultEd25519(); genErr != nil {
			return nil, fmt.Errorf("no SSH key found and ssh-keygen failed: %w", genErr)
		}
		pub, err = defaultPublicKey()
		if err != nil {
			return nil, fmt.Errorf("generated key but could not read it back: %w", err)
		}
	}
	pub = strings.TrimSpace(pub)

	// Step 3: POST to the remote.
	candidates, err := buildRemoteAgentCandidates(cfg, &DeviceInfo{
		DeviceID: report.DeviceID,
		Name:     report.Name,
		Alias:    report.Alias,
		Platform: report.Platform,
		IsOnline: true,
	})
	if err != nil {
		return nil, fmt.Errorf("transport candidates: %w", err)
	}
	if len(candidates) == 0 {
		return &sshBootstrapResult{SkipReason: "no reachable transport candidates"}, nil
	}
	hostname, _ := os.Hostname()
	label := strings.TrimSpace(fmt.Sprintf("yaver-bootstrap from %s @ %s", hostname, time.Now().UTC().Format("2006-01-02T15:04:05Z")))
	body, _ := json.Marshal(map[string]string{
		"publicKey": pub,
		"label":     label,
	})
	postCtx, postCancel := context.WithTimeout(ctx, 12*time.Second)
	defer postCancel()
	_, status, raw, err := doRemoteAgentRequest(postCtx, candidates, cfg.AuthToken, http.MethodPost, "/auth/ssh/authorized-keys", body, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("push key to %s: %w", remoteName, err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("push key to %s: HTTP %d %s", remoteName, status, strings.TrimSpace(string(raw)))
	}
	var resp struct {
		OK             bool   `json:"ok"`
		AlreadyPresent bool   `json:"alreadyPresent"`
		Fingerprint    string `json:"fingerprint"`
		KeyType        string `json:"keyType"`
	}
	_ = json.Unmarshal(raw, &resp)
	return &sshBootstrapResult{
		Pushed:         resp.OK && !resp.AlreadyPresent,
		AlreadyPresent: resp.AlreadyPresent,
		Fingerprint:    resp.Fingerprint,
		KeyType:        resp.KeyType,
		LocalPubKey:    pub,
		RemoteName:     remoteName,
		RemoteOSUser:   stringFromInfo(report.Info, "osUser"),
	}, nil
}

// generateDefaultEd25519 runs `ssh-keygen -t ed25519 -N "" -f
// ~/.ssh/id_ed25519` so callers without any prior SSH setup get a
// usable pair on first bootstrap. Quiet on success.
func generateDefaultEd25519() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(sshDir, "id_ed25519")
	if _, err := os.Stat(keyPath); err == nil {
		// Pair already exists, defaultPublicKey() must have failed for a
		// different reason (e.g. truncated .pub). Don't overwrite.
		return fmt.Errorf("%s exists but %s.pub did not — fix manually", keyPath, keyPath)
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "yaver"
	}
	binPath, err := osexec.LookPath("ssh-keygen")
	if err != nil {
		return fmt.Errorf("ssh-keygen not found in PATH (install OpenSSH client)")
	}
	cmd := osexec.Command(binPath, "-t", "ed25519", "-N", "", "-f", keyPath, "-C", "yaver@"+hostname)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh-keygen: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// stringFromInfo is a tiny helper to extract a string from /info's
// generic map without allocating a typed shadow.
func stringFromInfo(info map[string]interface{}, key string) string {
	if info == nil {
		return ""
	}
	if v, ok := info[key].(string); ok {
		return v
	}
	return ""
}
