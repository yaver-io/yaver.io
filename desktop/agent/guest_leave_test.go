package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Real HTTP server on a random port, per the house pattern — no mocks.

func TestLeaveSharedAccessRoutesHostUserID(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"alreadyGone":false,"hostName":"Some Host","hostUserId":"u_abc"}`))
	}))
	defer srv.Close()

	res, err := LeaveSharedAccess(srv.URL, "tok123", "u_abc", "")
	if err != nil {
		t.Fatalf("LeaveSharedAccess: %v", err)
	}

	if gotPath != "/guests/leave" {
		t.Errorf("path = %q, want /guests/leave", gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth = %q, want Bearer tok123", gotAuth)
	}
	if gotBody["hostUserId"] != "u_abc" {
		t.Errorf("hostUserId = %q, want u_abc", gotBody["hostUserId"])
	}
	// An empty email must be omitted, not sent as "" — the endpoint treats a
	// present-but-empty field as "provided" and would fail to resolve it.
	if _, present := gotBody["hostEmail"]; present {
		t.Errorf("hostEmail should be omitted when empty, got %q", gotBody["hostEmail"])
	}
	if !res.OK || res.HostName != "Some Host" {
		t.Errorf("result = %+v, want ok + hostName", res)
	}
}

func TestLeaveSharedAccessLowercasesEmail(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if _, err := LeaveSharedAccess(srv.URL, "tok", "", "  Host@Example.COM  "); err != nil {
		t.Fatalf("LeaveSharedAccess: %v", err)
	}
	// Convex indexes users by a lowercased email; sending mixed case would
	// silently miss the by_email index and report "no user found".
	if gotBody["hostEmail"] != "host@example.com" {
		t.Errorf("hostEmail = %q, want host@example.com", gotBody["hostEmail"])
	}
	if _, present := gotBody["hostUserId"]; present {
		t.Errorf("hostUserId should be omitted when empty")
	}
}

func TestLeaveSharedAccessRequiresATarget(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	_, err := LeaveSharedAccess(srv.URL, "tok", "   ", "  ")
	if err == nil {
		t.Fatal("want error when neither host userId nor email is given")
	}
	if called {
		t.Error("must not issue a request without a target")
	}
}

func TestLeaveSharedAccessPropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"No Yaver user found for that host"}`))
	}))
	defer srv.Close()

	_, err := LeaveSharedAccess(srv.URL, "tok", "u_nope", "")
	if err == nil {
		t.Fatal("want error on non-200")
	}
	if !strings.Contains(err.Error(), "No Yaver user found") {
		t.Errorf("error = %q, want it to carry the server's message", err.Error())
	}
}

func TestLeaveSharedAccessReportsAlreadyGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"alreadyGone":true,"hostName":"Some Host"}`))
	}))
	defer srv.Close()

	res, err := LeaveSharedAccess(srv.URL, "tok", "u_abc", "")
	if err != nil {
		t.Fatalf("LeaveSharedAccess: %v", err)
	}
	// Distinguishes "I just removed your access" from "there was nothing to
	// remove", so the CLI doesn't claim a change it didn't make.
	if !res.AlreadyGone {
		t.Error("alreadyGone should round-trip as true")
	}
}
