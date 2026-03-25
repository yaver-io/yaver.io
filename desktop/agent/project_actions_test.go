package main

import (
	"testing"
)

func TestDetectProjectActions_YaverRepo(t *testing.T) {
	actions := DetectProjectActions("/Users/kivanccakmak/Workspace/yaver.io")
	if len(actions) == 0 {
		t.Fatal("expected actions for yaver.io repo")
	}

	// Should find: mobile (expo), web (nextjs), backend (convex), relay (go/docker), desktop (go)
	types := map[string]bool{}
	for _, a := range actions {
		types[a.Framework+"/"+a.Platform] = true
		t.Logf("  %s [%s] %s → %s", a.Icon, a.Type, a.Label, a.Target)
	}

	if !types["expo/"] {
		t.Error("expected expo action")
	}
	if !types["nextjs/vercel"] {
		t.Error("expected nextjs/vercel action")
	}
	if !types["/convex"] {
		t.Error("expected convex action")
	}
}

func TestDetectProjectActions_AcmeStore(t *testing.T) {
	actions := DetectProjectActions("/Users/kivanccakmak/Workspace/yaver.io/demo/AcmeStore")
	if len(actions) == 0 {
		t.Fatal("expected actions for AcmeStore")
	}
	hasHotReload := false
	for _, a := range actions {
		if a.Type == "dev-server" && a.Framework == "expo" {
			hasHotReload = true
		}
		t.Logf("  %s [%s] %s", a.Icon, a.Type, a.Label)
	}
	if !hasHotReload {
		t.Error("expected expo hot reload action")
	}
}
