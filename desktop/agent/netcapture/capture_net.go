package netcapture

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// startTcpdump spawns tcpdump writing a classic pcap stream to stdout, which we
// decode in pure Go. Flags mirror testkit/capture_packets.go: full snap length,
// unbuffered writes so packets surface live. We do NOT shell through `sudo`
// (a long-running capture must not block on a password prompt) — if the agent
// lacks CAP_NET_RAW the pcap header read fails and the session reports a clear
// "needs privilege" error.
func startTcpdump(ctx context.Context, iface, filter string) (io.ReadCloser, *exec.Cmd, error) {
	args := []string{
		"-i", iface,
		"-w", "-", // pcap to stdout
		"-U",      // unbuffered: write each packet as it arrives
		"-s", "0", // full packet
		"-n", // no name resolution (faster, no side traffic)
	}
	if strings.TrimSpace(filter) != "" {
		args = append(args, strings.Fields(filter)...)
	}
	cmd := exec.CommandContext(ctx, "tcpdump", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("tcpdump start failed (is it installed and permitted?): %w", err)
	}
	return stdout, cmd, nil
}
