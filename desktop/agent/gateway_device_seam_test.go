package main

import "testing"

// TestDeviceDriverForTargetsConnectorDevice verifies the device-engine seam: a
// connector that pins a specific clone (c.Device) drives THAT adb serial, while a
// connector with no Device falls back to the broker's default driver.
func TestDeviceDriverForTargetsConnectorDevice(t *testing.T) {
	b := &broker{handlers: map[string]AuthMethod{}}
	b.register(newPasswordTotpHandler(nil, &redroidDeviceDriver{serial: "default-dev"}, newGateStore(nil)))

	// Pinned connector → targets its own clone serial.
	pinned := &Connector{ID: "bank", Engine: "device", Device: "clone-sim-7",
		Auth: ConnectorAuth{Method: "password_totp"}}
	drv, ok := b.deviceDriverFor(pinned)
	if !ok {
		t.Fatal("expected a device driver for the pinned connector")
	}
	rd, ok := drv.(*redroidDeviceDriver)
	if !ok || rd.serial != "clone-sim-7" {
		t.Fatalf("expected serial clone-sim-7, got %+v", drv)
	}

	// Unpinned connector → falls back to the registered default driver.
	unpinned := &Connector{ID: "misli", Engine: "redroid",
		Auth: ConnectorAuth{Method: "password_totp"}}
	drv2, ok := b.deviceDriverFor(unpinned)
	if !ok {
		t.Fatal("expected a device driver for the unpinned connector")
	}
	rd2, ok := drv2.(*redroidDeviceDriver)
	if !ok || rd2.serial != "default-dev" {
		t.Fatalf("expected default-dev fallback, got %+v", drv2)
	}
}
