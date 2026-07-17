package main

import (
	"errors"
	"testing"
)

func TestOpsDeployInfersSingleTarget(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
		"vercel.json":    "{}\n",
	})
	det := stackDetect(root)
	target, inferredFrom, err := resolveDeployTarget(opsDeployPayload{WorkDir: root}, det)
	if err != nil {
		t.Fatalf("resolveDeployTarget: %v", err)
	}
	if target != "vercel" {
		t.Fatalf("target = %q, want vercel", target)
	}
	if inferredFrom != "vercel.json" {
		t.Fatalf("inferredFrom = %q, want vercel.json", inferredFrom)
	}
}

func TestOpsDeployRejectsAmbiguousTargets(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
		"vercel.json":    "{}\n",
		"wrangler.toml":  "name = \"w\"\n",
	})
	_, _, err := resolveDeployTarget(opsDeployPayload{WorkDir: root}, stackDetect(root))
	var derr *deployResolveError
	if !errors.As(err, &derr) {
		t.Fatalf("expected deployResolveError, got %v", err)
	}
	if derr.Code != "ambiguous_target" {
		t.Fatalf("code = %q, want ambiguous_target", derr.Code)
	}
	if len(derr.Candidates) != 2 {
		t.Fatalf("candidates = %v, want 2", derr.Candidates)
	}
}

func TestOpsDeployRejectsNoTargetFound(t *testing.T) {
	root := writeTree(t, map[string]string{
		"README.md": "# empty\n",
	})
	_, _, err := resolveDeployTarget(opsDeployPayload{WorkDir: root}, stackDetect(root))
	if err == nil {
		t.Fatal("expected error for no deployable target")
	}
}

func TestOpsDeployExplicitTargetWins(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
		"vercel.json":    "{}\n",
		"wrangler.toml":  "name = \"w\"\n",
	})
	cmd, tool, err := resolveDeployCommand(opsDeployPayload{WorkDir: root, Target: "vercel"}, stackDetect(root))
	if err != nil {
		t.Fatalf("resolveDeployCommand: %v", err)
	}
	if tool != "vercel" {
		t.Fatalf("tool = %q, want vercel", tool)
	}
	if cmd == "" || cmd[:10] != "npx vercel" {
		t.Fatalf("command = %q, want npx vercel...", cmd)
	}
}

func TestOpsDeploySupabaseFunctionsCommand(t *testing.T) {
	root := writeTree(t, map[string]string{
		"supabase/config.toml": "project_id = \"abc\"\n",
		"package.json":         `{"name":"api"}`,
	})
	cmd, tool, err := resolveDeployCommand(opsDeployPayload{WorkDir: root, Target: "supabase-functions", Args: []string{"hello"}}, stackDetect(root))
	if err != nil {
		t.Fatalf("resolveDeployCommand: %v", err)
	}
	if tool != "supabase" {
		t.Fatalf("tool = %q, want supabase", tool)
	}
	if cmd != "supabase functions deploy hello" {
		t.Fatalf("command = %q", cmd)
	}
}

func TestOpsDeploySupabaseDBCommand(t *testing.T) {
	root := writeTree(t, map[string]string{
		"supabase/config.toml": "project_id = \"abc\"\n",
		"package.json":         `{"name":"api"}`,
	})
	cmd, _, err := resolveDeployCommand(opsDeployPayload{WorkDir: root, Target: "supabase-db"}, stackDetect(root))
	if err != nil {
		t.Fatalf("resolveDeployCommand: %v", err)
	}
	if cmd != "supabase db push" {
		t.Fatalf("command = %q, want supabase db push", cmd)
	}
}

func TestOpsDeployRefusesWeakSupabaseTarget(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json": `{"name":"web","dependencies":{"@supabase/supabase-js":"^2"}}`,
	})
	_, _, err := resolveDeployCommand(opsDeployPayload{WorkDir: root, Target: "supabase-db"}, stackDetect(root))
	if err == nil {
		t.Fatal("expected weak supabase target to be refused")
	}
}
