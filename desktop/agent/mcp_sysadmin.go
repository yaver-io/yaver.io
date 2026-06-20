package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Kernel & modules
// ---------------------------------------------------------------------------

func mcpDmesg(level string, lines int) interface{} {
	args := []string{"dmesg", "--time-format=reltime"}
	if level != "" {
		args = append(args, "--level="+level)
	}
	if lines > 0 {
		args = append(args, fmt.Sprintf("| tail -%d", lines))
		out, err := runCmd("sh", "-c", "sudo "+strings.Join(args, " "))
		if err != nil {
			out, _ = runCmd("sh", "-c", strings.Join(args, " "))
		}
		return map[string]interface{}{"dmesg": out}
	}
	args = append(args, "| tail -100")
	out, err := runCmd("sh", "-c", "sudo "+strings.Join(args, " "))
	if err != nil {
		out, _ = runCmd("sh", "-c", strings.Join(args, " "))
	}
	return map[string]interface{}{"dmesg": out}
}

func mcpLsmod() interface{} {
	out, err := runCmd("lsmod")
	if err != nil {
		if runtime.GOOS == "darwin" {
			out, err = runCmd("kextstat")
			if err != nil {
				return map[string]interface{}{"error": "lsmod not available on macOS, kextstat failed: " + err.Error()}
			}
			return map[string]interface{}{"modules": out}
		}
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"modules": out}
}

func mcpModinfo(module string) interface{} {
	out, err := runCmd("modinfo", module)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("modinfo %s: %s", module, out)}
	}
	return map[string]interface{}{"module": module, "info": out}
}

func mcpInsmod(module string) interface{} {
	out, err := runCmd("sudo", "modprobe", module)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("modprobe %s: %s — %s", module, err, out)}
	}
	return map[string]interface{}{"ok": true, "loaded": module}
}

func mcpRmmod(module string) interface{} {
	out, err := runCmd("sudo", "modprobe", "-r", module)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("modprobe -r %s: %s — %s", module, err, out)}
	}
	return map[string]interface{}{"ok": true, "unloaded": module}
}

func mcpUname() interface{} {
	out, _ := runCmd("uname", "-a")
	kernel, _ := runCmd("uname", "-r")
	arch, _ := runCmd("uname", "-m")
	hostname, _ := os.Hostname()
	return map[string]interface{}{
		"full":     strings.TrimSpace(out),
		"kernel":   strings.TrimSpace(kernel),
		"arch":     strings.TrimSpace(arch),
		"hostname": hostname,
		"os":       runtime.GOOS,
	}
}

func mcpSysctl(key string) interface{} {
	if key == "" {
		out, err := runCmd("sysctl", "-a")
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		// Truncate — sysctl -a is huge
		lines := strings.Split(out, "\n")
		if len(lines) > 100 {
			return map[string]interface{}{"output": strings.Join(lines[:100], "\n"), "truncated": true, "total_lines": len(lines), "note": "Pass a key to get specific value"}
		}
		return map[string]interface{}{"output": out}
	}
	out, err := runCmd("sysctl", key)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"value": strings.TrimSpace(out)}
}

// ---------------------------------------------------------------------------
// Process monitoring — top, htop, ps
// ---------------------------------------------------------------------------

func mcpTopSnapshot() interface{} {
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("top", "-l", "1", "-n", "20", "-o", "cpu")
	} else {
		out, err = runCmd("top", "-bn1", "-o", "%CPU")
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"snapshot": out}
}

func mcpPsAux(sortBy, filter string) interface{} {
	var out string
	var err error
	if filter != "" {
		out, err = runCmd("sh", "-c", fmt.Sprintf("ps aux | head -1; ps aux | grep -i '%s' | grep -v grep", filter))
	} else {
		switch sortBy {
		case "cpu":
			if runtime.GOOS == "darwin" {
				out, err = runCmd("ps", "aux", "-r")
			} else {
				out, err = runCmd("ps", "aux", "--sort=-%cpu")
			}
		case "mem", "memory":
			if runtime.GOOS == "darwin" {
				out, err = runCmd("ps", "aux", "-m")
			} else {
				out, err = runCmd("ps", "aux", "--sort=-%mem")
			}
		default:
			out, err = runCmd("ps", "aux")
		}
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	// Limit output
	lines := strings.Split(out, "\n")
	if len(lines) > 50 {
		out = strings.Join(lines[:50], "\n") + fmt.Sprintf("\n... (%d more)", len(lines)-50)
	}
	return map[string]interface{}{"processes": out}
}

func mcpPsTree() interface{} {
	out, err := runCmd("pstree")
	if err != nil {
		out, err = runCmd("ps", "axjf")
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	return map[string]interface{}{"tree": out}
}

func mcpLoadAverage() interface{} {
	out, _ := runCmd("sh", "-c", "cat /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null || uptime")
	nproc, _ := runCmd("sh", "-c", "nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null")
	return map[string]interface{}{
		"load": strings.TrimSpace(out),
		"cpus": strings.TrimSpace(nproc),
	}
}

// ---------------------------------------------------------------------------
// Memory — vm, swap, buffers/cache
// ---------------------------------------------------------------------------

func mcpVmstat(count int) interface{} {
	if count <= 0 {
		count = 5
	}
	out, err := runCmd("vmstat", "1", strconv.Itoa(count))
	if err != nil {
		if runtime.GOOS == "darwin" {
			out, err = runCmd("vm_stat")
			if err != nil {
				return map[string]interface{}{"error": err.Error()}
			}
		} else {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	return map[string]interface{}{"vmstat": out}
}

func mcpSwap() interface{} {
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("sysctl", "vm.swapusage")
	} else {
		out, err = runCmd("swapon", "--show")
		if err != nil {
			out, _ = runCmd("free", "-h")
		}
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"swap": out}
}

// ---------------------------------------------------------------------------
// Disk — df, du, lsblk, fdisk, mount, findmnt
// ---------------------------------------------------------------------------

func mcpDf(path string) interface{} {
	args := []string{"-h"}
	if path != "" {
		args = append(args, path)
	}
	out, err := runCmd("df", args...)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"filesystems": out}
}

func mcpDu(path string, depth int) interface{} {
	if path == "" {
		path = "."
	}
	if depth <= 0 {
		depth = 1
	}
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("du", "-h", "-d", strconv.Itoa(depth), path)
	} else {
		out, err = runCmd("du", "-h", "--max-depth="+strconv.Itoa(depth), path)
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"usage": out}
}

func mcpLsblk() interface{} {
	out, err := runCmd("lsblk", "-o", "NAME,SIZE,TYPE,MOUNTPOINT,FSTYPE,MODEL")
	if err != nil {
		if runtime.GOOS == "darwin" {
			out, _ = runCmd("diskutil", "list")
			return map[string]interface{}{"disks": out}
		}
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"devices": out}
}

func mcpFdisk() interface{} {
	out, err := runCmd("sudo", "fdisk", "-l")
	if err != nil {
		if runtime.GOOS == "darwin" {
			out, _ = runCmd("diskutil", "list")
			return map[string]interface{}{"partitions": out}
		}
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"partitions": out}
}

func mcpMounts() interface{} {
	out, err := runCmd("findmnt", "--real", "-o", "TARGET,SOURCE,FSTYPE,OPTIONS")
	if err != nil {
		out, _ = runCmd("mount")
	}
	return map[string]interface{}{"mounts": out}
}

func mcpIostat() interface{} {
	out, err := runCmd("iostat", "-x", "1", "2")
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("iostat: %s (install: brew install coreutils or apt install sysstat)", err)}
	}
	return map[string]interface{}{"io": out}
}

// ---------------------------------------------------------------------------
// Tree — directory listing
// ---------------------------------------------------------------------------

func mcpTree(path string, depth int, all bool, dirsOnly bool) interface{} {
	if path == "" {
		path = "."
	}
	if depth <= 0 {
		depth = 3
	}
	args := []string{"-L", strconv.Itoa(depth), "--dirsfirst", "-I", "node_modules|.git|__pycache__|.next|dist|build|vendor|target"}
	if all {
		args = append(args, "-a")
	}
	if dirsOnly {
		args = append(args, "-d")
	}
	args = append(args, path)
	out, err := runCmd("tree", args...)
	if err != nil {
		// Fallback to find
		findArgs := []string{path, "-maxdepth", strconv.Itoa(depth)}
		if dirsOnly {
			findArgs = append(findArgs, "-type", "d")
		}
		findArgs = append(findArgs, "-not", "-path", "*/node_modules/*", "-not", "-path", "*/.git/*")
		out, _ = runCmd("find", findArgs...)
		return map[string]interface{}{"tree": out, "note": "install tree for better output: brew install tree"}
	}
	return map[string]interface{}{"tree": out}
}

// ---------------------------------------------------------------------------
// Hardware info
// ---------------------------------------------------------------------------

func mcpCpuInfo() interface{} {
	if runtime.GOOS == "darwin" {
		brand, _ := runCmd("sysctl", "-n", "machdep.cpu.brand_string")
		cores, _ := runCmd("sysctl", "-n", "hw.ncpu")
		physCores, _ := runCmd("sysctl", "-n", "hw.physicalcpu")
		memBytes, _ := runCmd("sysctl", "-n", "hw.memsize")
		return map[string]interface{}{
			"brand":          strings.TrimSpace(brand),
			"logical_cores":  strings.TrimSpace(cores),
			"physical_cores": strings.TrimSpace(physCores),
			"memory_bytes":   strings.TrimSpace(memBytes),
		}
	}
	out, err := runCmd("lscpu")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"cpu": out}
}

func mcpLspci() interface{} {
	out, err := runCmd("lspci")
	if err != nil {
		if runtime.GOOS == "darwin" {
			out, _ = runCmd("system_profiler", "SPPCIDataType")
			return map[string]interface{}{"pci": out}
		}
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"pci": out}
}

func mcpLsusb() interface{} {
	out, err := runCmd("lsusb")
	if err != nil {
		if runtime.GOOS == "darwin" {
			out, _ = runCmd("system_profiler", "SPUSBDataType")
			return map[string]interface{}{"usb": out}
		}
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"usb": out}
}

func mcpSensors() interface{} {
	out, err := runCmd("sensors")
	if err != nil {
		if runtime.GOOS == "darwin" {
			// Try osx-cpu-temp or smctemp
			out, err = runCmd("osx-cpu-temp")
			if err != nil {
				return map[string]interface{}{"error": "sensors not available. Linux: apt install lm-sensors. macOS: brew install osx-cpu-temp"}
			}
		} else {
			return map[string]interface{}{"error": "sensors not found. Install: apt install lm-sensors && sensors-detect"}
		}
	}
	return map[string]interface{}{"sensors": out}
}

// ---------------------------------------------------------------------------
// Firewall
// ---------------------------------------------------------------------------

func mcpUfw(action, rule string) interface{} {
	switch action {
	case "status":
		out, err := runCmd("sudo", "ufw", "status", "verbose")
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"status": out}
	case "allow":
		out, err := runCmd("sudo", "ufw", "allow", rule)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "output": out}
	case "deny":
		out, err := runCmd("sudo", "ufw", "deny", rule)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "output": out}
	case "delete":
		out, err := runCmd("sudo", "ufw", "delete", rule)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "output": out}
	default:
		return map[string]interface{}{"error": "action: status, allow, deny, delete"}
	}
}

func mcpIptables() interface{} {
	out, err := runCmd("sudo", "iptables", "-L", "-n", "-v", "--line-numbers")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"rules": out}
}

// ---------------------------------------------------------------------------
// Users & sessions
// ---------------------------------------------------------------------------

func mcpWho() interface{} {
	out, _ := runCmd("w")
	return map[string]interface{}{"users": out}
}

func mcpLastLogins(count int) interface{} {
	if count <= 0 {
		count = 20
	}
	out, _ := runCmd("last", fmt.Sprintf("-%d", count))
	return map[string]interface{}{"logins": out}
}

func mcpTimeDateInfo() interface{} {
	out, err := runCmd("timedatectl")
	if err != nil {
		// macOS fallback
		dateOut, _ := runCmd("date")
		tzOut, _ := runCmd("sh", "-c", "echo $TZ")
		return map[string]interface{}{"date": dateOut, "timezone": tzOut}
	}
	return map[string]interface{}{"datetime": out}
}

func mcpHostnameInfo() interface{} {
	out, err := runCmd("hostnamectl")
	if err != nil {
		hostname, _ := os.Hostname()
		return map[string]interface{}{"hostname": hostname}
	}
	return map[string]interface{}{"info": out}
}

// ---------------------------------------------------------------------------
// Journalctl — systemd journal
// ---------------------------------------------------------------------------

func mcpJournalctl(unit string, priority string, lines int, boot bool, since string) interface{} {
	args := []string{"--no-pager"}
	if unit != "" {
		args = append(args, "-u", unit)
	}
	if priority != "" {
		args = append(args, "-p", priority)
	}
	if lines > 0 {
		args = append(args, "-n", strconv.Itoa(lines))
	} else {
		args = append(args, "-n", "100")
	}
	if boot {
		args = append(args, "-b")
	}
	if since != "" {
		args = append(args, "--since", since)
	}
	out, err := runCmd("journalctl", args...)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("journalctl: %s — %s", err, out)}
	}
	return map[string]interface{}{"logs": out}
}

func mcpJournalctlErrors() interface{} {
	out, err := runCmd("journalctl", "--no-pager", "-p", "err", "-b", "-n", "50")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"errors": out}
}

func mcpJournalctlDiskUsage() interface{} {
	out, err := runCmd("journalctl", "--disk-usage")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"usage": out}
}

// ---------------------------------------------------------------------------
// Systemctl — systemd service management
// ---------------------------------------------------------------------------

func mcpSystemctl(action, unit string) interface{} {
	switch action {
	case "status":
		out, _ := runCmd("systemctl", "status", unit, "--no-pager")
		return map[string]interface{}{"status": out}
	case "start", "stop", "restart", "enable", "disable":
		// Helper-first (privilege-separated root helper on confined operator
		// nodes), scoped-sudo fallback elsewhere. See helper_client.go.
		out, err := privilegedSystemctl(action, unit)
		if err != nil {
			return map[string]interface{}{"error": err.Error(), "output": out}
		}
		return map[string]interface{}{"ok": true, "action": action, "unit": unit}
	case "list":
		out, _ := runCmd("systemctl", "list-units", "--type=service", "--no-pager", "--no-legend")
		return map[string]interface{}{"services": out}
	case "failed":
		out, _ := runCmd("systemctl", "--failed", "--no-pager", "--no-legend")
		return map[string]interface{}{"failed": out}
	case "timers":
		out, _ := runCmd("systemctl", "list-timers", "--no-pager")
		return map[string]interface{}{"timers": out}
	default:
		return map[string]interface{}{"error": "action: status, start, stop, restart, enable, disable, list, failed, timers"}
	}
}

// ---------------------------------------------------------------------------
// GDB / LLDB — advanced debugging
// ---------------------------------------------------------------------------

func mcpGDBAttach(pid int, commands string) interface{} {
	if commands == "" {
		commands = "bt\ninfo threads\ninfo registers\ndetach\nquit"
	}
	out, err := runCmd("sh", "-c", fmt.Sprintf("echo '%s' | gdb -batch -p %d 2>&1", commands, pid))
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("gdb: %s — %s", err, out)}
	}
	return map[string]interface{}{"output": out, "pid": pid}
}

func mcpGDBCoreDump(binary, corefile string) interface{} {
	out, err := runCmd("gdb", "-batch", "-ex", "bt full", "-ex", "info threads", "-ex", "quit", binary, corefile)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("gdb: %s — %s", err, out)}
	}
	return map[string]interface{}{"backtrace": out, "binary": binary, "core": corefile}
}

func mcpLLDBAttach(pid int, commands string) interface{} {
	if commands == "" {
		commands = "bt all\nregister read\ndetach\nquit"
	}
	out, err := runCmd("sh", "-c", fmt.Sprintf("echo '%s' | lldb -p %d 2>&1", commands, pid))
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("lldb: %s — %s", err, out)}
	}
	return map[string]interface{}{"output": out, "pid": pid}
}

// ---------------------------------------------------------------------------
// Coredump management
// ---------------------------------------------------------------------------

func mcpCoredumpList() interface{} {
	out, err := runCmd("coredumpctl", "list", "--no-pager")
	if err != nil {
		// macOS fallback
		out, err = runCmd("ls", "-la", "/cores/")
		if err != nil {
			return map[string]interface{}{"error": "no coredump tool available. Linux: coredumpctl. macOS: /cores/"}
		}
		return map[string]interface{}{"cores": out, "location": "/cores/"}
	}
	return map[string]interface{}{"coredumps": out}
}

func mcpCoredumpInfo(pid string) interface{} {
	out, err := runCmd("coredumpctl", "info", pid)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"info": out}
}

// ---------------------------------------------------------------------------
// System logs (syslog, auth, etc.)
// ---------------------------------------------------------------------------

func mcpSyslog(logFile string, lines int, filter string) interface{} {
	if lines <= 0 {
		lines = 100
	}
	if logFile == "" {
		if runtime.GOOS == "darwin" {
			logFile = "/var/log/system.log"
		} else {
			logFile = "/var/log/syslog"
		}
	}
	var out string
	var err error
	if filter != "" {
		out, err = runCmd("sh", "-c", fmt.Sprintf("tail -%d %s | grep -i '%s'", lines*5, logFile, filter))
	} else {
		out, err = runCmd("tail", fmt.Sprintf("-%d", lines), logFile)
	}
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("cannot read %s: %s", logFile, err)}
	}
	return map[string]interface{}{"logs": out, "file": logFile}
}

func mcpAuthLog(lines int) interface{} {
	if lines <= 0 {
		lines = 50
	}
	// Try auth.log, secure, or macOS unified log
	out, err := runCmd("tail", fmt.Sprintf("-%d", lines), "/var/log/auth.log")
	if err != nil {
		out, err = runCmd("tail", fmt.Sprintf("-%d", lines), "/var/log/secure")
		if err != nil {
			if runtime.GOOS == "darwin" {
				out, _ = runCmd("log", "show", "--predicate", "eventMessage contains 'auth'", "--last", "1h", "--style", "compact")
			} else {
				out, _ = runCmd("journalctl", "-u", "sshd", "--no-pager", "-n", fmt.Sprintf("%d", lines))
			}
		}
	}
	return map[string]interface{}{"auth_logs": out}
}
