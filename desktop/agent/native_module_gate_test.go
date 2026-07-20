package main

import (
	"strings"
	"testing"
)

// The Hermes compat gate decides whether a guest bundle may load into the mobile
// host. Until 2026-07-20 it treated a MISSING native module exactly like a
// MISMATCHED one, and that conflation is the bug these tests exist to prevent
// coming back.
//
// The incident: talos/mobile declares `expo-gl`, which Yaver's host does not
// register. Both of its call sites (Cell3D.tsx, SpatialBackdrop.tsx) wrap
// `require("expo-gl")` in try/catch, and expo-gl binds its native module at
// import time — so the throw lands inside the guest's own catch and the screen
// renders a fallback. Nothing crashes. Yaver blocked the load anyway, telling the
// user "it would crash at runtime" and advising them to "guard unsupported call
// sites before retrying" — which they had already done.
//
// A behavioural test would need the full /dev/build-native handler plus a real
// guest project on disk, so the invariant is asserted where it is decided.

// gateSource returns the compat-gate decision region of devserver_http.go.
func gateSource(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "devserver_http.go")
	i := strings.Index(src, "blocksLoad :=")
	if i < 0 {
		t.Fatal("blocksLoad not found in devserver_http.go — the compat gate was renamed or restructured; update this test deliberately")
	}
	return src[i : i+400]
}

// A missing module throws only IF CALLED, and a guarded require never reaches
// the throw. Blocking on it makes the gate reject the very fix it recommends.
func TestCompatGateDoesNotBlockOnMissingModules(t *testing.T) {
	fn := gateSource(t)
	if strings.Contains(fn, "compatIncompatible") {
		t.Error("the blocking condition names compatIncompatible again: a MISSING module is conditional (throws only if called) " +
			"and a guest that guards its require() never reaches it. This re-creates the talos/expo-gl false block.")
	}
}

// Version and family drift are the genuinely fatal class: they corrupt the
// JSI/TurboModule contract for modules the host DOES register, and no guard in
// the guest can save that. These must keep hard-blocking.
func TestCompatGateStillBlocksOnVersionAndFamilyDrift(t *testing.T) {
	fn := gateSource(t)
	for _, must := range []string{
		"compatVersionMismatches",
		"compatReactVersionMismatch",
		"compatExpoVersionMismatch",
		"compatRNVersionMismatch",
	} {
		if !strings.Contains(fn, must) {
			t.Errorf("blocking condition no longer covers %s — version/family drift is a REAL crash the guest cannot guard against, and must stay fatal", must)
		}
	}
}

// Not blocking is not the same as staying quiet. The user still needs to know a
// declared module is absent, because it WILL throw if that code path runs.
func TestCompatGateStillWarnsAboutMissingModules(t *testing.T) {
	src := readSourceFile(t, "devserver_http.go")
	if !strings.Contains(src, "are NOT in Yaver's super-host") {
		t.Error("the missing-module warning was removed: downgrading the block must not also drop the diagnosis")
	}
	if !strings.Contains(src, "meta.IncompatibleNativeModules") {
		t.Error("missing modules must still be reported in bundle metadata so every surface can warn about them")
	}
}
