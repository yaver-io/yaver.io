package main

import "testing"

func TestValidateForgeCLIArgsBlocksCredentialPrinting(t *testing.T) {
	cases := []struct {
		name string
		cli  string
		args []string
	}{
		{name: "gh auth token", cli: "gh", args: []string{"auth", "token"}},
		{name: "gh secret list", cli: "gh", args: []string{"secret", "list"}},
		{name: "gh repo secret list", cli: "gh", args: []string{"repo", "secret", "list"}},
		{name: "glab show token", cli: "glab", args: []string{"auth", "status", "--show-token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateForgeCLIArgs(tc.cli, tc.args, false); err == "" {
				t.Fatalf("validateForgeCLIArgs(%q, %v) allowed a blocked command", tc.cli, tc.args)
			}
		})
	}
}

func TestValidateForgeCLIArgsRequiresConfirmForDestructive(t *testing.T) {
	cases := [][]string{
		{"repo", "delete", "acme/widget"},
		{"api", "-X", "DELETE", "repos/acme/widget/hooks/1"},
		{"api", "--method", "DELETE", "projects/acme%2Fwidget/members/42"},
	}
	for _, args := range cases {
		if err := validateForgeCLIArgs("gh", args, false); err == "" {
			t.Fatalf("expected destructive args %v to require confirm", args)
		}
		if err := validateForgeCLIArgs("gh", args, true); err != "" {
			t.Fatalf("expected destructive args %v to pass with confirm, got %q", args, err)
		}
	}
}

func TestValidateForgeCLIArgsAllowsSafeReadOnlyCalls(t *testing.T) {
	if err := validateForgeCLIArgs("gh", []string{"repo", "view", "--json", "name"}, false); err != "" {
		t.Fatalf("safe gh command blocked: %q", err)
	}
	if err := validateForgeCLIArgs("glab", []string{"mr", "view", "42"}, false); err != "" {
		t.Fatalf("safe glab command blocked: %q", err)
	}
}
