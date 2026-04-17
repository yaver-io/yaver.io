package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateRepoMetadataIncludesStackCIIntegrationsAndAutoinit(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("package.json", `{"name":"demo","private":true}`)
	mustWrite("pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	mustWrite(".github/workflows/ci.yml", "name: ci\n")
	mustWrite(".env.example", "OPENAI_API_KEY=\n")
	mustWrite(".yaver/config.yaml", "backend: sqlite\n")
	mustWrite("init.md", autoinitGenStart+"\nbody\n"+autoinitGenEnd+"\n"+autoinitHistoryStart+"\n"+autoinitHistoryEnd)

	meta := generateRepoMetadata(dir)

	if got := meta["stackType"]; got != "monorepo" {
		t.Fatalf("expected monorepo stackType, got %#v", got)
	}

	ci, ok := meta["ciProviders"].([]string)
	if !ok || len(ci) != 1 || ci[0] != "github-actions" {
		t.Fatalf("expected github-actions ciProviders, got %#v", meta["ciProviders"])
	}

	ints, ok := meta["integrations"].([]string)
	if !ok {
		t.Fatalf("expected integrations slice, got %#v", meta["integrations"])
	}
	foundOpenAI := false
	foundYaverBackend := false
	for _, v := range ints {
		if v == "openai" {
			foundOpenAI = true
		}
		if v == "yaver-backend" {
			foundYaverBackend = true
		}
	}
	if !foundOpenAI {
		t.Fatalf("expected openai integration, got %#v", ints)
	}
	if !foundYaverBackend {
		t.Fatalf("expected yaver-backend integration, got %#v", ints)
	}

	st, ok := meta["autoinit"].(AutoInitStatus)
	if !ok {
		t.Fatalf("expected AutoInitStatus, got %#v", meta["autoinit"])
	}
	if !st.Done || !st.HasGenSec || !st.HasHistory {
		t.Fatalf("unexpected autoinit status: %#v", st)
	}

	topology, ok := meta["topology"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected topology map, got %#v", meta["topology"])
	}
	codingRunsOn, ok := topology["codingRunsOn"].([]string)
	if !ok || len(codingRunsOn) != 3 {
		t.Fatalf("expected codingRunsOn continuum, got %#v", topology["codingRunsOn"])
	}
	if got := topology["codingDefault"]; got != "user-choice" {
		t.Fatalf("expected codingDefault=user-choice, got %#v", got)
	}
	backendRunsOn, ok := topology["backendRunsOn"].([]string)
	if !ok || len(backendRunsOn) != 3 {
		t.Fatalf("expected backendRunsOn continuum, got %#v", topology["backendRunsOn"])
	}
}
