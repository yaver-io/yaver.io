package main

import (
	"strings"
	"testing"
)

// REMOTE_WORKER.md §A ships the dev loop first. mobile_hermes_doctor was the one
// Layer 1 verb with nowhere to land: it could not be proxied because no route
// existed to proxy TO. Advertising device_id without the route would have shipped
// a capability that 404s — worse than not offering it.
func TestHermesDoctorRemoteRouteIsRegistered(t *testing.T) {
	src := readSourceFile(t, "httpserver.go")
	if !strings.Contains(src, `mux.HandleFunc("/mobile/hermes/doctor"`) {
		t.Fatal("no /mobile/hermes/doctor route — proxying the doctor would 404 on the remote")
	}
	if !strings.Contains(src, `proxyToDeviceJSON(context.Background(), "mobile_hermes_doctor"`) {
		t.Fatal("mobile_hermes_doctor does not proxy — device_id would be accepted and silently ignored")
	}
}

// The three halves must agree, or the tool is broken in a way no single file
// shows: schema advertises it, dispatcher honours it, route answers it.
func TestHermesDoctorAcceptsDeviceIDEndToEnd(t *testing.T) {
	if !strings.Contains(readSourceFile(t, "mcp_tools.go"), `"description": "Optional owned Yaver agent device id/name/alias that should run the diagnosis.`) {
		t.Error("schema does not advertise device_id — an MCP client cannot discover the capability")
	}
	if !strings.Contains(readSourceFile(t, "mcp_mobile_hermes_doctor.go"), "device_id,omitempty") {
		t.Error("mobileHermesDoctorInput has no DeviceID field — the dispatcher could not read it")
	}
	if !strings.Contains(readSourceFile(t, "mobile_project_http.go"), "func (s *HTTPServer) handleMobileHermesDoctor") {
		t.Error("no handler for the route")
	}
}

// The remote directory must NOT be defaulted on the calling side: it names a
// path in the REMOTE checkout, and baking this machine's working directory into
// a question about another box is how "diagnose my project" silently diagnoses
// the wrong tree.
func TestHermesDoctorDoesNotLocalizeTheRemoteDirectory(t *testing.T) {
	src := readSourceFile(t, "httpserver.go")
	i := strings.Index(src, `case "mobile_hermes_doctor":`)
	if i < 0 {
		t.Fatal("dispatch case not found")
	}
	seg := src[i : i+1200]
	proxy := strings.Index(seg, "proxyToDeviceJSON")
	deflt := strings.Index(seg, "args.Directory = s.taskMgr.workDir")
	if proxy < 0 || deflt < 0 {
		t.Fatal("expected both a proxy branch and a local default")
	}
	if deflt < proxy {
		t.Error("the local workDir default runs BEFORE the proxy — a remote diagnosis would carry this machine's path")
	}
}
