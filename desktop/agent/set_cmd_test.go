package main

import (
	"strings"
	"testing"
)

func TestParseEmailOAuthSetOptions(t *testing.T) {
	opts, err := parseEmailOAuthSetOptions([]string{
		"--email", " owner@example.com ",
		"--password-env", "YAVER_TEST_PASSWORD",
		"--convex-url", "https://example.convex.site/",
		"--require-owner",
	})
	if err != nil {
		t.Fatalf("parseEmailOAuthSetOptions: %v", err)
	}
	if opts.Email != "owner@example.com" {
		t.Fatalf("email = %q", opts.Email)
	}
	if opts.PasswordEnv != "YAVER_TEST_PASSWORD" {
		t.Fatalf("password env = %q", opts.PasswordEnv)
	}
	if opts.ConvexURL != "https://example.convex.site" {
		t.Fatalf("convex url = %q", opts.ConvexURL)
	}
	if !opts.RequireOwner {
		t.Fatal("require-owner not set")
	}
}

func TestParseEmailOAuthSetOptionsTypoAlias(t *testing.T) {
	opts, err := parseEmailOAuthSetOptions([]string{
		"--emaikl", "owner@example.com",
		"--password", "secret",
	})
	if err != nil {
		t.Fatalf("parseEmailOAuthSetOptions: %v", err)
	}
	if opts.Email != "owner@example.com" {
		t.Fatalf("email = %q", opts.Email)
	}
}

func TestParseEmailOAuthSetOptionsRequiresOnePasswordSource(t *testing.T) {
	if _, err := parseEmailOAuthSetOptions([]string{"--email", "owner@example.com"}); err == nil {
		t.Fatal("expected missing password error")
	}
	if _, err := parseEmailOAuthSetOptions([]string{
		"--email", "owner@example.com",
		"--password", "secret",
		"--password-env", "YAVER_TEST_PASSWORD",
	}); err == nil {
		t.Fatal("expected duplicate password source error")
	}
}

func TestParseEmailOAuthSetOptionsRemoteRequiresPasswordEnv(t *testing.T) {
	if _, err := parseEmailOAuthSetOptions([]string{
		"--email", "owner@example.com",
		"--password", "secret",
		"--machine", "magara",
	}); err == nil {
		t.Fatal("expected remote password-env requirement")
	}
	if _, err := parseEmailOAuthSetOptions([]string{
		"--email", "owner@example.com",
		"--password-env", "YAVER_TEST_PASSWORD",
		"--machine", "magara",
		"--print-token",
	}); err == nil {
		t.Fatal("expected remote print-token rejection")
	}
	opts, err := parseEmailOAuthSetOptions([]string{
		"--email", "owner@example.com",
		"--password-env", "YAVER_TEST_PASSWORD",
		"--machine", "magara",
	})
	if err != nil {
		t.Fatalf("parseEmailOAuthSetOptions: %v", err)
	}
	if opts.Machine != "magara" {
		t.Fatalf("machine = %q", opts.Machine)
	}
}

func TestResolveEmailOAuthPasswordFromStdin(t *testing.T) {
	got, err := resolveEmailOAuthPassword(emailOAuthSetOptions{PasswordStdin: true}, strings.NewReader("secret\n"))
	if err != nil {
		t.Fatalf("resolveEmailOAuthPassword: %v", err)
	}
	if got != "secret" {
		t.Fatalf("password = %q", got)
	}
}
