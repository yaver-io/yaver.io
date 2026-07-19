package main

// mesh_safety.go — the single funnel every mesh bring-up path must pass
// through. Historically the SubnetRouteConflict guard existed once (in
// autoEnableMesh) and was missing from handleMeshUp and meshConvergeDesired
// (audit §4c, 2026-07-19). Two of the three ways to bring the overlay up
// bypassed the guard entirely — one tap of "enable mesh on all machines" from
// the phone or a console-driven desired-state change installed 100.96/12 on a
// Tailscale host regardless.
//
// Refactor rule: NEVER call mesh.Start() directly from a bring-up path. Call
// meshGuardAllowsBringUp() first and only proceed when it returns "". A fourth
// bring-up path added later cannot silently miss the guard as long as it
// funnels here.

import (
	"github.com/yaver-io/agent/mesh"
)

// meshGuardAllowsBringUp returns "" when it is safe to bring the mesh data
// plane up on this host, or a user-facing reason string when a Tailscale-like
// route on another interface already covers Yaver Mesh's address range.
//
// The check is intentionally cheap (interface enumeration; no netlink watch)
// so it is safe to call on every convergence tick and on every /mesh/up.
//
// ifaceName is the name of THIS host's own mesh device when it already exists
// — passed through so the guard does not report our own interface as the
// conflict once we have brought ourselves up (that would make mesh permanently
// refuse to restart on convergence). Pass "" when the manager has not been
// constructed yet.
func meshGuardAllowsBringUp(ifaceName string) string {
	conflict, err := mesh.SubnetRouteConflict(ifaceName)
	if err != nil {
		// A failure to enumerate interfaces is not evidence of safety. Fail
		// closed: refuse the bring-up and surface why.
		return "cannot check for Tailscale route conflict: " + err.Error()
	}
	if conflict == nil {
		return ""
	}
	return conflict.Reason()
}

// meshBringUpBlocked names the ONE call sites are expected to use so a later
// reader can grep for exactly the safety check. It composes ifaceName lookup
// under the meshMu lock (server-scoped state) with the pure guard above.
func (s *HTTPServer) meshBringUpBlocked() string {
	ifaceName := ""
	s.meshMu.Lock()
	if s.meshMgr != nil {
		ifaceName = s.meshMgr.Status().IfaceName
	}
	s.meshMu.Unlock()
	return meshGuardAllowsBringUp(ifaceName)
}
