package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestResolveIOSInstallMethodWithReason_AutoBundleOffMac(t *testing.T) {
	method, reason := resolveIOSInstallMethodWithReason(IOSInstallAuto)
	if runtime.GOOS == "darwin" && canDoNativeInstall() {
		if method != IOSInstallNative {
			t.Fatalf("expected native on macOS with Xcode, got %q", method)
		}
		if !strings.Contains(reason, "macOS + Xcode") {
			t.Fatalf("expected macOS/Xcode reason, got %q", reason)
		}
		return
	}
	if method != IOSInstallBundle {
		t.Fatalf("expected bundle when native iOS install is unavailable, got %q", method)
	}
	if reason == "" {
		t.Fatal("expected non-empty reason for bundle fallback")
	}
}

func TestResolveIOSInstallMethodWithReason_ExplicitBundle(t *testing.T) {
	method, reason := resolveIOSInstallMethodWithReason(IOSInstallBundle)
	if method != IOSInstallBundle {
		t.Fatalf("expected bundle, got %q", method)
	}
	if reason != "bundle requested explicitly" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestResolveIOSInstallMethodWithReason_ExplicitNative(t *testing.T) {
	method, reason := resolveIOSInstallMethodWithReason(IOSInstallNative)
	if method != IOSInstallNative {
		t.Fatalf("expected native, got %q", method)
	}
	if reason != "native requested explicitly" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}
