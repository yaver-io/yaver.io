package main

// hermes_runtime.go — server-side Hermes bytecode validation + execution.
//
// Variant (a) of project_voice_glasses_revival_2026_05_27.md task 11:
// subprocess execution via the `hermes` CLI. Lowest blast radius, ships
// today. Upgrade to CGO-embedded Hermes (variant b) only if validation
// throughput becomes a bottleneck.
//
// Use cases:
//
//   1. Pre-push validation — before yaver-cli pushes a Hermes bundle to
//      the phone, we run it once headless. Catches crashes / unresolved
//      imports / native-module misses before they reach the device.
//      Saves a deploy cycle.
//
//   2. Smoke-test glue for the "voice-launch" verb — user says
//      "launch sfmg" via /voice/stream, the agent locates sfmg's
//      compiled bundle, calls HermesRun for a 5s sanity boot, then
//      either fans out the Hermes-push to paired phones (success) or
//      returns an error for TTS readback (failure).
//
//   3. Headless SSR for glasses HUD — render a tiny React tree to a
//      string for showTextWall on Mentra G1/G2. (Later — not in v1.)
//
// We deliberately keep this layer thin. The Hermes runtime config
// (heap size, microtask queue, profiler) is whatever hermes-cli ships
// with; if you need tighter control, swap to variant (b).

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HermesBytecodeMagic is the 4-byte LE marker at offset 4 of every
// valid HBC bundle. Per CLAUDE.md mobile section, anchor of the
// container validator.
const HermesBytecodeMagic uint32 = 0x1F1903C1

// HermesBytecodeVersion is the BC format version we expect. Mobile is
// pinned to 96; mismatches mean a wrong-toolchain rebuild and the phone
// will refuse to load the bundle.
const HermesBytecodeVersion uint32 = 96

// HermesResult is the structured outcome of a HermesRun call.
type HermesResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Killed     bool   `json:"killed,omitempty"` // true if we SIGKILL'd on timeout
	Error      string `json:"error,omitempty"`  // exec-layer error (binary missing, etc.)
}

// HermesValidation is the cheap pre-flight: does the file even look
// like an HBC bundle? Runs in microseconds — call before HermesRun
// to avoid wasting a fork on garbage input.
type HermesValidation struct {
	OK      bool   `json:"ok"`
	Magic   uint32 `json:"magic"`
	Version uint32 `json:"version"`
	Size    int64  `json:"size"`
	Error   string `json:"error,omitempty"`
}

// ValidateHermesBundle reads the first 16 bytes of a candidate bundle
// and asserts the HBC magic + version. Pure header check — does NOT
// execute the bundle.
func ValidateHermesBundle(path string) HermesValidation {
	st, err := os.Stat(path)
	if err != nil {
		return HermesValidation{OK: false, Error: fmt.Sprintf("stat: %v", err)}
	}
	if st.IsDir() {
		return HermesValidation{OK: false, Size: 0, Error: "path is a directory"}
	}
	f, err := os.Open(path)
	if err != nil {
		return HermesValidation{OK: false, Size: st.Size(), Error: fmt.Sprintf("open: %v", err)}
	}
	defer f.Close()

	header := make([]byte, 16)
	if _, err := io.ReadFull(f, header); err != nil {
		return HermesValidation{OK: false, Size: st.Size(), Error: fmt.Sprintf("read header: %v", err)}
	}
	magic := binary.LittleEndian.Uint32(header[4:8])
	version := binary.LittleEndian.Uint32(header[8:12])
	if magic != HermesBytecodeMagic {
		return HermesValidation{OK: false, Size: st.Size(), Magic: magic, Version: version, Error: fmt.Sprintf("bad magic: got %#x want %#x", magic, HermesBytecodeMagic)}
	}
	if version != HermesBytecodeVersion {
		// Version mismatch is a warning, not a hard fail — the phone may
		// happily run an older bundle if its embedded Hermes matches.
		return HermesValidation{OK: true, Size: st.Size(), Magic: magic, Version: version, Error: fmt.Sprintf("version mismatch (got %d want %d) — bundle may not load on a device pinned to v%d", version, HermesBytecodeVersion, HermesBytecodeVersion)}
	}
	return HermesValidation{OK: true, Size: st.Size(), Magic: magic, Version: version}
}

// hermesBinaryPath resolves the hermes CLI. Honors $YAVER_HERMES_BIN
// override (useful when a user has a vendored copy), else falls back
// to PATH. We accept either `hermes` or `hermesc` (the compiler also
// runs bytecode in some builds).
func hermesBinaryPath() (string, error) {
	if env := os.Getenv("YAVER_HERMES_BIN"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
		return "", fmt.Errorf("YAVER_HERMES_BIN=%q not found", env)
	}
	for _, name := range []string{"hermes", "hermesc"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("hermes binary not found on PATH; install via `npm i -g hermes-engine` or set $YAVER_HERMES_BIN")
}

// HermesRunOpts tunes a single HermesRun call.
type HermesRunOpts struct {
	BundlePath string // absolute path to .hbc bundle (or .js for source mode)
	TimeoutSec int    // 0 = 30s default; capped at 300s
	// EnableSourceMode runs raw JS via hermes -O0 instead of HBC bytecode.
	// Slower but useful for validating un-bundled module entry points.
	EnableSourceMode bool
	// ExtraArgs are appended after the standard flags. Use sparingly.
	ExtraArgs []string
	// StdinReader feeds source on stdin when BundlePath is empty.
	StdinReader io.Reader
}

// HermesRun executes a bundle via the hermes CLI and returns its
// structured result. Blocking call — wrap in a goroutine for HTTP
// handlers that need to stay responsive.
func HermesRun(ctx context.Context, opts HermesRunOpts) HermesResult {
	bin, err := hermesBinaryPath()
	if err != nil {
		return HermesResult{Error: err.Error()}
	}

	timeoutSec := opts.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	args := []string{}
	if !opts.EnableSourceMode {
		args = append(args, "-emit-binary=false")
	}
	args = append(args, opts.ExtraArgs...)
	if opts.BundlePath != "" {
		args = append(args, opts.BundlePath)
	}

	cmd := exec.CommandContext(runCtx, bin, args...)
	if opts.StdinReader != nil {
		cmd.Stdin = opts.StdinReader
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	err = cmd.Run()
	durMs := time.Since(start).Milliseconds()

	res := HermesResult{
		Stdout:     strings.TrimRight(outBuf.String(), "\n"),
		Stderr:     strings.TrimRight(errBuf.String(), "\n"),
		DurationMs: durMs,
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		}
		if runCtx.Err() == context.DeadlineExceeded {
			res.Killed = true
			res.Error = fmt.Sprintf("hermes timed out after %ds", timeoutSec)
		} else if res.ExitCode == 0 {
			// non-ExitError → exec failed before launching
			res.Error = err.Error()
		}
		return res
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	return res
}

// HermesSmokeTest is the convenience entry point for the voice-launch
// verb: given a directory that should contain a Hermes bundle named
// `index.hbc`, validate the magic + run with a 5s budget. Returns
// `ok=true` only if both pass.
type HermesSmokeResult struct {
	OK         bool             `json:"ok"`
	Validation HermesValidation `json:"validation"`
	Run        HermesResult     `json:"run"`
	Hint       string           `json:"hint,omitempty"` // user-facing "what to fix" line
}

func HermesSmokeTest(ctx context.Context, bundleDir string) HermesSmokeResult {
	res := HermesSmokeResult{}
	bundlePath := filepath.Join(bundleDir, "index.hbc")
	res.Validation = ValidateHermesBundle(bundlePath)
	if !res.Validation.OK {
		res.Hint = fmt.Sprintf("bundle header failed: %s — rebuild with `expo export:embed --platform ios --bundle-output index.bundle && hermesc -emit-binary -out index.hbc index.bundle`", res.Validation.Error)
		return res
	}
	res.Run = HermesRun(ctx, HermesRunOpts{
		BundlePath: bundlePath,
		TimeoutSec: 5,
	})
	if res.Run.Error != "" {
		res.Hint = "hermes binary not invokable — check $YAVER_HERMES_BIN or `npm i -g hermes-engine`"
		return res
	}
	if res.Run.ExitCode != 0 {
		res.Hint = fmt.Sprintf("bundle exited %d — stderr: %s", res.Run.ExitCode, head(res.Run.Stderr, 240))
		return res
	}
	res.OK = true
	return res
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
