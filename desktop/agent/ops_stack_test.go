package main

import (
	"encoding/json"
	"testing"
)

func TestOpsStackReturnsSanitizedDetection(t *testing.T) {
	root := writeTree(t, map[string]string{
		"supabase/config.toml": "project_id = \"abc\"\n",
		"package.json":         `{"name":"api","dependencies":{"@supabase/supabase-js":"^2"}}`,
	})

	payload, _ := json.Marshal(opsStackPayload{WorkDir: root})
	res := opsStackHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("opsStackHandler failed: %+v", res)
	}
	initial := res.Initial.(opsStackInitial)
	if initial.Detection == nil {
		t.Fatal("expected detection")
	}
	if initial.Detection.Root != "" {
		t.Fatalf("Root leaked in response: %q", initial.Detection.Root)
	}
	if len(initial.Warnings) != len(initial.Detection.Warnings) {
		t.Fatalf("warnings mismatch: %v vs %v", initial.Warnings, initial.Detection.Warnings)
	}
}

func TestOpsStackRunnableTargetsReported(t *testing.T) {
	root := writeTree(t, map[string]string{
		"supabase/config.toml": "project_id = \"abc\"\n",
		"package.json":         `{"name":"api"}`,
	})
	prev := discoverToolBinary
	discoverToolBinary = func(name string) string {
		if name == "supabase" {
			return "/usr/local/bin/supabase"
		}
		return ""
	}
	defer func() { discoverToolBinary = prev }()

	payload, _ := json.Marshal(opsStackPayload{WorkDir: root})
	res := opsStackHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("opsStackHandler failed: %+v", res)
	}
	initial := res.Initial.(opsStackInitial)
	if len(initial.RunnableTargets) == 0 {
		t.Fatal("expected runnable target rows")
	}
	found := false
	for _, row := range initial.RunnableTargets {
		if row.Target.ID != "supabase" {
			continue
		}
		found = true
		if row.OpsTarget != "supabase-functions" {
			t.Fatalf("ops target = %q, want supabase-functions", row.OpsTarget)
		}
		if !row.Runnable {
			t.Fatalf("expected runnable supabase target, got %+v", row)
		}
	}
	if !found {
		t.Fatal("supabase row not returned")
	}
}
