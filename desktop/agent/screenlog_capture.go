package main

// screenlog_capture.go — cross-platform raw screen grab for screenlog,
// including the WSL2 special case.
//
// The hard part is WSL: a `yaver serve` running inside WSL sees
// runtime.GOOS=="linux", but there are TWO screens — the Windows host
// desktop (what the user actually looks at) and the mostly-empty WSLg
// Wayland surface. Naïve scrot/import grabs the wrong one. We detect WSL
// (discovery.go::isWSLHost) and, for target host/auto, capture the
// Windows desktop through the `powershell.exe` interop bridge — the same
// mechanism process_wsl.go already uses with cmd.exe — then read the PNG
// back through /mnt/c.
//
// Every byte stays local. No network, no upload.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type rawScreenlogFrame struct {
	display int
	png     []byte
}

// powershellCaptureScript builds the PS one-liner that grabs the whole
// virtual screen (all monitors) or just the primary, saves a PNG to the
// Windows temp dir, and prints the Windows path on stdout.
func powershellCaptureScript(all bool) string {
	bounds := "[System.Windows.Forms.SystemInformation]::VirtualScreen"
	if !all {
		bounds = "[System.Windows.Forms.Screen]::PrimaryScreen.Bounds"
	}
	return "Add-Type -AssemblyName System.Windows.Forms,System.Drawing; " +
		"$b=" + bounds + "; " +
		"$bmp=New-Object System.Drawing.Bitmap($b.Width,$b.Height); " +
		"$g=[System.Drawing.Graphics]::FromImage($bmp); " +
		"$g.CopyFromScreen($b.Location,[System.Drawing.Point]::Empty,$b.Size); " +
		"$p=Join-Path $env:TEMP ('yaver-slog-'+[System.Guid]::NewGuid().ToString()+'.png'); " +
		"$bmp.Save($p); Write-Output $p"
}

// winPathToWSL translates a Windows path (C:\Users\..\f.png) to its WSL
// mount (/mnt/c/Users/../f.png). Prefers `wslpath`, falls back to a
// string rewrite.
func winPathToWSL(winPath string) string {
	winPath = strings.TrimSpace(winPath)
	if winPath == "" {
		return ""
	}
	if out, err := exec.Command("wslpath", "-u", winPath).Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p
		}
	}
	p := strings.ReplaceAll(winPath, "\\", "/")
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		p = "/mnt/" + drive + p[2:]
	}
	return p
}

// captureViaPowerShell runs the PS capture and returns the PNG bytes.
// When fromWSL is true the printed Windows path is mapped to /mnt/c and
// the file is read (and removed) from there; otherwise it's a native
// Windows path read directly.
func captureViaPowerShell(all, fromWSL bool) ([]byte, error) {
	psBin := "powershell"
	if fromWSL {
		psBin = "powershell.exe"
	}
	cmd := exec.Command(psBin, "-NoProfile", "-NonInteractive", "-Command", powershellCaptureScript(all))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("powershell screen capture failed: %v", err)
	}
	winPath := strings.TrimSpace(string(out))
	if winPath == "" {
		return nil, fmt.Errorf("powershell capture produced no path (is the Windows session unlocked?)")
	}
	readPath := winPath
	if fromWSL {
		readPath = winPathToWSL(winPath)
	}
	data, err := os.ReadFile(readPath)
	if err != nil {
		return nil, fmt.Errorf("reading captured frame at %s: %v", readPath, err)
	}
	_ = os.Remove(readPath)
	return data, nil
}

// captureMacDisplays grabs each attached display via `screencapture -D`.
// Display 1 is the main display; we probe upward until a grab fails.
func captureMacDisplays(all bool) ([]rawScreenlogFrame, error) {
	var frames []rawScreenlogFrame
	maxD := 1
	if all {
		maxD = 8
	}
	for d := 1; d <= maxD; d++ {
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("yaver-slog-%d-%d.png", d, time.Now().UnixNano()))
		cmd := exec.Command("screencapture", "-x", "-t", "png", fmt.Sprintf("-D%d", d), tmp)
		err := cmd.Run()
		if err != nil {
			os.Remove(tmp)
			break
		}
		data, rerr := os.ReadFile(tmp)
		os.Remove(tmp)
		if rerr != nil || len(data) == 0 {
			break
		}
		frames = append(frames, rawScreenlogFrame{display: d - 1, png: data})
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("screencapture produced no frames — grant Screen Recording permission to the agent in System Settings → Privacy")
	}
	return frames, nil
}

// captureLinuxX11 grabs the combined X screen as a single image.
func captureLinuxX11() ([]rawScreenlogFrame, error) {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("yaver-slog-%d.png", time.Now().UnixNano()))
	defer os.Remove(tmp)
	var cmd *exec.Cmd
	switch {
	case lookPathOK("gnome-screenshot"):
		cmd = exec.Command("gnome-screenshot", "-f", tmp)
	case lookPathOK("scrot"):
		cmd = exec.Command("scrot", "-o", tmp)
	case lookPathOK("import"):
		cmd = exec.Command("import", "-window", "root", tmp)
	default:
		return nil, fmt.Errorf("no Linux screenshot tool found — run `yaver screenlog install-deps` (installs scrot)")
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("linux screen capture failed: %v", err)
	}
	data, err := os.ReadFile(tmp)
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("linux capture produced no image")
	}
	return []rawScreenlogFrame{{display: 0, png: data}}, nil
}

func lookPathOK(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// wslShouldUseHost decides whether to capture the Windows host desktop
// (vs the WSLg Linux surface) under WSL.
func wslShouldUseHost(cfg ScreenlogConfig) bool {
	switch cfg.WSLTarget {
	case "host":
		return true
	case "wslg":
		return false
	default: // auto — prefer the Windows desktop if interop is available
		return lookPathOK("powershell.exe")
	}
}

// captureScreenlogFrames is the entry point: returns one rawScreenlogFrame
// per display for the current OS / WSL target.
func captureScreenlogFrames(cfg ScreenlogConfig) ([]rawScreenlogFrame, error) {
	all := cfg.Displays == "all"

	if runtime.GOOS == "linux" && isWSLHost() {
		if wslShouldUseHost(cfg) {
			if !lookPathOK("powershell.exe") {
				return nil, fmt.Errorf("WSL host capture needs powershell.exe on PATH (Windows interop) — or set wslTarget=wslg")
			}
			data, err := captureViaPowerShell(all, true)
			if err != nil {
				return nil, err
			}
			return []rawScreenlogFrame{{display: 0, png: data}}, nil
		}
		// Fall through to WSLg X11/Wayland capture below.
	}

	switch runtime.GOOS {
	case "darwin":
		return captureMacDisplays(all)
	case "linux":
		return captureLinuxX11()
	case "windows":
		data, err := captureViaPowerShell(all, false)
		if err != nil {
			return nil, err
		}
		return []rawScreenlogFrame{{display: 0, png: data}}, nil
	default:
		return nil, fmt.Errorf("screenlog not supported on %s", runtime.GOOS)
	}
}

// screenlogDriverName describes the active capture path for diagnostics.
func screenlogDriverName(cfg ScreenlogConfig) string {
	if runtime.GOOS == "linux" && isWSLHost() {
		if wslShouldUseHost(cfg) {
			return "wsl-interop:powershell.exe (Windows host desktop)"
		}
		return "wslg:x11 (Linux GUI surface)"
	}
	switch runtime.GOOS {
	case "darwin":
		return "macos:screencapture"
	case "windows":
		return "windows:powershell:CopyFromScreen"
	case "linux":
		switch {
		case lookPathOK("gnome-screenshot"):
			return "linux:gnome-screenshot"
		case lookPathOK("scrot"):
			return "linux:scrot"
		case lookPathOK("import"):
			return "linux:imagemagick-import"
		}
		return "linux:none"
	}
	return runtime.GOOS + ":unsupported"
}

// screenlogProbe attempts a real capture so start() can fail fast and
// the drivers verb can report ground truth. Returns the driver name and
// display count.
func screenlogProbe(cfg ScreenlogConfig) (map[string]interface{}, error) {
	frames, err := screenlogCaptureFn(cfg)
	if err != nil {
		return map[string]interface{}{
			"driver":    screenlogDriverName(cfg),
			"available": false,
			"error":     err.Error(),
			"deps":      screenlogDepStatus(),
		}, err
	}
	total := 0
	for _, f := range frames {
		total += len(f.png)
	}
	return map[string]interface{}{
		"driver":      screenlogDriverName(cfg),
		"available":   true,
		"displays":    len(frames),
		"sampleBytes": total,
		"wsl":         runtime.GOOS == "linux" && isWSLHost(),
		"input":       inputCaptureDriver(),
		"deps":        screenlogDepStatus(),
	}, nil
}
