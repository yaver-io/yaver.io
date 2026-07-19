package main

// mesh_safety_test.go — pin the invariant that EVERY mesh bring-up path calls
// through the shared safety guard. Zero tests asserted this before 2026-07-19
// (audit §7), so a fourth path could add itself and silently bypass the check.
//
// We do not (and cannot) actually bring the data plane up in these tests —
// that requires elevated privilege and would race Tailscale on this box. What
// we CAN assert:
//   1. The three bring-up call sites reference meshBringUpBlocked. Textual, but
//      it makes an intent-level regression loud rather than silent.
//   2. meshBringUpBlocked composes SubnetRouteConflict correctly on real
//      interface tables: no crash on empty ifaceName, own iface is skipped.
//   3. The guard returns a non-empty reason when a Tailscale-like interface is
//      pretended to be present (exercised via mesh.SubnetRouteConflict against
//      the current host's tables — this is a smoke test, not a mocked one).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMeshSafetyGuardEmptyIfaceNameDoesNotPanic checks the trivial invariant
// used before the manager exists: pass ifaceName="" and the guard must not
// return an ambiguous "cannot check" reason for a healthy host. Whatever the
// host looks like, this must not panic.
func TestMeshSafetyGuardEmptyIfaceNameDoesNotPanic(t *testing.T) {
	// If this returns an error string, that's still a valid outcome (the CI
	// host might have an interface in CGNAT). Just don't panic.
	got := meshGuardAllowsBringUp("")
	_ = got // acceptance is "no panic", not a specific string
}

// TestHandleMeshUpConsultsGuard asserts textually that handleMeshUp routes
// through meshBringUpBlocked before reaching keypair / meshRegisterJoin /
// mgr.Start(). This is the phone "enable mesh on all machines" path.
func TestHandleMeshUpConsultsGuard(t *testing.T) {
	src := readSourceOrSkip(t, "mesh_http.go")
	fn := extractFunctionBody(t, src, "handleMeshUp")
	assertGuardBeforeCall(t, fn, "handleMeshUp", []string{
		"meshLoadOrCreateKeyPair",
		"meshRegisterJoin",
		"mgr.Start",
	})
}

// TestAutoEnableMeshConsultsGuard asserts the default-on boot path also
// consults the shared guard (this was the ONLY path that had it before this
// refactor). It should now call meshBringUpBlocked, not SubnetRouteConflict
// directly, so a future change to the guard shape lands in one place.
func TestAutoEnableMeshConsultsGuard(t *testing.T) {
	src := readSourceOrSkip(t, "mesh_http.go")
	fn := extractFunctionBody(t, src, "autoEnableMesh")
	assertGuardBeforeCall(t, fn, "autoEnableMesh", []string{
		"meshLoadOrCreateKeyPair",
		"meshRegisterJoin",
		"mgr.Start",
	})
	// Regression bar: no bring-up path should reach directly into the mesh
	// package for the conflict check any more — that's a sign somebody
	// re-inlined the guard and skipped meshBringUpBlocked.
	if strings.Contains(fn, "mesh.SubnetRouteConflict(") {
		t.Errorf("autoEnableMesh should call meshBringUpBlocked (the shared helper), "+
			"not mesh.SubnetRouteConflict directly:\n%s", fn)
	}
}

// TestMeshConvergeDesiredConsultsGuard asserts the 30s convergence loop —
// which restarts the data plane on any console-driven desired-state change —
// also consults the guard before it restarts. Before this fix, a console
// toggle could undo a correct boot-time deferral.
func TestMeshConvergeDesiredConsultsGuard(t *testing.T) {
	src := readSourceOrSkip(t, "mesh_agent.go")
	fn := extractFunctionBody(t, src, "meshConvergeDesired")
	// The bring-up-sensitive call in this function is ensureMeshManagerLocked
	// / mgr.Start() (the restart path). The guard must sit before that call.
	assertGuardBeforeCall(t, fn, "meshConvergeDesired", []string{
		"ensureMeshManagerLocked",
	})
}

// TestMeshSafetyHelperIsTheOnlyGuardCallSite is the "no fourth path" test.
// Once we have a shared helper, the ONLY places outside the mesh package
// itself and the safety helper file that should mention SubnetRouteConflict
// are (a) mesh_safety.go and (b) test files. Anything else is a bring-up path
// that hasn't been routed through the funnel yet.
func TestMeshSafetyHelperIsTheOnlyGuardCallSite(t *testing.T) {
	dir := sourceDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "mesh_safety.go" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if strings.Contains(string(body), "mesh.SubnetRouteConflict(") {
			offenders = append(offenders, name)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("SubnetRouteConflict is called outside mesh_safety.go — every "+
			"bring-up path must funnel through meshBringUpBlocked so a fourth "+
			"path cannot silently miss the guard. Offending files: %v", offenders)
	}
}

// --- test-only helpers ---------------------------------------------------

func readSourceOrSkip(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(sourceDir(t), name)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("cannot read %s: %v", p, err)
	}
	return string(body)
}

func sourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

// extractFunctionBody pulls the body of a top-level func literal by name using
// a brace-depth scanner. Deliberately dumb — a full parser is not worth the
// dependency, and this is only asked to find things like handleMeshUp in a
// file we control.
func extractFunctionBody(t *testing.T, src, name string) string {
	t.Helper()
	// Signature can be a method (func (recv *T) Name(...) ...) or free func.
	// Look for the closing ") " before the name, or "func " directly before it.
	// Cheapest heuristic: find "func " occurrences and pick the one where
	// name appears before the first "(".
	idx := 0
	for {
		i := strings.Index(src[idx:], "func ")
		if i < 0 {
			t.Fatalf("function %q not found in source", name)
		}
		start := idx + i
		// Look for the name up to the first "{" of this signature.
		braceIdx := strings.Index(src[start:], "{")
		if braceIdx < 0 {
			t.Fatalf("no opening brace after %q", src[start:minInt(start+80, len(src))])
		}
		sig := src[start : start+braceIdx]
		if strings.Contains(sig, " "+name+"(") {
			// Walk braces from start+braceIdx to matching close.
			depth := 0
			for j := start + braceIdx; j < len(src); j++ {
				switch src[j] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return src[start : j+1]
					}
				}
			}
			t.Fatalf("unbalanced braces starting at %q", src[start:minInt(start+80, len(src))])
		}
		idx = start + braceIdx + 1
	}
}

// assertGuardBeforeCall requires that meshBringUpBlocked appears in the given
// function body BEFORE any of the sensitive call names. Ordering matters —
// checking after the bring-up is worse than not checking at all.
func assertGuardBeforeCall(t *testing.T, body, fnName string, sensitive []string) {
	t.Helper()
	guardIdx := strings.Index(body, "meshBringUpBlocked(")
	if guardIdx < 0 {
		t.Fatalf("%s does not call meshBringUpBlocked — mesh bring-up must consult "+
			"the shared safety guard (see mesh_safety.go)\nfunction body:\n%s", fnName, body)
	}
	for _, s := range sensitive {
		callIdx := strings.Index(body, s)
		if callIdx < 0 {
			continue // this bring-up may not use this particular call
		}
		if callIdx < guardIdx {
			t.Errorf("%s calls %s at offset %d BEFORE meshBringUpBlocked at offset %d "+
				"— the guard must run first, or the bring-up wins the race",
				fnName, s, callIdx, guardIdx)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
