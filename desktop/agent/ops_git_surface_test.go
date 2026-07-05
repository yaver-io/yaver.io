package main

import (
	"encoding/json"
	"testing"
)

func TestGitSurfaceOpsRegistered(t *testing.T) {
	want := map[string]bool{
		"git_prs":       false,
		"git_issues":    false,
		"git_ci_status": false,
	}
	for _, v := range listOpsVerbs() {
		if _, ok := want[v.Name]; ok {
			want[v.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing ops verb %s", name)
		}
	}
}

func TestGitSurfaceProviderNormalization(t *testing.T) {
	cases := map[string]string{
		"":           "auto",
		"auto":       "auto",
		"gh":         "github",
		"github.com": "github",
		"glab":       "gitlab",
		"gitlab.com": "gitlab",
	}
	for in, want := range cases {
		if got := normalizeGitSurfaceProvider(in); got != want {
			t.Fatalf("normalizeGitSurfaceProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitSurfaceCount(t *testing.T) {
	raw := map[string]interface{}{"pull_requests": []interface{}{1, 2}}
	if got := gitSurfaceCount(raw, "pull_requests"); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
	raw = map[string]interface{}{"output": "one\ntwo\n\n"}
	if got := gitSurfaceCount(raw, "pipelines"); got != 2 {
		t.Fatalf("output count = %d, want 2", got)
	}
}

func TestParseGitSurfacePayloadDefaultsToGitHub(t *testing.T) {
	_, provider, err := parseGitSurfacePayload(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if provider != "github" {
		t.Fatalf("provider = %q, want github", provider)
	}
}
