package testkit

import (
	"context"
	"testing"
	"time"
)

func TestNewUSBDeviceDriverPicksBackend(t *testing.T) {
	ios := NewUSBDeviceDriver(USBDevice{Platform: DevicePlatformIOS, UDID: "abc"})
	if ios.IOSSim == nil {
		t.Error("iOS device should get an IOSSimDriver backend")
	}
	if ios.Android != nil {
		t.Error("iOS device should not get an AndroidEmuDriver backend")
	}
	if ios.IOSSim.UDID != "abc" {
		t.Errorf("UDID not propagated: %q", ios.IOSSim.UDID)
	}

	andr := NewUSBDeviceDriver(USBDevice{Platform: DevicePlatformAndroid, UDID: "serial1"})
	if andr.Android == nil {
		t.Error("android device should get an AndroidEmuDriver backend")
	}
	if andr.IOSSim != nil {
		t.Error("android device should not get an IOSSimDriver backend")
	}
}

func TestUSBDeviceDriverVerifyUnknownPlatform(t *testing.T) {
	d := NewUSBDeviceDriver(USBDevice{Platform: "magic", UDID: "x"})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := d.Verify(ctx); err == nil {
		t.Error("expected error for unknown platform")
	}
}

func TestUSBDeviceDriverInstallUnknownPlatform(t *testing.T) {
	d := NewUSBDeviceDriver(USBDevice{Platform: "magic", UDID: "x"})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := d.Install(ctx, "/tmp/fake.apk"); err == nil {
		t.Error("expected error for unknown platform")
	}
}

func TestPickFreePortReturnsNonZero(t *testing.T) {
	p := pickFreePort()
	if p == 0 {
		t.Error("pickFreePort returned 0")
	}
}

func TestListUSBDevicesDoesNotPanic(t *testing.T) {
	// On CI runners without any device tools installed ListUSBDevices
	// should return an empty list cleanly, not panic or error.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := ListUSBDevices(ctx)
	if err != nil {
		t.Fatalf("ListUSBDevices err = %v", err)
	}
	if got == nil {
		// nil slice is fine, just shouldn't panic.
	}
}
