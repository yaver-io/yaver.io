package main

// Pure-string parser tests for the wireless additions: adb mDNS service
// listing, mDNS-host repair (0.0.0.0 → real LAN IP), wireless serial
// classification, and the Android getprop+meminfo enrichment parser.
//
// We deliberately don't shell out to adb / xcrun in these tests — that
// would hide subtle parser bugs behind environment differences. Each
// test feeds a literal fixture string captured from real devices into
// the parser and asserts the structured output.

import (
	"strings"
	"testing"
)

func TestRepairAdbMdnsHostFillsPairingFromConnect(t *testing.T) {
	in := []adbMdnsService{
		{Name: "adb-R52W60BEDXD-x6aGBg", Type: "_adb-tls-pairing._tcp", HostPort: "0.0.0.0:37921"},
		{Name: "adb-R52W60BEDXD-x6aGBg", Type: "_adb-tls-connect._tcp", HostPort: "192.168.111.7:39869"},
	}
	out := repairAdbMdnsHost(in)
	if got, want := out[0].HostPort, "192.168.111.7:37921"; got != want {
		t.Errorf("pairing host not patched: got %q, want %q", got, want)
	}
	if got, want := out[1].HostPort, "192.168.111.7:39869"; got != want {
		t.Errorf("connect host changed unexpectedly: got %q, want %q", got, want)
	}
}

func TestRepairAdbMdnsHostNoConnectKeepsZeros(t *testing.T) {
	// Pair shows up before the connect entry — adb sometimes splits
	// them across polling rounds. Make sure we don't synthesize a host.
	in := []adbMdnsService{
		{Name: "adb-XYZ-abcd", Type: "_adb-tls-pairing._tcp", HostPort: "0.0.0.0:5555"},
	}
	out := repairAdbMdnsHost(in)
	if got, want := out[0].HostPort, "0.0.0.0:5555"; got != want {
		t.Errorf("got %q, want %q (no connect entry → host stays 0.0.0.0)", got, want)
	}
}

func TestAdbSerialFromMdnsName(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"adb-R52W60BEDXD-x6aGBg", "R52W60BEDXD"},
		{"adb-FA8AB1A04054-NlPkRm", "FA8AB1A04054"},
		{"some-other-string", ""},
		{"adb-onlyserial", "onlyserial"},
	} {
		if got := adbSerialFromMdnsName(c.in); got != c.want {
			t.Errorf("adbSerialFromMdnsName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsAdbWirelessSerial(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"192.168.1.42:5555", true},
		{"10.0.0.5:37123", true},
		{"adb-R52W60BEDXD-x6aGBg._adb-tls-connect.", true},
		{"adb-R52W60BEDXD-x6aGBg._adb-tls-pairing.", true},
		{"emulator-5554", false},
		{"R52W60BEDXD", false},
		{"localhost", false},
		{"192.168.1.42:", false},
		{":5555", false},
	} {
		if got := isAdbWirelessSerial(c.in); got != c.want {
			t.Errorf("isAdbWirelessSerial(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Real-device fixture: Galaxy Tab S7 FE on Android 14.
const fixtureAndroidEnrichGalaxyTabS7FE = `##GETPROP##
[ro.boot.hardware]: [qcom]
[ro.build.version.release]: [14]
[ro.build.version.sdk]: [34]
[ro.product.board]: [lahaina]
[ro.product.brand]: [samsung]
[ro.product.cpu.abi]: [arm64-v8a]
[ro.product.device]: [gts7fewifi]
[ro.product.manufacturer]: [samsung]
[ro.product.model]: [SM-T733]
##DEVNAME##
Galaxy Tab S7 FE
##MEMINFO##
MemTotal:        3475708 kB
MemFree:          173740 kB
MemAvailable:     554024 kB
`

func TestParseAndroidEnrichOutputGalaxyTab(t *testing.T) {
	got := parseAndroidEnrichOutput(fixtureAndroidEnrichGalaxyTabS7FE)
	check := func(field, gotV, wantV string) {
		t.Helper()
		if gotV != wantV {
			t.Errorf("%s: got %q, want %q", field, gotV, wantV)
		}
	}
	check("DeviceName", got.DeviceName, "Galaxy Tab S7 FE")
	check("Manufacturer", got.Manufacturer, "samsung")
	check("Brand", got.Brand, "samsung")
	check("ModelCode", got.ModelCode, "SM-T733")
	check("OS", got.OS, "Android")
	check("OSVersion", got.OSVersion, "14")
	check("OSBuild(API)", got.OSBuild, "34")
	check("CPUType", got.CPUType, "arm64-v8a")
	if !strings.Contains(got.SoC, "Snapdragon") {
		t.Errorf("SoC: got %q, want a Snapdragon-family label", got.SoC)
	}
	wantRAM := uint64(3475708) * 1024
	if got.RAMBytes != wantRAM {
		t.Errorf("RAMBytes: got %d, want %d", got.RAMBytes, wantRAM)
	}
}

func TestParseAndroidEnrichOutputMissingDevName(t *testing.T) {
	// `settings get global device_name` returns "null" on devices
	// where the user never set a custom name — make sure we treat it
	// as empty (and fall back to the marketing name).
	in := `##GETPROP##
[ro.product.brand]: [google]
[ro.product.model]: [Pixel 7]
[ro.config.marketing_name]: [Pixel 7]
[ro.build.version.release]: [14]
##DEVNAME##
null
##MEMINFO##
MemTotal:        8000000 kB
`
	got := parseAndroidEnrichOutput(in)
	if got.DeviceName != "Pixel 7" {
		t.Errorf("DeviceName fallback: got %q, want %q", got.DeviceName, "Pixel 7")
	}
	if got.MarketingName != "Pixel 7" {
		t.Errorf("MarketingName: got %q, want %q", got.MarketingName, "Pixel 7")
	}
}

func TestMobileDeviceInfoSummary(t *testing.T) {
	for _, c := range []struct {
		name string
		in   mobileDeviceInfo
		want string
	}{
		{
			"full",
			mobileDeviceInfo{DeviceName: "Galaxy Tab S7 FE", OS: "Android", OSVersion: "14", RAMBytes: 4 * 1024 * 1024 * 1024},
			"Galaxy Tab S7 FE • Android 14 • 4.0 GB RAM",
		},
		{
			"no-ram",
			mobileDeviceInfo{DeviceName: "iPhone 14", OS: "iOS", OSVersion: "26.4.2"},
			"iPhone 14 • iOS 26.4.2",
		},
		{
			"only-marketing",
			mobileDeviceInfo{MarketingName: "Pixel 7"},
			"Pixel 7",
		},
		{
			"empty",
			mobileDeviceInfo{},
			"",
		},
	} {
		if got := c.in.summary(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Wireless detect dedupe: when adb shows the same device twice (IP form
// + mDNS form), the IP form should win. The fixture mirrors what we saw
// live on `adb devices -l`.
func TestWirelessDevicesDedupePrefersIPForm(t *testing.T) {
	// Synthetic call into the dedupe predicate — we replicate what
	// androidDevicesFromAdb does for the wireless branch since that
	// function shells to adb and we don't want a live dependency.
	type raw struct {
		serial   string
		deviceID string
	}
	raws := []raw{
		{"192.168.111.7:39869", "gts7fewifi"},
		{"adb-R52W60BEDXD-x6aGBg._adb-tls-connect.", "gts7fewifi"},
	}
	hasIP := map[string]bool{}
	for _, r := range raws {
		isMDNS := strings.Contains(r.serial, "._adb-tls-")
		if !isMDNS {
			hasIP[r.deviceID] = true
		}
	}
	var kept []string
	for _, r := range raws {
		isMDNS := strings.Contains(r.serial, "._adb-tls-")
		if isMDNS && hasIP[r.deviceID] {
			continue
		}
		kept = append(kept, r.serial)
	}
	if len(kept) != 1 || kept[0] != "192.168.111.7:39869" {
		t.Errorf("dedupe kept %v, want exactly [192.168.111.7:39869]", kept)
	}
}
