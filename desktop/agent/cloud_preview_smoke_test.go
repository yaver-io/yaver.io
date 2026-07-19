package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudDevBypassRequiresExplicitEnv(t *testing.T) {
	t.Setenv("YAVER_CLOUD_DEV_BYPASS", "")
	if cloudDevBypassEnabled() {
		t.Fatal("dev bypass should default off")
	}
	for _, value := range []string{"1", "true", "yes", "on"} {
		t.Setenv("YAVER_CLOUD_DEV_BYPASS", value)
		if !cloudDevBypassEnabled() {
			t.Fatalf("dev bypass should accept %q", value)
		}
	}
	t.Setenv("YAVER_CLOUD_DEV_BYPASS", "0")
	if cloudDevBypassEnabled() {
		t.Fatal("dev bypass should reject 0")
	}
}

func TestCloudUsageDoesNotAdvertiseDevBypass(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	printCloudUsage()
	_ = w.Close()
	os.Stdout = oldStdout
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	text := string(out)
	for _, forbidden := range []string{"skip-payment", "private-preview", "without Lemon Squeezy"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("usage must not advertise %q:\n%s", forbidden, text)
		}
	}
	if !strings.Contains(text, "Cloud Workspace purchase is web-only") {
		t.Fatalf("usage missing web-only Cloud Workspace billing note:\n%s", text)
	}
}

func TestActivateCloudMachine_DevBypass(t *testing.T) {
	sawAuth := ""
	sawRegion := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/billing/yaver-cloud/dev-activate" {
			http.NotFound(w, r)
			return
		}
		sawAuth = r.Header.Get("Authorization")
		var body struct {
			Region string `json:"region"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		sawRegion = body.Region
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"mode": "dev-bypass",
		})
	}))
	defer srv.Close()

	mode, err := activateCloudMachine("preview-token", srv.URL)
	if err != nil {
		t.Fatalf("activateCloudMachine: %v", err)
	}
	if mode != "dev-bypass" {
		t.Fatalf("mode = %q, want dev-bypass", mode)
	}
	if sawAuth != "Bearer preview-token" {
		t.Fatalf("Authorization = %q, want Bearer preview-token", sawAuth)
	}
	if sawRegion != "eu" {
		t.Fatalf("region = %q, want eu", sawRegion)
	}
}

func TestCloudPreviewTodoApp_PushBundleRoundTrip(t *testing.T) {
	setupPhoneTestHome(t)

	src, err := CreatePhoneProject(PhoneCreateSpec{
		Name:     "Cloud Preview Todos",
		Template: "todos",
	})
	if err != nil {
		t.Fatalf("create source project: %v", err)
	}

	srcAdapter, err := PhoneAdapter(src.Slug)
	if err != nil {
		t.Fatalf("PhoneAdapter(source): %v", err)
	}
	if _, err := srcAdapter.Insert("todos", map[string]interface{}{
		"id":       "t-cloud",
		"title":    "Preview smoke task",
		"done":     false,
		"owner_id": "alice",
	}); err != nil {
		t.Fatalf("insert todo row: %v", err)
	}

	bundle, err := ExportPhoneProjectWithOptions(src.Slug, PhoneExportOptions{
		IncludeData:  true,
		Containerize: true,
	})
	if err != nil {
		t.Fatalf("export source bundle: %v", err)
	}

	for _, name := range []string{"local.db", "Dockerfile", "docker-compose.yml", ".env.example"} {
		if !bundleContainsFile(t, bundle, name) {
			t.Fatalf("expected bundle to include %s", name)
		}
	}

	srv := &HTTPServer{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/phone/projects/receive" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer target-token" {
			t.Fatalf("Authorization = %q, want Bearer target-token", got)
		}
		srv.handlePhoneReceive(w, r)
	}))
	defer target.Close()

	result, err := pushPhoneBundle(target.URL, "target-token", bundle, "cloud-preview-copy", "overwrite", false)
	if err != nil {
		t.Fatalf("pushPhoneBundle: %v", err)
	}
	if result.Slug != "cloud-preview-copy" {
		t.Fatalf("slug = %q, want cloud-preview-copy", result.Slug)
	}

	imported, err := LoadPhoneProject(result.Slug)
	if err != nil {
		t.Fatalf("load imported project: %v", err)
	}

	for _, name := range []string{"Dockerfile", "docker-compose.yml", ".env.example", ".dockerignore", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(imported.Dir, name)); err != nil {
			t.Fatalf("expected imported project to contain %s: %v", name, err)
		}
	}

	adapter, err := PhoneAdapter(imported.Slug)
	if err != nil {
		t.Fatalf("PhoneAdapter: %v", err)
	}
	res, err := adapter.Browse("todos", "", 50)
	if err != nil {
		t.Fatalf("browse todos: %v", err)
	}
	rows := res.Rows
	if len(rows) < 4 {
		t.Fatalf("expected at least 4 todo rows after import, got %d", len(rows))
	}

	found := false
	for _, row := range rows {
		if title, _ := row["title"].(string); title == "Preview smoke task" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected imported todos to include the preview smoke task")
	}
}
