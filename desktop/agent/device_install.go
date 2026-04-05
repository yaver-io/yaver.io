package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// installAppOnDevice builds and installs an .app on a connected iOS device.
// appPath should be the path to a .app bundle (not .ipa).
// Returns the device UDID and any error.
func installAppOnDevice(ctx context.Context, appPath string) (string, error) {
	udid := detectIOSDevice(ctx)
	if udid == "" {
		return "", fmt.Errorf("no iOS device detected — is the device connected via USB or WiFi-paired in Xcode?")
	}

	log.Printf("[device-install] installing %s on device %s...", appPath, udid[:8])

	cmd := exec.CommandContext(ctx, "xcrun", "devicectl", "device", "install", "app", "--device", udid, appPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return udid, fmt.Errorf("xcrun devicectl install failed: %v\n%s", err, string(out))
	}

	log.Printf("[device-install] installed on device %s: %s", udid[:8], strings.TrimSpace(string(out)))
	return udid, nil
}

// launchAppOnDevice launches an installed app by bundle ID on the device.
func launchAppOnDevice(ctx context.Context, udid, bundleID string) error {
	cmd := exec.CommandContext(ctx, "xcrun", "devicectl", "device", "process", "launch", "--device", udid, bundleID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xcrun devicectl launch failed: %v\n%s", err, string(out))
	}
	log.Printf("[device-install] launched %s on %s", bundleID, udid[:8])
	return nil
}

// isDirectConnection checks if the HTTP request came directly (LAN) vs through relay.
// Relay/nginx adds X-Forwarded-For; direct connections don't have it.
func isDirectConnection(r *http.Request) bool {
	// If X-Forwarded-For is present, request came through relay/proxy
	if r.Header.Get("X-Forwarded-For") != "" {
		return false
	}

	// Extract IP from RemoteAddr (host:port)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// Check if it's a private/loopback IP (same LAN)
	return ip.IsLoopback() || ip.IsPrivate()
}

// detectIOSDeviceWithTimeout is a convenience wrapper with a 10s timeout.
func detectIOSDeviceWithTimeout() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return detectIOSDevice(ctx)
}

// findAppInDerivedData searches for a .app bundle in the xcodebuild derived data output.
func findAppInDerivedData(workDir string) string {
	// Standard locations for xcodebuild output
	patterns := []string{
		"build/DerivedData/Build/Products/Release-iphoneos/*.app",
		"build/DerivedData/Build/Products/Debug-iphoneos/*.app",
		"ios/build/Build/Products/Release-iphoneos/*.app",
		"ios/build/Build/Products/Debug-iphoneos/*.app",
	}
	return detectArtifact(workDir, patterns)
}
