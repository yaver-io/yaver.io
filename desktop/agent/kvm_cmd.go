package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func printKVMUsage() {
	fmt.Println(`yaver kvm — local-first physical PC appliance

  yaver kvm discover
  yaver kvm pair --auto --token-file <owner-only-file>
  yaver kvm pair --device-id <m5-id> --token-file <owner-only-file>
  yaver kvm pair --url http://<m5-ip>:8348 --token-file <owner-only-file>
  yaver kvm status
  yaver kvm doctor
  yaver kvm capture list
  yaver kvm capture select --device /dev/v4l/by-id/<capture>
  yaver kvm release-all
  yaver kvm firmware install --file <verified-ota.bin>
  yaver kvm unpair

The v0 kit is Raspberry Pi 4 + UVC HDMI capture + M5Stack AtomS3U.
Pairing, frames, leases and actions stay local; Convex is not the data plane.`)
}

func printKVMJSON(value any, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(raw))
}

func runKVMCmd(args []string) {
	if len(args) == 0 {
		printKVMUsage()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	switch args[0] {
	case "discover":
		discoverCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		devices, err := discoverPhysicalKVM(discoverCtx)
		printKVMJSON(map[string]any{"devices": devices}, err)
	case "pair":
		fs := flag.NewFlagSet("kvm pair", flag.ContinueOnError)
		rawURL := fs.String("url", "", "M5Stack bridge URL")
		deviceID := fs.String("device-id", "", "announced M5Stack device id")
		auto := fs.Bool("auto", false, "pair the only nearby unpaired keyboard-mode bridge")
		tokenFile := fs.String("token-file", "", "owner-only file containing the pairing token")
		if err := fs.Parse(args[1:]); err != nil {
			os.Exit(2)
		}
		locators := 0
		if *rawURL != "" {
			locators++
		}
		if *deviceID != "" {
			locators++
		}
		if *auto {
			locators++
		}
		if *tokenFile == "" || locators != 1 {
			fmt.Fprintln(os.Stderr, "usage: yaver kvm pair (--auto | --device-id <id> | --url <url>) --token-file <owner-only-file>")
			os.Exit(2)
		}
		resolvedURL, err := resolveKVMPairURL(ctx, *rawURL, *deviceID, *auto)
		if err != nil {
			printKVMJSON(nil, err)
		}
		info, err := os.Stat(*tokenFile)
		if err != nil {
			printKVMJSON(nil, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			printKVMJSON(nil, fmt.Errorf("pairing token file must be owner-only (chmod 600 %s)", *tokenFile))
		}
		token, err := os.ReadFile(*tokenFile)
		if err != nil {
			printKVMJSON(nil, err)
		}
		status, err := physicalKVM.pair(ctx, resolvedURL, string(token))
		printKVMJSON(map[string]any{"ok": err == nil, "deviceId": status.DeviceID, "usbReady": status.USBReady, "armed": status.Armed}, err)
	case "status":
		status, err := physicalKVMFullStatus(ctx)
		if err != nil {
			status["error"] = err.Error()
		}
		printKVMJSON(status, nil)
	case "doctor":
		result, err := runPhysicalKVMDoctor(ctx)
		printKVMJSON(result, err)
	case "capture":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver kvm capture list | capture select --device <path>")
			os.Exit(2)
		}
		switch args[1] {
		case "list":
			selected, err := physicalKVM.selectedCaptureDevice()
			printKVMJSON(map[string]any{"selectedDevice": selected, "devices": captureDevices()}, err)
		case "select":
			fs := flag.NewFlagSet("kvm capture select", flag.ContinueOnError)
			device := fs.String("device", "", "advertised /dev video path")
			if err := fs.Parse(args[2:]); err != nil || *device == "" {
				fmt.Fprintln(os.Stderr, "usage: yaver kvm capture select --device <path>")
				os.Exit(2)
			}
			err := physicalKVM.selectCaptureDevice(*device)
			printKVMJSON(map[string]any{"ok": err == nil, "selectedDevice": filepath.Clean(*device)}, err)
		default:
			fmt.Fprintln(os.Stderr, "usage: yaver kvm capture list | capture select --device <path>")
			os.Exit(2)
		}
	case "release-all":
		err := physicalKVM.releaseAll(ctx)
		printKVMJSON(map[string]any{"ok": err == nil, "released": err == nil}, err)
	case "unpair":
		err := physicalKVM.unpair(ctx)
		printKVMJSON(map[string]any{"ok": err == nil}, err)
	case "firmware":
		if len(args) < 2 || args[1] != "install" {
			fmt.Fprintln(os.Stderr, "usage: yaver kvm firmware install --file <verified-ota.bin>")
			os.Exit(2)
		}
		fs := flag.NewFlagSet("kvm firmware install", flag.ContinueOnError)
		path := fs.String("file", "", "verified Yaver AtomS3U OTA image")
		if err := fs.Parse(args[2:]); err != nil || *path == "" {
			fmt.Fprintln(os.Stderr, "usage: yaver kvm firmware install --file <verified-ota.bin>")
			os.Exit(2)
		}
		image, err := os.ReadFile(*path)
		if err != nil {
			printKVMJSON(nil, err)
		}
		checksum, err := physicalKVM.installFirmware(ctx, image)
		printKVMJSON(map[string]any{"ok": err == nil, "sha256": checksum, "restarting": err == nil}, err)
	default:
		printKVMUsage()
		os.Exit(2)
	}
}

func resolveKVMPairURL(ctx context.Context, rawURL, deviceID string, auto bool) (string, error) {
	if strings.TrimSpace(rawURL) != "" {
		return rawURL, nil
	}
	discoverCtx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()
	devices, err := discoverPhysicalKVM(discoverCtx)
	if err != nil {
		return "", err
	}
	matches := make([]physicalKVMDiscovery, 0, 1)
	for _, item := range devices {
		if deviceID != "" && item.DeviceID != deviceID {
			continue
		}
		if auto && (item.Mode != "keyboard" || item.PairingState != "unpaired") {
			continue
		}
		matches = append(matches, item)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("KVM_PAIRING_AMBIGUOUS: found %d matching bridges; use --device-id after `yaver kvm discover`", len(matches))
	}
	port := matches[0].Port
	if port == 0 {
		port = 8348
	}
	return "http://" + net.JoinHostPort(matches[0].Address, fmt.Sprint(port)), nil
}
