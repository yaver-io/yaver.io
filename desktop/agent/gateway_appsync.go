package main

// gateway_appsync.go — app provisioning + SYNC onto a device node.
//
// The user lists the apps they want once (a desired NodeAppSet) and Yaver
// installs them all — no one-by-one manual install. This is the provisioning
// half of docs §13 ("App sync / provisioning"): get the right apps ONTO the
// device node so the gateway broker (gateway_redroid.go) can later log into each
// one. It is engine-agnostic — it drives a `deviceDriver` (gateway_redroid.go),
// so the same reconcile works for a redroid node OR a real phone.
//
// TWO INSTALL PATHS (both Play-sourced — NEVER a sideloaded random APK):
//
//	Path 1 — "play_ui" (default): open market://details?id=<pkg> via an intent and
//	  drive the Play Store UI with the deviceDriver — find + tap the Install
//	  button (UiTexts/Tap), wait for completion. Works on an un-rooted phone /
//	  vanilla redroid with no special privileges.
//	Path 2 — "device_owner": SILENT install via a device-owner / pm /
//	  PackageInstaller path (adb-backed). Needs the node enrolled as a managed
//	  device (an onboarding step, OUT OF SCOPE here) — we scaffold the call behind
//	  the interface so the higher tier can drop in without touching reconcile.
//
// install ≠ logged in.  syncApps ONLY installs apps. The per-app auth-broker
// login (gateway_redroid.go's passwordTotpHandler) runs SEPARATELY, AFTERWARDS,
// once per app. syncApps NEVER attempts a login — that keeps "what's on the
// device" cleanly separated from "what's signed in", and lets the user provision
// a node now and authorize each app on its own schedule.
//
// POLICY GUARD (CLAUDE.md): Play Store is the only install source (trusted +
// honest). On a Play block / region-lock we record the app as "unavailable",
// STOP, and never sideload-around it or rotate to evade the block.
//
// LOCAL-FIRST: a node's desired NodeAppSet persists to
// ~/.yaver/nodes/<nodeID>/apps.json — never Convex.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── desired-set + result types ───────────────────────────────────────────────

// AppSpec is one app the user wants on a node, by its Play/Android package id.
// Required marks an app whose absence should be surfaced prominently (the
// reconcile still attempts the optional ones, but a caller can tell them apart).
type AppSpec struct {
	PackageID string `json:"packageId"`
	Required  bool   `json:"required,omitempty"`
}

// NodeAppSet is the desired set of apps for one device node. Persisted locally
// at ~/.yaver/nodes/<nodeID>/apps.json — never Convex.
type NodeAppSet struct {
	NodeID string    `json:"nodeId"`
	Apps   []AppSpec `json:"apps"`
}

// AppSyncResult is the per-app outcome of a reconcile.
//
//	"installed"   — was missing, we installed it.
//	"already"     — already present, nothing to do.
//	"failed"      — install attempted and errored (transient/UI/driver).
//	"unavailable" — Play block / region-lock / not on the store. We STOP on this
//	                app (no retry-spam, no sideload-around) and record it.
type AppSyncResult struct {
	PackageID string `json:"packageId"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}

// App-sync status constants (avoid magic strings across the file + tests).
const (
	appSyncInstalled   = "installed"
	appSyncAlready     = "already"
	appSyncFailed      = "failed"
	appSyncUnavailable = "unavailable"
)

// Install modes selected by the caller / syncApps default.
const (
	appSyncModePlayUI      = "play_ui"
	appSyncModeDeviceOwner = "device_owner"
)

// errAppUnavailable is the sentinel an installer returns when Play reports the
// app is blocked / region-locked / absent — a "no", not a transient failure.
// syncApps maps it to "unavailable" and STOPS on that app (never evades).
var errAppUnavailable = fmt.Errorf("app unavailable on the Play Store (blocked, region-locked, or not published)")

// ── installer interface (the DI seam) ────────────────────────────────────────

// appInstaller abstracts the install surface so syncApps is unit-testable with a
// fake — no real adb, no device, no network. The production impl
// (androidAppInstaller) wraps a deviceDriver + adb; tests inject fakeAppInstaller.
type appInstaller interface {
	// IsInstalled reports whether the package is already present on the node.
	IsInstalled(pkg string) (bool, error)
	// PlayUIInstall is Path 1 — open market://details?id=<pkg> and drive the Play
	// Store UI (find + tap Install via UiTexts/Tap, wait for completion). Returns
	// errAppUnavailable on a Play block / region-lock / not-found.
	PlayUIInstall(ctx context.Context, pkg string) error
	// DeviceOwnerInstall is Path 2 — a SILENT install via device-owner / pm /
	// PackageInstaller. Returns errAppUnavailable when the store has no such app.
	DeviceOwnerInstall(ctx context.Context, pkg string) error
}

// ── reconcile / sync ─────────────────────────────────────────────────────────

// syncApps reconciles a desired app-set onto a node via inst. For each app:
//   - already present              → "already"
//   - missing, installs OK         → "installed"
//   - missing, install errors      → "failed"
//   - Play block / region-lock     → "unavailable" (STOP on that app; no evade)
//
// mode selects the install path: "play_ui" (default) or "device_owner".
//
// syncApps ONLY installs — it NEVER attempts a login. The auth-broker login
// (gateway_redroid.go) runs separately, once per app, afterwards. Provisioning
// (apps present) and authorization (apps signed in) are deliberately decoupled.
func syncApps(ctx context.Context, inst appInstaller, desired []AppSpec, mode string) []AppSyncResult {
	mode = normalizeAppSyncMode(mode)
	results := make([]AppSyncResult, 0, len(desired))
	for _, spec := range desired {
		pkg := strings.TrimSpace(spec.PackageID)
		if pkg == "" {
			results = append(results, AppSyncResult{PackageID: spec.PackageID, Status: appSyncFailed, Detail: "empty package id"})
			continue
		}

		present, err := inst.IsInstalled(pkg)
		if err != nil {
			results = append(results, AppSyncResult{PackageID: pkg, Status: appSyncFailed, Detail: "check installed: " + err.Error()})
			continue
		}
		if present {
			results = append(results, AppSyncResult{PackageID: pkg, Status: appSyncAlready})
			continue
		}

		var installErr error
		switch mode {
		case appSyncModeDeviceOwner:
			installErr = inst.DeviceOwnerInstall(ctx, pkg)
		default: // appSyncModePlayUI
			installErr = inst.PlayUIInstall(ctx, pkg)
		}

		switch {
		case installErr == nil:
			results = append(results, AppSyncResult{PackageID: pkg, Status: appSyncInstalled})
		case isAppUnavailable(installErr):
			// A Play block / region-lock is a "no" — record it and STOP on this
			// app. NEVER sideload around it or rotate to evade the block.
			results = append(results, AppSyncResult{PackageID: pkg, Status: appSyncUnavailable, Detail: installErr.Error()})
		default:
			results = append(results, AppSyncResult{PackageID: pkg, Status: appSyncFailed, Detail: installErr.Error()})
		}
	}
	return results
}

// normalizeAppSyncMode coerces an empty/unknown mode to the safe default
// ("play_ui" — works without device-owner enrollment).
func normalizeAppSyncMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case appSyncModeDeviceOwner:
		return appSyncModeDeviceOwner
	default:
		return appSyncModePlayUI
	}
}

// isAppUnavailable reports whether err signals a Play block / region-lock / not
// published (the errAppUnavailable sentinel, directly or wrapped).
func isAppUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if err == errAppUnavailable {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unavailable")
}

// ── production installer (deviceDriver + adb) ────────────────────────────────

// androidAppInstaller is the production appInstaller. It drives the Play Store
// UI through a deviceDriver for Path 1, and shells adb/pm through the device
// serial for Path 2. serial targets the redroid/phone for the adb-backed checks;
// driver is the same surface gateway_redroid.go uses.
type androidAppInstaller struct {
	serial string
	driver deviceDriver
}

func newAndroidAppInstaller(serial string, driver deviceDriver) *androidAppInstaller {
	return &androidAppInstaller{serial: serial, driver: driver}
}

// IsInstalled checks `pm list packages` for an exact package match.
func (a *androidAppInstaller) IsInstalled(pkg string) (bool, error) {
	pkgs, err := droidInstalledPackages(a.serial, pkg, 50)
	if err != nil {
		return false, err
	}
	for _, p := range pkgs {
		if p == pkg {
			return true, nil
		}
	}
	return false, nil
}

// PlayUIInstall opens the Play Store deep-link for the package and drives the UI
// to tap Install, then waits (polling for the package to appear). Play-sourced
// ONLY — no APK is ever downloaded/sideloaded by this path.
func (a *androidAppInstaller) PlayUIInstall(ctx context.Context, pkg string) error {
	if a.driver == nil {
		return fmt.Errorf("no device driver available for Play UI install")
	}
	// Deep-link straight to the app's store page; the Play app handles the intent.
	if err := a.driver.LaunchURL("market://details?id=" + pkg); err != nil {
		return fmt.Errorf("open play page for %q: %w", pkg, err)
	}
	// Give the store page a moment to render before reading the screen.
	if !sleepCtx(ctx, 2*time.Second) {
		return ctx.Err()
	}

	// Tap the Install/Get button. The store labels it "Install" (or "Get"); a
	// region/availability block shows "This item isn't available…" / "not
	// available in your country" instead — that's a Play "no", not a failure to
	// retry. We read the screen, classify, then act.
	if unavailable, err := a.playPageUnavailable(); err != nil {
		return err
	} else if unavailable {
		return errAppUnavailable
	}
	if err := a.driver.Tap("Install"); err != nil {
		return fmt.Errorf("tap Install for %q: %w", pkg, err)
	}

	// Poll for completion: the package shows up in `pm list packages`. Bounded —
	// no infinite retry-spam.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, 3*time.Second) {
			return ctx.Err()
		}
		if ok, _ := a.IsInstalled(pkg); ok {
			return nil
		}
	}
	return fmt.Errorf("install of %q did not complete within timeout", pkg)
}

// playPageUnavailable reads the visible Play page text and reports whether it
// shows a region/availability block (a "no" we must record + stop on).
func (a *androidAppInstaller) playPageUnavailable() (bool, error) {
	nodes, err := a.driver.UiTexts()
	if err != nil {
		return false, fmt.Errorf("read play page: %w", err)
	}
	for _, n := range nodes {
		t := strings.ToLower(n.Text)
		for _, marker := range []string{
			"isn't available", "is not available", "not available in your country",
			"item isn't available", "this app is not available",
		} {
			if strings.Contains(t, marker) {
				return true, nil
			}
		}
	}
	return false, nil
}

// DeviceOwnerInstall is the SILENT install path (Path 2). It requires the node
// to be enrolled as a device-owner / managed device, which is an ONBOARDING step
// (dpm set-device-owner) intentionally OUT OF SCOPE of this slice. We scaffold
// the call so the higher tier can drop the real PackageInstaller session in here
// without touching syncApps; until then we report it clearly rather than fake a
// guarantee.
func (a *androidAppInstaller) DeviceOwnerInstall(ctx context.Context, pkg string) error {
	// A real impl would: open a Play "install" request through the managed-Google-
	// Play API (Play-sourced), or run a PackageInstaller session as the device
	// owner. Both require enrollment that lands in onboarding.
	return fmt.Errorf("device-owner silent install for %q requires the node enrolled as a managed device (onboarding step); use mode %q meanwhile", pkg, appSyncModePlayUI)
}

// (sleepCtx — "sleep for d or until ctx ends, returns false if ctx ended first"
// — is provided by provision.go and reused here.)

// ── desired-set persistence (LOCAL-FIRST, never Convex) ──────────────────────

// gatewayNodeAppsPath returns ~/.yaver/nodes/<nodeID>/apps.json, validating the
// node id so it can never escape the nodes directory.
func gatewayNodeAppsPath(nodeID string) (string, error) {
	if err := validateConnectorID(nodeID); err != nil { // reuse the fs-safe id check
		return "", fmt.Errorf("node id: %w", err)
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nodes", nodeID, "apps.json"), nil
}

// saveNodeAppSet persists a node's desired app-set locally (atomic write).
func saveNodeAppSet(set NodeAppSet) error {
	if strings.TrimSpace(set.NodeID) == "" {
		return fmt.Errorf("nodeId is required")
	}
	path, err := gatewayNodeAppsPath(set.NodeID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create node dir: %w", err)
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal node app set: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write node app set tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename node app set: %w", err)
	}
	return nil
}

// loadNodeAppSet loads a node's desired app-set. A missing file is not an error
// — it returns an empty set so a fresh node reads as "nothing desired yet".
func loadNodeAppSet(nodeID string) (NodeAppSet, error) {
	path, err := gatewayNodeAppsPath(nodeID)
	if err != nil {
		return NodeAppSet{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NodeAppSet{NodeID: nodeID}, nil
		}
		return NodeAppSet{}, fmt.Errorf("read node app set: %w", err)
	}
	var set NodeAppSet
	if err := json.Unmarshal(data, &set); err != nil {
		return NodeAppSet{}, fmt.Errorf("parse node app set: %w", err)
	}
	if set.NodeID == "" {
		set.NodeID = nodeID
	}
	return set, nil
}

// ── MCP entrypoints (mirror gateway_query) ───────────────────────────────────

// gatewayNodeInstallerFor builds the production installer for a node. The node
// id IS the adb serial of the redroid/phone in this slice (the same id the
// device driver targets). It is a thin seam so the MCP handlers stay tiny and
// the install plumbing is reused.
func gatewayNodeInstallerFor(nodeID string) appInstaller {
	return newAndroidAppInstaller(nodeID, &redroidDeviceDriver{serial: nodeID})
}

// mcpGatewayNodeApps lists a node's desired app-set and, best-effort, which of
// those apps are currently installed. Pure-listing — no install side effects.
func mcpGatewayNodeApps(nodeID string) interface{} {
	if strings.TrimSpace(nodeID) == "" {
		return map[string]interface{}{"error": "node is required"}
	}
	set, err := loadNodeAppSet(nodeID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	inst := gatewayNodeInstallerFor(nodeID)
	apps := make([]map[string]interface{}, 0, len(set.Apps))
	for _, spec := range set.Apps {
		present, perr := inst.IsInstalled(spec.PackageID)
		entry := map[string]interface{}{
			"packageId": spec.PackageID,
			"required":  spec.Required,
			"installed": present,
		}
		if perr != nil {
			entry["installed"] = nil
			entry["note"] = "could not check: " + perr.Error()
		}
		apps = append(apps, entry)
	}
	return map[string]interface{}{
		"node":  nodeID,
		"count": len(apps),
		"apps":  apps,
		"note":  "Desired app-set for this node (local only, never Convex). install ≠ logged in — run the auth broker per app afterwards.",
	}
}

// mcpGatewayNodeSync runs syncApps for a node's desired set (persisting the set
// first when the caller supplies one) and returns per-app results. This is the
// "list once → install them all" entry point.
func mcpGatewayNodeSync(nodeID string, apps []AppSpec, mode string) interface{} {
	if strings.TrimSpace(nodeID) == "" {
		return map[string]interface{}{"error": "node is required"}
	}
	// If the caller passed a desired set, persist it as the node's new desired
	// set; otherwise reconcile whatever is already on disk.
	if len(apps) > 0 {
		if err := saveNodeAppSet(NodeAppSet{NodeID: nodeID, Apps: apps}); err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	set, err := loadNodeAppSet(nodeID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if len(set.Apps) == 0 {
		return map[string]interface{}{
			"node": nodeID, "count": 0, "results": []AppSyncResult{},
			"note": "No desired apps for this node. Pass apps[] to set + sync.",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	inst := gatewayNodeInstallerFor(nodeID)
	results := syncApps(ctx, inst, set.Apps, mode)

	return map[string]interface{}{
		"node":    nodeID,
		"mode":    normalizeAppSyncMode(mode),
		"count":   len(results),
		"results": results,
		"summary": appSyncSummary(results),
		"note":    "Installed only — install ≠ logged in. Authorize each app via the auth broker (gateway login) separately.",
	}
}

// mcpGatewayNodeInstall installs ONE package on a node (a manual one-off on top
// of the bulk sync). It also records the app in the node's desired set so a
// later sync keeps it provisioned.
func mcpGatewayNodeInstall(nodeID, pkg, mode string) interface{} {
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(pkg) == "" {
		return map[string]interface{}{"error": "node and package are required"}
	}
	// Add to the desired set (idempotent) so it survives the next reconcile.
	set, err := loadNodeAppSet(nodeID)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if !appSetContains(set.Apps, pkg) {
		set.Apps = append(set.Apps, AppSpec{PackageID: pkg})
		if err := saveNodeAppSet(set); err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst := gatewayNodeInstallerFor(nodeID)
	results := syncApps(ctx, inst, []AppSpec{{PackageID: pkg}}, mode)
	res := AppSyncResult{PackageID: pkg, Status: appSyncFailed}
	if len(results) == 1 {
		res = results[0]
	}
	return map[string]interface{}{
		"node":    nodeID,
		"mode":    normalizeAppSyncMode(mode),
		"package": pkg,
		"status":  res.Status,
		"detail":  res.Detail,
		"note":    "Installed only — install ≠ logged in. Authorize via the auth broker separately.",
	}
}

// appSetContains reports whether a desired set already lists pkg.
func appSetContains(apps []AppSpec, pkg string) bool {
	for _, a := range apps {
		if a.PackageID == pkg {
			return true
		}
	}
	return false
}

// appSyncSummary tallies the per-app outcomes for a terse top-line.
func appSyncSummary(results []AppSyncResult) map[string]int {
	sum := map[string]int{}
	for _, r := range results {
		sum[r.Status]++
	}
	// Deterministic key set even when zero (nice for callers/tests).
	for _, k := range []string{appSyncInstalled, appSyncAlready, appSyncFailed, appSyncUnavailable} {
		if _, ok := sum[k]; !ok {
			sum[k] = 0
		}
	}
	return sum
}
