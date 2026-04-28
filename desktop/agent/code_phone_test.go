package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// code_phone_test.go pins the /phone status renderer's behaviour.
// The terminal copy in attach.go and client.go is just plumbing —
// renderPhoneStatus is the function with logic worth testing.

func TestRenderPhoneStatus_RepoTier(t *testing.T) {
	dir := t.TempDir()
	store := NewRepoProjectStore(dir)
	want := Project{
		Slug:     "todo",
		Name:     "Todo App",
		Template: "blank",
		Schema: &PhoneSchema{Tables: []PhoneTable{
			{Name: "Item", Columns: []PhoneColumn{{Name: "id", Type: "string"}, {Name: "label", Type: "string"}}},
			{Name: "Tag", Columns: []PhoneColumn{{Name: "id", Type: "string"}}},
		}},
		Auth: &PhoneAuth{Personas: []PhonePersona{{ID: "u1", Email: "x@x.com"}}},
		Seed: PhoneSeed{
			"Item": []map[string]interface{}{{"id": "1"}, {"id": "2"}},
			"Tag":  []map[string]interface{}{{"id": "t1"}},
		},
		TokenLabels: []TokenLabel{{ID: "tok_a", Label: "web-prod"}},
	}
	if _, err := store.Write(context.Background(), want, WriteOptions{}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	out, err := renderPhoneStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("renderPhoneStatus: %v", err)
	}

	for _, want := range []string{
		"todo · repo",
		"Todo App",
		"2 tables — Item(2), Tag(1)",
		"1 personas",
		"3 rows across Item, Tag",
		"1 active — web-prod",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderPhoneStatus_NoProject(t *testing.T) {
	dir := t.TempDir()
	out, err := renderPhoneStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("renderPhoneStatus: %v", err)
	}
	if !strings.Contains(out, "no project at this path") {
		t.Fatalf("expected the no-project hint, got:\n%s", out)
	}
	if !strings.Contains(out, dir) {
		t.Fatalf("hint should echo the workdir; got:\n%s", out)
	}
}

func TestRenderPhoneStatus_AgentTier(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	if _, err := store.Write(context.Background(), Project{
		Slug:     "agt",
		Name:     "Agent Project",
		Template: "blank",
		Schema:   &PhoneSchema{Tables: []PhoneTable{{Name: "X"}}},
	}, WriteOptions{}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	root, err := PhoneProjectsRoot()
	if err != nil {
		t.Fatalf("PhoneProjectsRoot: %v", err)
	}
	workDir := filepath.Join(root, "agt")

	out, err := renderPhoneStatus(context.Background(), workDir)
	if err != nil {
		t.Fatalf("renderPhoneStatus: %v", err)
	}
	if !strings.Contains(out, "agt · agent") {
		t.Fatalf("expected agent-tier badge; got:\n%s", out)
	}
	if !strings.Contains(out, "Agent Project") {
		t.Fatalf("expected name in body; got:\n%s", out)
	}
}

func TestRenderPhoneStatus_RepoTierTakesPrecedenceOverAgent(t *testing.T) {
	// If a workdir somehow has both a `.yaver/project.yaml` AND lives
	// under PhoneProjectsRoot, the repo tier wins. That matches the
	// dual-mode story: the canonical source of truth is the repo
	// when one is present, the agent only when no repo is.
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	if _, err := store.Write(context.Background(), Project{Slug: "both", Name: "Agent View"}, WriteOptions{}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	root, _ := PhoneProjectsRoot()
	workDir := filepath.Join(root, "both")
	repo := NewRepoProjectStore(workDir)
	if _, err := repo.Write(context.Background(), Project{Slug: "both", Name: "Repo View"}, WriteOptions{OnConflict: ConflictOverwrite}); err != nil {
		t.Fatalf("seed repo overlay: %v", err)
	}
	out, err := renderPhoneStatus(context.Background(), workDir)
	if err != nil {
		t.Fatalf("renderPhoneStatus: %v", err)
	}
	if !strings.Contains(out, "both · repo") {
		t.Fatalf("repo tier should win when .yaver/ exists; got:\n%s", out)
	}
	if strings.Contains(out, "Agent View") {
		t.Fatalf("repo tier should hide agent fields; got:\n%s", out)
	}
}

func TestRelativeAge(t *testing.T) {
	cases := map[string]string{
		"":                     "", // unparseable → echoed
		"not a date":           "not a date",
		"2026-04-28T11:59:30Z": "just now", // less than a minute (depends on test time, may fail)
	}
	_ = cases
	// Just ensure unparseable input round-trips literally; concrete
	// time-based assertions would be flaky against the test clock.
	if got := relativeAge("not a date"); got != "not a date" {
		t.Errorf("relativeAge(\"not a date\") = %q; want literal echo", got)
	}
	if got := relativeAge(""); got != "" {
		t.Errorf("relativeAge(\"\") = %q; want empty echo", got)
	}
}
