package main

import "testing"

func TestRouteVoiceCommand(t *testing.T) {
	known := map[string]bool{
		"status": true, "info": true, "run": true, "logs": true,
		"cloud_status": true, "git_push": true, "deploy": true, "destroy": true,
	}
	cases := []struct {
		in          string
		wantKind    string
		wantVerb    string
		wantCmd     string
		wantConfirm bool
	}{
		{"status", "ops", "status", "", false},
		{"Status.", "ops", "status", "", false},
		{"hey yaver, status", "ops", "status", "", false},
		{"yaver info", "ops", "info", "", false},
		{"cloud status", "ops", "cloud_status", "", false},
		{"ops git push", "ops", "git_push", "", true}, // destructive verb
		{"deploy", "ops", "deploy", "", true},         // destructive verb
		{"destroy", "ops", "destroy", "", true},       // destructive verb
		{"run git status", "ops", "run", "git status", false},
		{"run rm -rf build", "ops", "run", "rm -rf build", true}, // destructive run
		{"execute ls -la", "ops", "run", "ls -la", false},
		{"stop", "quit", "", "", false},
		{"stop listening", "quit", "", "", false},
		{"exit.", "quit", "", "", false},
		{"make me a sandwich", "none", "", "", false},
		{"", "none", "", "", false},
		{"run", "none", "", "", false}, // run with no command
	}
	for _, c := range cases {
		got := routeVoiceCommand(c.in, known)
		if got.Kind != c.wantKind || got.Verb != c.wantVerb || got.Cmd != c.wantCmd || got.Confirm != c.wantConfirm {
			t.Errorf("routeVoiceCommand(%q) = {%s %s %q confirm=%v}, want {%s %s %q confirm=%v}",
				c.in, got.Kind, got.Verb, got.Cmd, got.Confirm, c.wantKind, c.wantVerb, c.wantCmd, c.wantConfirm)
		}
	}
}

func TestVerbSlug(t *testing.T) {
	for in, want := range map[string]string{
		"cloud status": "cloud_status",
		"git   push":   "git_push",
		" Status ":     "status",
		"GLASS hud":    "glass_hud",
	} {
		if got := verbSlug(in); got != want {
			t.Errorf("verbSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
