package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var (
	hwIDOnce  sync.Once
	hwIDValue string
)

// HardwareID returns a stable, unchanging identifier for this physical machine.
// Priority:
//  1. macOS: IOPlatformUUID (from ioreg — hardware UUID burned into firmware)
//  2. Linux: /sys/class/dmi/id/product_uuid (root) or /etc/machine-id (all users)
//  3. Windows: SMBIOS UUID from wmic
//  4. Fallback: SHA256 of sorted MAC addresses (WiFi + Ethernet)
//
// The result is cached — computed only once per process lifetime.
// This NEVER leaves the local network. Not sent to Convex.
func HardwareID() string {
	hwIDOnce.Do(func() {
		var id string
		switch runtime.GOOS {
		case "darwin":
			id = macOSHardwareUUID()
		case "linux":
			id = linuxHardwareID()
		case "windows":
			id = windowsHardwareID()
		}
		if id == "" {
			id = macAddressFallback()
		}
		if id == "" {
			id = "unknown"
		}
		// Hash it to normalize length and avoid leaking raw UUIDs
		h := sha256.Sum256([]byte(id))
		hwIDValue = fmt.Sprintf("%x", h[:16]) // 32 hex chars
		log.Printf("[hwid] Hardware ID: %s (source: %s/%s)", hwIDValue[:8]+"...", runtime.GOOS, hwIDSource(id))
	})
	return hwIDValue
}

func hwIDSource(raw string) string {
	if strings.Contains(raw, "-") {
		return "uuid"
	}
	if strings.Contains(raw, ":") {
		return "mac"
	}
	return "id"
}

// macOSHardwareUUID gets the IOPlatformUUID — burned into Mac firmware, never changes.
func macOSHardwareUUID() string {
	out, err := exec.Command("ioreg", "-d2", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "IOPlatformUUID") {
			// Extract UUID from: "IOPlatformUUID" = "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX"
			parts := strings.Split(line, "\"")
			for i, p := range parts {
				if p == "IOPlatformUUID" && i+2 < len(parts) {
					uuid := strings.TrimSpace(parts[i+2])
					if len(uuid) >= 32 {
						return uuid
					}
				}
			}
		}
	}
	return ""
}

// linuxHardwareID tries DMI product UUID first, then machine-id.
func linuxHardwareID() string {
	// Try DMI product UUID (requires root on some distros)
	if out, err := exec.Command("cat", "/sys/class/dmi/id/product_uuid").Output(); err == nil {
		id := strings.TrimSpace(string(out))
		if len(id) >= 32 && id != "Not Settable" && !strings.HasPrefix(id, "0000") {
			return id
		}
	}
	// Try machine-id (stable across boots, changes on OS reinstall)
	if out, err := exec.Command("cat", "/etc/machine-id").Output(); err == nil {
		id := strings.TrimSpace(string(out))
		if len(id) >= 16 {
			return id
		}
	}
	// Try /var/lib/dbus/machine-id as fallback
	if out, err := exec.Command("cat", "/var/lib/dbus/machine-id").Output(); err == nil {
		id := strings.TrimSpace(string(out))
		if len(id) >= 16 {
			return id
		}
	}
	return ""
}

// windowsHardwareID gets SMBIOS UUID via wmic.
func windowsHardwareID() string {
	out, err := exec.Command("wmic", "csproduct", "get", "UUID").Output()
	if err != nil {
		// Try PowerShell fallback
		out, err = exec.Command("powershell", "-Command",
			"(Get-CimInstance -ClassName Win32_ComputerSystemProduct).UUID").Output()
		if err != nil {
			return ""
		}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && line != "UUID" && !strings.HasPrefix(line, "0000") {
			return line
		}
	}
	return ""
}

// macAddressFallback computes SHA256 of sorted MAC addresses.
// Uses all non-loopback, non-virtual interfaces (WiFi + Ethernet).
func macAddressFallback() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var macs []string
	for _, iface := range ifaces {
		// Skip loopback, virtual, and down interfaces
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		mac := iface.HardwareAddr.String()
		if mac == "" {
			continue
		}
		// Skip common virtual interface prefixes
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") ||
			strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr") ||
			strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "awdl") ||
			strings.HasPrefix(name, "llw") || strings.HasPrefix(name, "anpi") {
			continue
		}
		macs = append(macs, mac)
	}
	if len(macs) == 0 {
		return ""
	}
	sort.Strings(macs)
	return strings.Join(macs, "+")
}
