package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// freshAPKServer resets the package-global server between tests.
func resetAPKServer(t *testing.T) {
	t.Helper()
	apkSrv.stop()
	apkSrv.mu.Lock()
	apkSrv.apps = map[string]*apkEntry{}
	apkSrv.latest = ""
	apkSrv.mu.Unlock()
}

func writeFakeAPK(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "demo.apk")
	if err := os.WriteFile(p, []byte("PK\x03\x04 fake apk bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPKServeAndDownload(t *testing.T) {
	resetAPKServer(t)
	defer resetAPKServer(t)

	apk := writeFakeAPK(t)
	s := &HTTPServer{}
	if _, err := s.apkServeCore(map[string]interface{}{
		"apk":     apk,
		"app":     "Demo App",
		"package": "com.example.demo",
		"port":    float64(0), // ephemeral
	}); err != nil {
		t.Fatalf("serve failed: %v", err)
	}

	_, port, _, _ := apkSrv.snapshot()
	if port == 0 {
		t.Fatal("server not running")
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	// install page
	if body := httpGet(t, base+"/"); !strings.Contains(body, "Install") {
		t.Errorf("install page missing Install text: %q", body)
	}

	// apk bytes by slug
	got := httpGet(t, base+"/demo-app.apk")
	if !strings.HasPrefix(got, "PK\x03\x04") {
		t.Errorf("apk download wrong content: %q", got)
	}

	// latest.apk alias
	if got2 := httpGet(t, base+"/latest.apk"); got2 != got {
		t.Errorf("latest.apk != demo-app.apk")
	}

	// version.json
	var ver apkEntry
	if err := json.Unmarshal([]byte(httpGet(t, base+"/version.json")), &ver); err != nil {
		t.Fatalf("version.json parse: %v", err)
	}
	if ver.App != "demo-app" || ver.Package != "com.example.demo" {
		t.Errorf("version.json unexpected: %+v", ver)
	}
}

func TestAssetlinksReflectsPackageAndSHA(t *testing.T) {
	resetAPKServer(t)
	defer resetAPKServer(t)

	apk := writeFakeAPK(t)
	s := &HTTPServer{}
	// publish without a domain → LAN, but assetlinks still served locally
	if _, err := s.apkPublishCore(map[string]interface{}{
		"apk":         apk,
		"app":         "demo",
		"package":     "com.example.demo",
		"versionName": "1.0.0",
		"versionCode": float64(7),
		"sha256":      "AA:BB:CC, DD:EE:FF",
	}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	_, port, _, _ := apkSrv.snapshot()
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	var links []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			PackageName  string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	raw := httpGet(t, base+"/.well-known/assetlinks.json")
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		t.Fatalf("assetlinks parse: %v (%s)", err, raw)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 statement, got %d: %s", len(links), raw)
	}
	if links[0].Target.PackageName != "com.example.demo" {
		t.Errorf("package mismatch: %s", links[0].Target.PackageName)
	}
	if len(links[0].Target.Fingerprints) != 2 || links[0].Target.Fingerprints[0] != "AA:BB:CC" {
		t.Errorf("fingerprints wrong: %v", links[0].Target.Fingerprints)
	}
}

func TestAssetlinksEmptyWithoutPackage(t *testing.T) {
	resetAPKServer(t)
	defer resetAPKServer(t)

	apk := writeFakeAPK(t)
	s := &HTTPServer{}
	if _, err := s.apkServeCore(map[string]interface{}{"apk": apk, "app": "nopkg", "port": float64(0)}); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	_, port, _, _ := apkSrv.snapshot()
	raw := httpGet(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/.well-known/assetlinks.json")
	if strings.TrimSpace(raw) != "[]" {
		t.Errorf("expected empty assetlinks for app with no package/sha, got %q", raw)
	}
}

func TestSplitSHAs(t *testing.T) {
	got := splitSHAs("aa:bb, cc:dd\nee:ff gg:hh")
	want := []string{"AA:BB", "CC:DD", "EE:FF", "GG:HH"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %s want %s", i, got[i], want[i])
		}
	}
}

// --- helpers ---

func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
