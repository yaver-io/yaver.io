package main

import (
	"os"
	"strings"
	"testing"
)

func TestClassifyMessage_TodoSignals(t *testing.T) {
	tests := []struct {
		msg    string
		intent MessageIntent
	}{
		{"the login button is broken, add to queue", IntentTodo},
		{"queue this bug: cart total shows NaN", IntentTodo},
		{"not now but the header overlaps on small screens", IntentTodo},
		{"fix later: dark mode colors are wrong", IntentTodo},
		{"note this — signup form has no validation", IntentTodo},
	}
	for _, tt := range tests {
		result := ClassifyMessage(tt.msg, nil)
		if result.Intent != tt.intent {
			t.Errorf("ClassifyMessage(%q) = %s, want %s", tt.msg, result.Intent, tt.intent)
		}
	}
}

func TestClassifyMessage_ActionSignals(t *testing.T) {
	tests := []struct {
		msg    string
		intent MessageIntent
	}{
		{"hot reload the app", IntentAction},
		{"deploy to testflight", IntentAction},
		{"build the ios app", IntentAction},
		{"implement all the fixes", IntentAction},
		{"show me the error logs", IntentAction},
		{"restart the dev server", IntentAction},
	}
	for _, tt := range tests {
		result := ClassifyMessage(tt.msg, nil)
		if result.Intent != tt.intent {
			t.Errorf("ClassifyMessage(%q) = %s, want %s", tt.msg, result.Intent, tt.intent)
		}
	}
}

func TestClassifyMessage_BugPatterns(t *testing.T) {
	tests := []struct {
		msg    string
		intent MessageIntent
	}{
		{"the checkout button doesn't work", IntentTodo},
		{"there's a crash when I tap profile", IntentTodo},
		{"this screen is broken on iphone", IntentTodo},
		{"the text is cut off on the settings page", IntentTodo},
		{"error when submitting the form", IntentTodo},
	}
	for _, tt := range tests {
		result := ClassifyMessage(tt.msg, nil)
		if result.Intent != tt.intent {
			t.Errorf("ClassifyMessage(%q) = %s, want %s", tt.msg, result.Intent, tt.intent)
		}
	}
}

func TestClassifyMessage_Continuation(t *testing.T) {
	items := []*TodoItem{
		{ID: "abc123", Description: "Login form broken", Status: TodoStatusPending, CreatedAt: "2026-03-25T10:00:00Z"},
	}

	result := ClassifyMessage("also the password field is too short", items)
	if result.Intent != IntentContinuation {
		t.Errorf("expected continuation, got %s", result.Intent)
	}
	if result.TodoID != "abc123" {
		t.Errorf("expected todoId abc123, got %s", result.TodoID)
	}
}

func TestClassifyMessage_DefaultAction(t *testing.T) {
	// Generic messages that don't match patterns default to action
	result := ClassifyMessage("refactor the auth module", nil)
	if result.Intent != IntentAction {
		t.Errorf("expected action for generic request, got %s", result.Intent)
	}
}

func TestDetectProjectInfo(t *testing.T) {
	// Test with current directory (should at least get a name)
	info := DetectProjectInfo("/tmp")
	if info.Name != "tmp" {
		t.Errorf("expected name 'tmp', got %s", info.Name)
	}
}

// Scope awareness: the mobile + web + desktop UIs hide mobile deploy buttons
// when detectFramework returns something that isn't a mobile framework, so
// these tests lock in the contract.
func TestDetectFramework_ScopeAwareness(t *testing.T) {
	write := func(t *testing.T, dir, name, body string) {
		t.Helper()
		path := dir + "/" + name
		// Ensure parent dir exists for nested names.
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			if err := os.MkdirAll(dir+"/"+name[:idx], 0o755); err != nil {
				t.Fatalf("mkdir parent for %s: %v", name, err)
			}
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cases := []struct {
		name   string
		setup  func(t *testing.T, dir string)
		expect string
	}{
		{
			name: "flutter project",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "pubspec.yaml", "name: e_mobile_new\n")
			},
			expect: "flutter",
		},
		{
			name: "expo mobile project",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "package.json", `{"name":"e_mobile_new","dependencies":{"expo":"~52.0.0"}}`)
			},
			expect: "expo",
		},
		{
			name: "pure react-native project",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "package.json", `{"name":"app","dependencies":{"react-native":"0.74.0"}}`)
			},
			expect: "react-native",
		},
		{
			name: "nextjs frontend (e_front)",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "next.config.ts", "export default {};")
				write(t, dir, "package.json", `{"name":"e_front","dependencies":{"next":"15.0.0"}}`)
			},
			expect: "nextjs",
		},
		{
			name: "convex/node backend (e_back) — no mobile framework",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "package.json", `{"name":"e_back","dependencies":{"convex":"1.0.0","dotenv":"16.0.0"}}`)
			},
			expect: "",
		},
		{
			name: "kotlin spring-boot backend — must NOT be classified as mobile",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "build.gradle.kts", `plugins { id("org.springframework.boot") version "3.2.0" }`)
				write(t, dir, "settings.gradle.kts", `rootProject.name = "backend"`)
			},
			expect: "",
		},
		{
			name: "kotlin android app — classified as kotlin mobile",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "build.gradle.kts", `plugins { id("com.android.application") }`)
				write(t, dir, "app/src/main/AndroidManifest.xml", `<manifest/>`)
			},
			expect: "kotlin",
		},
		{
			name: "swift ios app",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "Package.swift", `// swift-tools-version:5.9`)
			},
			expect: "swift",
		},
		{
			name: "supabase project — no mobile framework",
			setup: func(t *testing.T, dir string) {
				write(t, dir, "supabase/config.toml", `[api]`)
				write(t, dir, "package.json", `{"name":"backend","dependencies":{"@supabase/supabase-js":"2.0.0"}}`)
			},
			expect: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			got := detectFramework(dir)
			if got != tc.expect {
				t.Errorf("detectFramework(%q) = %q, want %q", tc.name, got, tc.expect)
			}
		})
	}
}
