package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSharedStorageProfilesFilteredForGuest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	sharedA := filepath.Join(t.TempDir(), "shared-a")
	sharedB := filepath.Join(t.TempDir(), "shared-b")
	if err := os.MkdirAll(sharedA, 0755); err != nil {
		t.Fatalf("mkdir shared-a: %v", err)
	}
	if err := os.MkdirAll(sharedB, 0755); err != nil {
		t.Fatalf("mkdir shared-b: %v", err)
	}

	if err := SaveConfig(&Config{
		SharedStorage: []SharedStorageProfile{
			{ID: "allowed", Name: "Allowed", Type: "local", Path: sharedA},
			{ID: "blocked", Name: "Blocked", Type: "local", Path: sharedB},
		},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	currentTestHTTPServer.guestConfigMgr = NewGuestConfigManager(t.TempDir())
	currentTestHTTPServer.guestConfigMgr.SetSharedStorageAccess("guest-1", []string{"allowed"})

	status, body := doRequestWithHeaders(t, "GET", baseURL+"/shared-storage/profiles", "tok", "", map[string]string{
		"X-Yaver-GuestUserID": "guest-1",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", status, body)
	}
	profiles, ok := body["profiles"].([]interface{})
	if !ok {
		t.Fatalf("expected profiles array, got %T", body["profiles"])
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 guest-visible profile, got %d", len(profiles))
	}
	profile, ok := profiles[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected profile object, got %T", profiles[0])
	}
	if profile["id"] != "allowed" {
		t.Fatalf("expected allowed profile, got %v", profile["id"])
	}

	hostStatus, hostBody := doRequest(t, "GET", baseURL+"/shared-storage/profiles", "tok", "")
	if hostStatus != http.StatusOK {
		t.Fatalf("host profiles expected 200, got %d body=%v", hostStatus, hostBody)
	}
	hostProfiles, ok := hostBody["profiles"].([]interface{})
	if !ok {
		t.Fatalf("expected host profiles array, got %T", hostBody["profiles"])
	}
	if len(hostProfiles) != 2 {
		t.Fatalf("expected host to see 2 profiles, got %d", len(hostProfiles))
	}
}

func TestSharedStorageListDeniedForGuestWithoutACL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(shared, 0755); err != nil {
		t.Fatalf("mkdir shared: %v", err)
	}

	if err := SaveConfig(&Config{
		SharedStorage: []SharedStorageProfile{
			{ID: "private", Name: "Private", Type: "local", Path: shared},
		},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	currentTestHTTPServer.guestConfigMgr = NewGuestConfigManager(t.TempDir())

	status, body := doRequestWithHeaders(t, "GET", baseURL+"/shared-storage/list?id=private", "tok", "", map[string]string{
		"X-Yaver-GuestUserID": "guest-1",
	})
	if status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", status, body)
	}
	errText, _ := body["error"].(string)
	if !strings.Contains(strings.ToLower(errText), "shared storage") {
		t.Fatalf("expected shared storage denial, got %q", errText)
	}
}

func TestSharedStorageContainerMountsForTaskRespectsModeAndACL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	hostOnly := filepath.Join(t.TempDir(), "host-only")
	guestOnly := filepath.Join(t.TempDir(), "guest-only")
	allAccess := filepath.Join(t.TempDir(), "all-access")
	readOnlyAll := filepath.Join(t.TempDir(), "readonly-all")
	for _, dir := range []string{hostOnly, guestOnly, allAccess, readOnlyAll} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	if err := SaveConfig(&Config{
		SharedStorage: []SharedStorageProfile{
			{ID: "host", Name: "Host", Type: "local", Path: hostOnly, ContainerMountMode: "host", ContainerPath: "/mnt/host"},
			{ID: "guest", Name: "Guest", Type: "local", Path: guestOnly, ContainerMountMode: "guests", ContainerPath: "/mnt/guest"},
			{ID: "all", Name: "All", Type: "local", Path: allAccess, ContainerMountMode: "all", ContainerPath: "/mnt/all"},
			{ID: "ro-all", Name: "RO", Type: "local", Path: readOnlyAll, ContainerMountMode: "all", ContainerPath: "/mnt/ro", ReadOnly: true},
		},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	guestMgr := NewGuestConfigManager(t.TempDir())
	guestMgr.SetSharedStorageAccess("guest-1", []string{"guest", "all", "ro-all"})

	hostMounts, err := sharedStorageContainerMountsForTask("", guestMgr)
	if err != nil {
		t.Fatalf("host mounts error = %v", err)
	}
	wantHost := []string{
		allAccess + ":/mnt/all",
		hostOnly + ":/mnt/host",
		readOnlyAll + ":/mnt/ro:ro",
	}
	assertStringSliceEqual(t, hostMounts, wantHost)

	guestMounts, err := sharedStorageContainerMountsForTask("guest-1", guestMgr)
	if err != nil {
		t.Fatalf("guest mounts error = %v", err)
	}
	wantGuest := []string{
		allAccess + ":/mnt/all:ro",
		guestOnly + ":/mnt/guest:ro",
		readOnlyAll + ":/mnt/ro:ro",
	}
	assertStringSliceEqual(t, guestMounts, wantGuest)
}

func doRequestWithHeaders(t *testing.T, method, url, token string, body string, headers map[string]string) (int, map[string]interface{}) {
	t.Helper()

	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body == "" {
		req.Body = nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	got = append([]string{}, got...)
	want = append([]string{}, want...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("length mismatch\ngot:  %v\nwant: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice mismatch\ngot:  %v\nwant: %v", got, want)
		}
	}
}
