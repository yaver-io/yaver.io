package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type wslRuntimeInfo struct {
	IsWSL   bool
	Version int
}

// wslRuntimeProbe is the indirection point for tests. Production path
// is detectWSLRuntimeReal; tests can swap it out to assert WSL-specific
// branches on a non-WSL host.
var wslRuntimeProbe = detectWSLRuntimeReal

func detectWSLRuntime() wslRuntimeInfo {
	return wslRuntimeProbe()
}

func detectWSLRuntimeReal() wslRuntimeInfo {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		if containsWSL2Marker(strings.ToLower(os.Getenv("WSL_INTEROP"))) {
			return wslRuntimeInfo{IsWSL: true, Version: 2}
		}
	}

	combined := strings.ToLower(readTextFile("/proc/version") + "\n" + readTextFile("/proc/sys/kernel/osrelease"))
	if !strings.Contains(combined, "microsoft") {
		return wslRuntimeInfo{}
	}
	if containsWSL2Marker(combined) {
		return wslRuntimeInfo{IsWSL: true, Version: 2}
	}
	return wslRuntimeInfo{IsWSL: true, Version: 1}
}

func isWSL() bool {
	return detectWSLRuntime().IsWSL
}

func printWSL2RequirementWarning() {
	rt := detectWSLRuntime()
	if !rt.IsWSL || rt.Version != 1 {
		return
	}

	fmt.Println("Warning: detected WSL1.")
	fmt.Println("Yaver depends on WSL2 on Windows hosts.")
	fmt.Println("Upgrade this distro to WSL2, then rerun this command.")
	fmt.Println()
}

func containsWSL2Marker(s string) bool {
	return strings.Contains(s, "wsl2")
}

func readTextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// quicMinUDPBuffer is the receive/send buffer size quic-go recommends
// to avoid "timeout: no recent network activity" on handshake. The
// Linux kernel default (~208 KiB) is far too small under WSL2, where
// the NAT layer adds latency and drops fragmented QUIC packets.
const quicMinUDPBuffer = 7_500_000

// maybeRunWSL2NetworkTuning runs once at `yaver serve` start. Under
// WSL2 it raises net.core.{r,w}mem_max so the QUIC relay tunnel can
// complete its handshake. If we can't write (no sudo), it prints a
// one-screen remediation banner. No-op on non-WSL2 hosts. Never blocks.
func maybeRunWSL2NetworkTuning() {
	rt := detectWSLRuntime()
	if !rt.IsWSL || rt.Version != 2 {
		return
	}
	rmem := readSysctlInt("net.core.rmem_max")
	wmem := readSysctlInt("net.core.wmem_max")
	if rmem >= quicMinUDPBuffer && wmem >= quicMinUDPBuffer {
		return
	}
	rOk := trySetSysctl("net.core.rmem_max", quicMinUDPBuffer)
	wOk := trySetSysctl("net.core.wmem_max", quicMinUDPBuffer)
	if rOk && wOk {
		fmt.Println("Yaver: raised WSL2 UDP buffers to 7.5 MB for QUIC relay.")
		return
	}
	fmt.Println()
	fmt.Println("Yaver: WSL2 detected — UDP buffers too small for the QUIC relay.")
	fmt.Printf("  current rmem_max=%d wmem_max=%d (need %d)\n", rmem, wmem, quicMinUDPBuffer)
	fmt.Println("Fix once (requires sudo):")
	fmt.Println("  sudo sysctl -w net.core.rmem_max=7500000 net.core.wmem_max=7500000")
	fmt.Println("  echo -e 'net.core.rmem_max=7500000\\nnet.core.wmem_max=7500000' | sudo tee -a /etc/sysctl.conf")
	fmt.Println()
	fmt.Println("If QUIC still times out after that, WSL2's default NAT is dropping handshakes.")
	fmt.Println("Switch to mirrored networking (Windows 11 22H2+). In the Windows file")
	wslConfigPath := "%" + "USERPROFILE" + "%" + "\\.wslconfig"
	os.Stdout.WriteString("  " + wslConfigPath + "\n")
	fmt.Println("  [wsl2]")
	fmt.Println("  networkingMode=mirrored")
	fmt.Println("then run `wsl --shutdown` from PowerShell.")
	fmt.Println("See docs/wsl2-relay-troubleshooting.md for background + alternatives.")
	fmt.Println()
}

func readSysctlInt(key string) int {
	path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
	s := strings.TrimSpace(readTextFile(path))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func trySetSysctl(key string, value int) bool {
	path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
	return os.WriteFile(path, []byte(strconv.Itoa(value)), 0o644) == nil
}

func preferredUnixShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return "sh"
}
