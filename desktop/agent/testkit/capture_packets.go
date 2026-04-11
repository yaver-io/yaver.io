package testkit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Packet capture escape hatch.
//
// 99% of web-dev network issues show up in the browser's CDP
// network stream we already capture via instrumentation.go. The
// tiny minority that doesn't — DNS oddities, VPN trouble,
// mis-routed sockets — is better served by running tcpdump by
// hand. This file is a thin wrapper around tcpdump so the dev can
// trigger a capture from `yaver test debug --capture-packets`
// without remembering the exact incantation, but it's explicitly
// NOT wired into the spec executor. Off by default, opt-in only.
//
// Why not bake it into every run:
//
//   - Packet capture needs root on macOS + Linux. Asking every
//     test run to `sudo` is a friction tax that doesn't pay off.
//   - TLS hides the payload anyway. Raw packets alone rarely
//     answer the question the dev is actually asking.
//   - It's debugging, not testing — the dev reaches for it when
//     something is weird, not every time they hit Save.
//
// Usage:
//
//   yaver test debug --capture-packets --iface en0 --duration 30s --out out.pcap
//
// Dev opens `out.pcap` in Wireshark (or `tcpdump -r out.pcap`)
// afterward. We don't try to parse it.

// CapturePacketsOptions configures one capture session.
type CapturePacketsOptions struct {
	Interface string        // e.g. "en0" on macOS, "wlan0" / "eth0" on Linux
	Filter    string        // tcpdump BPF filter, e.g. "port 80 or port 443"
	Duration  time.Duration // how long to capture
	OutPath   string        // where to write the pcap file
}

// CapturePackets shells out to tcpdump with a duration cap, writes
// the pcap file to disk, and returns when the timer fires. Caller
// provides sudo context (we do NOT invoke sudo ourselves so we
// don't prompt mid-run).
//
// On macOS the default tcpdump binary is in /usr/sbin/tcpdump. On
// Linux it's in /usr/bin/tcpdump or /usr/sbin/tcpdump. The runner
// just calls exec.LookPath.
func CapturePackets(ctx context.Context, opts CapturePacketsOptions) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return fmt.Errorf("packet capture only supported on macOS and Linux")
	}
	if opts.OutPath == "" {
		return fmt.Errorf("capture packets: --out is required")
	}
	if opts.Duration == 0 {
		opts.Duration = 30 * time.Second
	}
	iface := opts.Interface
	if iface == "" {
		iface = defaultCaptureInterface()
	}

	bin, err := exec.LookPath("tcpdump")
	if err != nil {
		return fmt.Errorf("tcpdump not found — install it (`brew install tcpdump` / `apt-get install tcpdump`)")
	}

	args := []string{
		"-i", iface,
		"-w", opts.OutPath,
		"-s", "0",            // full packet
		"-U",                  // unbuffered write (write as each packet arrives)
		"-G", "0",             // no rotation
	}
	if opts.Filter != "" {
		args = append(args, opts.Filter)
	}

	cctx, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tcpdump start: %w (probably needs sudo — run: sudo yaver test debug --capture-packets …)", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-cctx.Done():
		// Graceful shutdown: tcpdump flushes on SIGINT.
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		<-done
	case err := <-done:
		if err != nil && cctx.Err() == nil {
			return fmt.Errorf("tcpdump: %w", err)
		}
	}
	return nil
}

// defaultCaptureInterface picks a sensible default per OS.
func defaultCaptureInterface() string {
	switch runtime.GOOS {
	case "darwin":
		return "en0" // Wi-Fi on most MacBooks
	case "linux":
		// "any" catches every interface; works with tcpdump on modern kernels.
		return "any"
	}
	return "any"
}
