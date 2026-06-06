package main

// screenlog_window.go — best-effort active app + window-title tagging,
// a Go port of talos's platform_compat.py. Every helper is best-effort:
// a frame with no tag is still a valid frame, so all failures degrade
// silently. The title stays in the LOCAL index.json only — it's on the
// Convex forbidden-field list (convex_privacy_test.go) precisely because
// window titles can carry sensitive content.

import (
	"os/exec"
	"runtime"
	"strings"
)

// activeWindowInfo returns (appName, windowTitle), either possibly "".
func activeWindowInfo() (string, string) {
	if runtime.GOOS == "linux" && isWSLHost() && lookPathOK("powershell.exe") {
		return windowsActiveWindow(true)
	}
	switch runtime.GOOS {
	case "darwin":
		return macActiveWindow()
	case "windows":
		return windowsActiveWindow(false)
	case "linux":
		return linuxActiveWindow()
	}
	return "", ""
}

func macActiveWindow() (string, string) {
	// Frontmost process name via System Events. No extra permission
	// beyond the Screen-Recording grant the capture itself needs.
	app := runOut("osascript", "-e",
		`tell application "System Events" to get name of first application process whose frontmost is true`)
	title := runOut("osascript", "-e",
		`tell application "System Events" to tell (first application process whose frontmost is true) to try
			get value of attribute "AXTitle" of front window
		end try`)
	return strings.TrimSpace(app), strings.TrimSpace(title)
}

func linuxActiveWindow() (string, string) {
	if lookPathOK("xdotool") {
		title := runOut("xdotool", "getactivewindow", "getwindowname")
		var app string
		if pid := strings.TrimSpace(runOut("xdotool", "getactivewindow", "getwindowpid")); pid != "" {
			app = strings.TrimSpace(runOut("ps", "-p", pid, "-o", "comm="))
		}
		return app, strings.TrimSpace(title)
	}
	if lookPathOK("xprop") {
		// Resolve _NET_ACTIVE_WINDOW → WM_NAME via xprop.
		root := runOut("xprop", "-root", "_NET_ACTIVE_WINDOW")
		if i := strings.LastIndex(root, "# "); i >= 0 {
			id := strings.TrimSpace(root[i+2:])
			id = strings.SplitN(id, ",", 2)[0]
			id = strings.TrimSpace(id)
			if id != "" && id != "0x0" {
				name := runOut("xprop", "-id", id, "WM_NAME")
				if j := strings.Index(name, "= "); j >= 0 {
					return "", strings.Trim(strings.TrimSpace(name[j+2:]), `"`)
				}
			}
		}
	}
	return "", ""
}

// windowsActiveWindow uses a small PowerShell snippet (GetForegroundWindow
// + GetWindowText). fromWSL routes through the interop binary.
func windowsActiveWindow(fromWSL bool) (string, string) {
	psBin := "powershell"
	if fromWSL {
		psBin = "powershell.exe"
	}
	// Double-quote here-string + a literal "|" delimiter so the whole
	// script lives in a Go raw string with no escaping. $wpid avoids the
	// reserved PowerShell $pid.
	script := `Add-Type @"
using System;
using System.Runtime.InteropServices;
using System.Text;
public class Wlog {
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern int GetWindowThreadProcessId(IntPtr h, out int wpid);
}
"@
$h=[Wlog]::GetForegroundWindow()
$sb=New-Object System.Text.StringBuilder 512
[void][Wlog]::GetWindowText($h,$sb,512)
$wpid=0
[void][Wlog]::GetWindowThreadProcessId($h,[ref]$wpid)
$proc=""
try { $proc=(Get-Process -Id $wpid).ProcessName } catch {}
Write-Output ($proc + "|" + $sb.ToString())`
	out := runOut(psBin, "-NoProfile", "-NonInteractive", "-Command", script)
	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), ""
	}
	return "", ""
}

// runOut runs a command and returns trimmed stdout, or "" on any error.
func runOut(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
