package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Reuses the shared fakeHetzner harness (cloud_provisioner_hetzner_test
// .go: server id 4242, snapshot image id 1). A uniquely-named raw
// helper covers the snapshot-failure + payload-capture cases the
// shared harness doesn't model. No mocks (repo convention).

func withRawFakeHetzner(t *testing.T, h http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(h)
	ob, os2 := hetznerAPIBase, hetznerSkipReadyWait
	hetznerAPIBase, hetznerSkipReadyWait = srv.URL, true
	return func() { hetznerAPIBase, hetznerSkipReadyWait = ob, os2; srv.Close() }
}

func TestHetznerStopServer_SnapshotThenDelete(t *testing.T) {
	f := newFakeHetzner(t)
	withFakeHetzner(t, f)
	m, err := NewCloudDeployManager(".")
	if err != nil {
		t.Fatal(err)
	}
	snapID, err := m.hetznerStopServer("tok", "4242", "yaver-stop-4242")
	if err != nil {
		t.Fatalf("hetznerStopServer: %v", err)
	}
	if snapID != "1" {
		t.Fatalf("want snapshot id 1, got %q", snapID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.snapshot || !f.deleted {
		t.Fatalf("expected snapshot AND delete; snap=%v del=%v", f.snapshot, f.deleted)
	}
}

func TestHetznerStopServer_AbortsDeleteOnSnapshotFailure(t *testing.T) {
	var delCalled int32
	defer withRawFakeHetzner(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			atomic.StoreInt32(&delCalled, 1)
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500) // snapshot create_image fails
	})()

	m, _ := NewCloudDeployManager(".")
	_, err := m.hetznerStopServer("tok", "555", "lbl")
	if err == nil || !strings.Contains(err.Error(), "NOT deleting") {
		t.Fatalf("expected fail-closed abort error, got: %v", err)
	}
	if atomic.LoadInt32(&delCalled) != 0 {
		t.Fatal("DELETE must NOT be called when snapshot fails (fail-closed)")
	}
}

func TestHetznerStartServer_FromSnapshot(t *testing.T) {
	var gotImage interface{}
	defer withRawFakeHetzner(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/servers") {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotImage = body["image"]
			_, _ = w.Write([]byte(`{"server":{"id":77,"public_net":{"ipv4":{"ip":"10.0.0.7"}}}}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	})()

	m, _ := NewCloudDeployManager(".")
	ip, id, err := m.hetznerStartServer("tok", "restored-box", "starter", "eu", "99001")
	if err != nil {
		t.Fatalf("hetznerStartServer: %v", err)
	}
	if ip != "10.0.0.7" || id != "77" {
		t.Fatalf("want 10.0.0.7/77, got %s/%s", ip, id)
	}
	if f, ok := gotImage.(float64); !ok || int(f) != 99001 {
		t.Fatalf("server payload image must be numeric 99001, got %#v", gotImage)
	}
}

func TestOpsCloudStop_DryRunByDefault(t *testing.T) {
	res := opsCloudStopHandler(OpsContext{}, json.RawMessage(`{"serverId":"555","confirm":true}`))
	if !res.OK {
		t.Fatalf("dry-run should be OK, got %+v", res)
	}
	mm, _ := res.Initial.(map[string]interface{})
	if mm == nil || mm["dryRun"] != true {
		t.Fatalf("expected dryRun:true plan, got %#v", res.Initial)
	}
}

func TestOpsCloudStart_DryRunByDefault(t *testing.T) {
	res := opsCloudStartHandler(OpsContext{}, json.RawMessage(`{"snapshotImageId":"99001","name":"x","confirm":true}`))
	if !res.OK {
		t.Fatalf("dry-run should be OK, got %+v", res)
	}
	mm, _ := res.Initial.(map[string]interface{})
	if mm == nil || mm["dryRun"] != true {
		t.Fatalf("expected dryRun:true plan, got %#v", res.Initial)
	}
}

func TestOpsCloudStop_RejectsMissingServerId(t *testing.T) {
	res := opsCloudStopHandler(OpsContext{}, json.RawMessage(`{"confirm":true}`))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload, got %+v", res)
	}
}
