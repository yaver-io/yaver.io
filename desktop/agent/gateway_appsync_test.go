package main

// gateway_appsync_test.go — tests for app provisioning + sync (gateway_appsync.go).
//
// In-memory + fakes ONLY: a fakeAppInstaller stands in for the device/adb so
// the whole reconcile is exercised offline — NO vault/keychain, NO network, NO
// native build, NO real adb.
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"testing"
)

// fakeAppInstaller is the in-memory appInstaller double. installed seeds the
// "already present" set; installable lists packages PlayUIInstall/DeviceOwner
// will succeed on (and then mark installed); unavailable lists packages that
// report a Play block; failPlay/failDeviceOwner force a generic failure.
type fakeAppInstaller struct {
	installed    map[string]bool
	installable  map[string]bool
	unavailable  map[string]bool
	failInstall  map[string]bool
	playCalls    []string
	ownerCalls   []string
	loginCalls   int // MUST stay 0 — syncApps never logs in
	isInstallErr error
}

func newFakeAppInstaller() *fakeAppInstaller {
	return &fakeAppInstaller{
		installed:   map[string]bool{},
		installable: map[string]bool{},
		unavailable: map[string]bool{},
		failInstall: map[string]bool{},
	}
}

func (f *fakeAppInstaller) IsInstalled(pkg string) (bool, error) {
	if f.isInstallErr != nil {
		return false, f.isInstallErr
	}
	return f.installed[pkg], nil
}

func (f *fakeAppInstaller) PlayUIInstall(ctx context.Context, pkg string) error {
	f.playCalls = append(f.playCalls, pkg)
	return f.doInstall(pkg)
}

func (f *fakeAppInstaller) DeviceOwnerInstall(ctx context.Context, pkg string) error {
	f.ownerCalls = append(f.ownerCalls, pkg)
	return f.doInstall(pkg)
}

func (f *fakeAppInstaller) doInstall(pkg string) error {
	if f.unavailable[pkg] {
		return errAppUnavailable
	}
	if f.failInstall[pkg] {
		return context.DeadlineExceeded // a generic non-unavailable failure
	}
	if f.installable[pkg] {
		f.installed[pkg] = true
		return nil
	}
	// Default: pretend it installed.
	f.installed[pkg] = true
	return nil
}

func resultStatus(results []AppSyncResult, pkg string) string {
	for _, r := range results {
		if r.PackageID == pkg {
			return r.Status
		}
	}
	return ""
}

// TestGatewayAppSyncReconcile is the core reconcile: installs MISSING apps,
// SKIPS already-present ones, reports failed/unavailable gracefully.
func TestGatewayAppSyncReconcile(t *testing.T) {
	f := newFakeAppInstaller()
	f.installed["com.present.one"] = true   // already there → "already"
	f.installable["com.missing.one"] = true // missing, installs → "installed"
	f.unavailable["com.blocked.one"] = true // Play block → "unavailable"
	f.failInstall["com.broken.one"] = true  // transient failure → "failed"

	desired := []AppSpec{
		{PackageID: "com.present.one"},
		{PackageID: "com.missing.one"},
		{PackageID: "com.blocked.one"},
		{PackageID: "com.broken.one"},
	}

	results := syncApps(context.Background(), f, desired, appSyncModePlayUI)
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	if got := resultStatus(results, "com.present.one"); got != appSyncAlready {
		t.Errorf("present app: want %q, got %q", appSyncAlready, got)
	}
	if got := resultStatus(results, "com.missing.one"); got != appSyncInstalled {
		t.Errorf("missing app: want %q, got %q", appSyncInstalled, got)
	}
	if got := resultStatus(results, "com.blocked.one"); got != appSyncUnavailable {
		t.Errorf("blocked app: want %q, got %q", appSyncUnavailable, got)
	}
	if got := resultStatus(results, "com.broken.one"); got != appSyncFailed {
		t.Errorf("broken app: want %q, got %q", appSyncFailed, got)
	}

	// The already-present app must NOT have triggered an install attempt.
	for _, p := range f.playCalls {
		if p == "com.present.one" {
			t.Errorf("present app should not be installed, but PlayUIInstall was called for it")
		}
	}
	// And syncApps must NEVER attempt a login.
	if f.loginCalls != 0 {
		t.Errorf("syncApps attempted a login (%d calls) — install ≠ logged in must be kept separate", f.loginCalls)
	}
}

// TestGatewayAppSyncModeDispatch verifies play_ui vs device_owner route to the
// right install path.
func TestGatewayAppSyncModeDispatch(t *testing.T) {
	// play_ui (default) → PlayUIInstall.
	f := newFakeAppInstaller()
	f.installable["com.a"] = true
	syncApps(context.Background(), f, []AppSpec{{PackageID: "com.a"}}, appSyncModePlayUI)
	if len(f.playCalls) != 1 || len(f.ownerCalls) != 0 {
		t.Errorf("play_ui mode: want PlayUIInstall, got play=%v owner=%v", f.playCalls, f.ownerCalls)
	}

	// device_owner → DeviceOwnerInstall.
	g := newFakeAppInstaller()
	g.installable["com.b"] = true
	syncApps(context.Background(), g, []AppSpec{{PackageID: "com.b"}}, appSyncModeDeviceOwner)
	if len(g.ownerCalls) != 1 || len(g.playCalls) != 0 {
		t.Errorf("device_owner mode: want DeviceOwnerInstall, got play=%v owner=%v", g.playCalls, g.ownerCalls)
	}

	// Empty/unknown mode → default play_ui.
	h := newFakeAppInstaller()
	h.installable["com.c"] = true
	syncApps(context.Background(), h, []AppSpec{{PackageID: "com.c"}}, "")
	if len(h.playCalls) != 1 || len(h.ownerCalls) != 0 {
		t.Errorf("empty mode: want default PlayUIInstall, got play=%v owner=%v", h.playCalls, h.ownerCalls)
	}
}

// TestGatewayAppSyncUnavailableStops confirms a Play block is recorded as
// "unavailable" and we do NOT retry/sideload around it — exactly one install
// attempt for the blocked app.
func TestGatewayAppSyncUnavailableStops(t *testing.T) {
	f := newFakeAppInstaller()
	f.unavailable["com.blocked"] = true

	results := syncApps(context.Background(), f, []AppSpec{{PackageID: "com.blocked"}}, appSyncModePlayUI)
	if got := resultStatus(results, "com.blocked"); got != appSyncUnavailable {
		t.Fatalf("want %q, got %q", appSyncUnavailable, got)
	}
	if len(f.playCalls) != 1 {
		t.Errorf("blocked app should be attempted exactly once (no retry-spam/evasion), got %d attempts", len(f.playCalls))
	}
}

// TestGatewayAppSyncEmptyPackage handles a malformed spec gracefully.
func TestGatewayAppSyncEmptyPackage(t *testing.T) {
	f := newFakeAppInstaller()
	results := syncApps(context.Background(), f, []AppSpec{{PackageID: "  "}}, appSyncModePlayUI)
	if len(results) != 1 || results[0].Status != appSyncFailed {
		t.Fatalf("empty package id: want one %q result, got %+v", appSyncFailed, results)
	}
	if len(f.playCalls) != 0 {
		t.Errorf("empty package id should not trigger an install attempt")
	}
}

// TestGatewayAppSyncCheckError surfaces an IsInstalled error as "failed", not a
// crash or a bogus install.
func TestGatewayAppSyncCheckError(t *testing.T) {
	f := newFakeAppInstaller()
	f.isInstallErr = context.Canceled
	results := syncApps(context.Background(), f, []AppSpec{{PackageID: "com.x"}}, appSyncModePlayUI)
	if got := resultStatus(results, "com.x"); got != appSyncFailed {
		t.Fatalf("IsInstalled error: want %q, got %q", appSyncFailed, got)
	}
	if len(f.playCalls) != 0 {
		t.Errorf("must not attempt install when the installed-check errored")
	}
}

// TestGatewayAppSyncSummary tallies outcomes with a deterministic key set.
func TestGatewayAppSyncSummary(t *testing.T) {
	results := []AppSyncResult{
		{PackageID: "a", Status: appSyncInstalled},
		{PackageID: "b", Status: appSyncAlready},
		{PackageID: "c", Status: appSyncAlready},
		{PackageID: "d", Status: appSyncUnavailable},
	}
	sum := appSyncSummary(results)
	if sum[appSyncInstalled] != 1 || sum[appSyncAlready] != 2 || sum[appSyncUnavailable] != 1 || sum[appSyncFailed] != 0 {
		t.Errorf("summary tally wrong: %+v", sum)
	}
}

// TestGatewayNodeAppSetRoundTrip exercises the local-first persistence using an
// isolated HOME so no real ~/.yaver is touched. No keychain/network.
func TestGatewayNodeAppSetRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // ConfigDir() resolves via os.UserHomeDir() ($HOME on unix)
	t.Setenv("USERPROFILE", home) // windows equivalent (harmless on unix)

	set := NodeAppSet{NodeID: "node-1", Apps: []AppSpec{
		{PackageID: "com.one", Required: true},
		{PackageID: "com.two"},
	}}
	if err := saveNodeAppSet(set); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadNodeAppSet("node-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.NodeID != "node-1" || len(got.Apps) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.Apps[0].Required || got.Apps[0].PackageID != "com.one" {
		t.Errorf("required flag/order not preserved: %+v", got.Apps)
	}

	// A node with no stored set reads as an empty desired set (not an error).
	empty, err := loadNodeAppSet("node-unknown")
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if empty.NodeID != "node-unknown" || len(empty.Apps) != 0 {
		t.Errorf("missing set should be empty, got %+v", empty)
	}
}
