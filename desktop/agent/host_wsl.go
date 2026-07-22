package main

// host_wsl.go — Windows, and the WSL question.
//
// Yaver treats Windows as a second-class DEVELOPER host but there is no reason
// it should be a second-class REMOTE MACHINE: a Windows box with WSL2 is an
// ordinary Linux workspace wearing a Windows badge, and plenty of people have
// exactly that machine sitting idle.
//
// The honest split, and why it is not "Windows vs WSL" but three cases:
//
//	native Windows   Chrome runs, the Android emulator runs (WHPX), Redroid
//	                 cannot (no Linux kernel), iOS cannot (never).
//	WSL2             a real Linux kernel, so the browser target and the Linux
//	                 toolchain work. Redroid is the interesting one: WSL2's
//	                 kernel usually ships WITHOUT binder/ashmem, so Redroid
//	                 fails at container start rather than at probe time — the
//	                 classic inventory-says-yes. And the Android emulator
//	                 inside WSL2 needs nested virtualisation that is off by
//	                 default, so it is NOT assumed either.
//	WSL1             a syscall shim, not a kernel. Anything kernel-dependent
//	                 is out.
//
// Which means the answer to "can we fully depend on WSL?" is: yes for the
// browser lane, which is the lane that carries Flutter, RN-web, SwiftWasm and
// every web stack — and that is most of what a remote machine is asked to do.
// Native Windows additionally covers the Android emulator. Neither covers iOS,
// and nothing ever will off Apple hardware.
//
// Every gate below PROBES. WSL detection reads /proc/version because the
// environment variable (WSL_DISTRO_NAME) is absent when the agent is started
// by systemd rather than a login shell — a detector that only works in an
// interactive shell is a detector that fails exactly where the daemon runs.

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// WSLInfo describes whether this Linux host is really WSL, and which version.
type WSLInfo struct {
	IsWSL bool `json:"isWsl"`
	// Version is 1 or 2 when known, 0 when it is not WSL or cannot be told.
	Version int `json:"version,omitempty"`
	// Detail is the evidence, so a report can say WHY rather than assert.
	Detail string `json:"detail,omitempty"`
}

var (
	wslOnce sync.Once
	wslInfo WSLInfo
)

// DetectWSL reports whether the agent runs inside WSL. Cached: /proc/version
// cannot change without a reboot, and this is consulted per capability probe.
func DetectWSL() WSLInfo {
	wslOnce.Do(func() { wslInfo = detectWSLUncached() })
	return wslInfo
}

func detectWSLUncached() WSLInfo {
	if runtime.GOOS != "linux" {
		return WSLInfo{}
	}
	// /proc/version is the reliable signal. WSL2 kernels are built by Microsoft
	// and say so; WSL1 reports "Microsoft" too but has no real kernel.
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return WSLInfo{}
	}
	v := strings.ToLower(string(data))
	if !strings.Contains(v, "microsoft") && !strings.Contains(v, "wsl") {
		return WSLInfo{}
	}
	info := WSLInfo{IsWSL: true, Detail: strings.TrimSpace(string(data))}
	// WSL2 ships a genuine Linux kernel and advertises "WSL2"; WSL1 does not.
	// Fall back to the interop marker, which only WSL2 mounts.
	if strings.Contains(v, "wsl2") {
		info.Version = 2
	} else if _, statErr := os.Stat("/run/WSL"); statErr == nil {
		info.Version = 2
	} else {
		info.Version = 1
	}
	return info
}

// HostSupportsLinuxKernelFeatures reports whether kernel-level Linux features
// (binder/ashmem for Redroid, cgroup tricks) can be relied on.
//
// WSL1 is a syscall translation layer with no kernel, so it is out. WSL2 has a
// kernel but Microsoft's default build omits binder/ashmem, so we do NOT assume
// it — the modules are probed for real, because "it is Linux" is exactly the
// inventory-shaped claim that produces a container which fails at start.
func HostSupportsLinuxKernelFeatures() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	w := DetectWSL()
	if w.IsWSL && w.Version < 2 {
		return false
	}
	return true
}

// HostCanRunRedroidHere is the runtime-probed form of HostCanRunRedroid.
//
// Redroid needs binder. On a normal Linux box the module is present or
// loadable; on WSL2 it usually is not, and discovering that when the container
// refuses to start costs the user a session. Probe the module, not the OS.
func HostCanRunRedroidHere() (bool, string) {
	if runtime.GOOS != "linux" {
		return false, "Redroid needs a real Linux kernel (binder/ashmem); use a Linux workspace or the Android emulator"
	}
	w := DetectWSL()
	if w.IsWSL && w.Version < 2 {
		return false, "WSL1 has no Linux kernel — upgrade the distro to WSL2 (`wsl --set-version <distro> 2`), or use the Android emulator on the Windows side"
	}
	if !binderModuleAvailable() {
		if w.IsWSL {
			return false, "WSL2's default kernel ships without binder/ashmem, which Redroid requires. Use the Android emulator on the Windows side, or a custom WSL2 kernel built with CONFIG_ANDROID_BINDER_IPC"
		}
		return false, "the binder kernel module is not available, which Redroid requires. Load it (`modprobe binder_linux`) or use the Android emulator"
	}
	return true, ""
}

// binderModuleAvailable checks for binder the way the container runtime will
// need it: a device node, or the module listed as loadable.
func binderModuleAvailable() bool {
	for _, p := range []string{
		"/dev/binder", "/dev/binderfs", "/dev/anbox-binder",
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	// binderfs may be mountable even without a node present yet.
	if data, err := os.ReadFile("/proc/filesystems"); err == nil {
		if strings.Contains(string(data), "binder") {
			return true
		}
	}
	return false
}

// HostBrowserLaneAvailable reports whether the browser preview lane — the one
// that carries Flutter, RN-web, SwiftWasm and every web stack — works here.
//
// This is the load-bearing answer for Windows: it is true on native Windows,
// on WSL1 and on WSL2, because it needs a browser and nothing else. It is why
// a Windows machine can be a genuinely useful remote workspace rather than a
// second-class one.
func HostBrowserLaneAvailable() bool { return DiscoverChromeBinary() != "" }

// DescribeHostForPreview is a one-line, honest summary for reports and doctors.
func DescribeHostForPreview() string {
	p := DetectHostPlatform()
	if w := DetectWSL(); w.IsWSL {
		if w.Version >= 2 {
			return "windows (WSL2) — Linux workspace: browser lane yes, Redroid only with a binder-capable kernel, iOS never"
		}
		return "windows (WSL1) — no Linux kernel: browser lane yes, containers no, iOS never"
	}
	switch p {
	case HostWindows:
		return "windows (native) — browser lane yes, Android emulator yes (WHPX), Redroid no, iOS never"
	case HostMacOS:
		return "macos — browser lane yes, iOS simulators yes, Android emulator yes, Redroid no"
	case HostLinux:
		return "linux — browser lane yes, Redroid yes, Android emulator with KVM, iOS never"
	}
	return string(p)
}
