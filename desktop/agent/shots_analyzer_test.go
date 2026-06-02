package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny test helper.
func shotsWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindShotsFlowPrefersCommitted(t *testing.T) {
	dir := t.TempDir()
	if got := findShotsFlow(dir); got != "" {
		t.Fatalf("expected no flow, got %q", got)
	}
	flow := filepath.Join(dir, ".yaver", "shots.flow.yaml")
	shotsWrite(t, flow, "appId: x\n")
	if got := findShotsFlow(dir); got != flow {
		t.Fatalf("expected %q, got %q", flow, got)
	}
}

func TestReadBundleIDFromAppJSON(t *testing.T) {
	dir := t.TempDir()
	shotsWrite(t, filepath.Join(dir, "app.json"),
		`{"expo":{"ios":{"bundleIdentifier":"com.example.demo"}}}`)
	if got := readBundleIDFromAppJSON(dir); got != "com.example.demo" {
		t.Fatalf("bundle id = %q", got)
	}
}

func TestAnalyzeExpoRouterTabsAndGates(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "src", "app")
	// Gates.
	shotsWrite(t, filepath.Join(app, "language-select.tsx"), "export default function L(){}")
	shotsWrite(t, filepath.Join(app, "agent-setup.tsx"), "export default function A(){}")
	// app.json for bundle id.
	shotsWrite(t, filepath.Join(dir, "app.json"),
		`{"expo":{"ios":{"bundleIdentifier":"com.example.demo"}}}`)
	// Tabs layout: 3 visible + 1 hidden (href:null).
	shotsWrite(t, filepath.Join(app, "(tabs)", "_layout.tsx"), `
		<Tabs.Screen name="dashboard" options={{ title: 'Home' }} />
		<Tabs.Screen name="messages" options={{ title: 'Messages' }} />
		<Tabs.Screen name="clients" options={{ title: 'Clients' }} />
		<Tabs.Screen name="twitter" options={{ href: null }} />
	`)

	a, err := AnalyzeExpoRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.BundleID != "com.example.demo" {
		t.Errorf("bundle = %q", a.BundleID)
	}
	if !a.HasLangSelect {
		t.Error("expected HasLangSelect")
	}
	if !a.HasAuthGate {
		t.Error("expected HasAuthGate")
	}
	if a.VisibleTabs != 3 {
		t.Errorf("VisibleTabs = %d, want 3 (twitter is href:null)", a.VisibleTabs)
	}
	if len(a.Screens) != 4 {
		t.Errorf("Screens = %d, want 4", len(a.Screens))
	}
}

func TestGenerateShotsFlow(t *testing.T) {
	dir := t.TempDir()
	a := &ShotsAnalysis{VisibleTabs: 2, HasLangSelect: true, HasAuthGate: true,
		Screens: []ScreenNode{
			{Route: "dashboard", IsTab: true},
			{Route: "messages", IsTab: true},
		}}
	path, err := generateShotsFlow(dir, "com.example.demo", a)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"appId: com.example.demo", "takeScreenshot: 01_dashboard", "takeScreenshot: 02_messages", "clearState: true"} {
		if !strings.Contains(s, want) {
			t.Errorf("generated flow missing %q\n%s", want, s)
		}
	}
}

func TestSanitizeShotName(t *testing.T) {
	cases := map[string]string{
		"/(tabs)/dashboard": "01_tabs_dashboard",
		"01_dashboard":      "01_dashboard", // already prefixed → kept
		"":                  "01_screen",
	}
	for in, want := range cases {
		if got := sanitizeShotName(in, 0); got != want {
			t.Errorf("sanitizeShotName(%q) = %q, want %q", in, got, want)
		}
	}
}
