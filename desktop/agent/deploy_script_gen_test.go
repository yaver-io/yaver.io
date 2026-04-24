package main

import (
	"strings"
	"testing"
)

func TestGenerateDeployScriptCloudflare(t *testing.T) {
	script, err := GenerateDeployScript(DeployScriptSpec{
		App:    "web",
		Stack:  "nextjs",
		Target: "cloudflare",
		Path:   "/tmp/web",
	})
	if err != nil {
		t.Fatalf("GenerateDeployScript: %v", err)
	}
	mustContain := []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"yaver vault env --project web",
		"yaver doctor build --target=cloudflare",
		`cd "/tmp/web"`,
		"CLOUDFLARE_API_TOKEN",
		"npm run deploy",
	}
	for _, s := range mustContain {
		if !strings.Contains(script, s) {
			t.Errorf("generated script missing %q:\n%s", s, script)
		}
	}
}

func TestGenerateDeployScriptTestflight(t *testing.T) {
	script, err := GenerateDeployScript(DeployScriptSpec{
		App:    "mobile",
		Stack:  "react-native-expo",
		Target: "testflight",
		Path:   "/tmp/mobile",
	})
	if err != nil {
		t.Fatalf("GenerateDeployScript: %v", err)
	}
	for _, s := range []string{
		"xcodebuild",
		"APP_STORE_KEY_PATH",
		"APP_STORE_KEY_ISSUER",
		"app-store-connect",
		`cd "/tmp/mobile/ios"`,
		// Resumable-archive guarantees:
		"ARCHIVE=/tmp/yaver-deploy-mobile-testflight.xcarchive",
		"ApplicationProperties:CFBundleVersion",
		"Resuming: existing archive",
		"Archive kept at",
		// After a successful upload the archive is cleaned up.
		`rm -rf "$ARCHIVE"`,
	} {
		if !strings.Contains(script, s) {
			t.Errorf("testflight script missing %q", s)
		}
	}
	// Older, unscoped paths must not appear anywhere — two parallel
	// apps would race on them.
	for _, forbidden := range []string{
		"/tmp/yaver-deploy.xcarchive",
		"/tmp/yaver-deploy-build",
		"/tmp/yaver-deploy-ExportOptions.plist",
		"/tmp/yaver-deploy-export",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("testflight script still has unscoped path %q", forbidden)
		}
	}
}

func TestGenerateDeployScriptPlaystore(t *testing.T) {
	script, err := GenerateDeployScript(DeployScriptSpec{
		App:    "mobile",
		Stack:  "react-native-expo",
		Target: "playstore",
		Path:   "/tmp/mobile",
	})
	if err != nil {
		t.Fatalf("GenerateDeployScript: %v", err)
	}
	for _, s := range []string{
		"ANDROID_KEYSTORE_PASSWORD",
		"bundleRelease",
		"app-release.aab",
		// Resumable-AAB guarantees:
		"FP=/tmp/yaver-deploy-mobile-playstore.fp",
		"Resuming: existing AAB",
		// Fingerprint captures both versionCode + git HEAD.
		"vc=$CURRENT git=$GIT_SHA",
		"vc=$NEW git=$GIT_SHA",
		// Upload success clears fingerprint; failure keeps it.
		`rm -f "$FP"`,
	} {
		if !strings.Contains(script, s) {
			t.Errorf("playstore script missing %q", s)
		}
	}
}

func TestGenerateDeployScriptUnknown(t *testing.T) {
	_, err := GenerateDeployScript(DeployScriptSpec{
		App:    "x",
		Stack:  "nope",
		Target: "nowhere",
	})
	if err == nil {
		t.Fatal("expected error for unknown stack/target")
	}
	if !strings.Contains(err.Error(), "no template") {
		t.Fatalf("expected 'no template' error, got: %v", err)
	}
}

func TestGenerateDeployScriptValidation(t *testing.T) {
	if _, err := GenerateDeployScript(DeployScriptSpec{Stack: "nextjs", Target: "cloudflare"}); err == nil {
		t.Error("expected error when app missing")
	}
	if _, err := GenerateDeployScript(DeployScriptSpec{App: "x", Target: "cloudflare"}); err == nil {
		t.Error("expected error when stack missing")
	}
	if _, err := GenerateDeployScript(DeployScriptSpec{App: "x", Stack: "nextjs"}); err == nil {
		t.Error("expected error when target missing")
	}
}

func TestDeployTemplateNames(t *testing.T) {
	names := DeployTemplateNames()
	if len(names) < 4 {
		t.Fatalf("expected at least 4 templates, got %d: %v", len(names), names)
	}
	// Sanity: our core stack/target pairs must be present.
	required := []string{
		"react-native-expo:testflight",
		"react-native-expo:playstore",
		"nextjs:cloudflare",
		"convex:convex",
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, r := range required {
		if !have[r] {
			t.Errorf("missing required template: %q", r)
		}
	}
}

func TestBuildDoctorReport(t *testing.T) {
	// Without a vault — just checks toolchain probe path works and
	// produces a structured response.
	r, err := RunBuildDoctor("cloudflare", "web", nil)
	if err != nil {
		t.Fatalf("RunBuildDoctor: %v", err)
	}
	if r.Target != "cloudflare" || r.Project != "web" {
		t.Fatalf("wrong report meta: %+v", r)
	}
	// Tools should have been probed (3 of them: node/npm/wrangler).
	if len(r.Tools) != 3 {
		t.Fatalf("expected 3 tool probes, got %d", len(r.Tools))
	}
	// Secrets are probed against env vars only when vs is nil.
	if len(r.Secrets) != 2 {
		t.Fatalf("expected 2 secret probes (CLOUDFLARE_API_TOKEN/ACCOUNT_ID), got %d", len(r.Secrets))
	}
}

func TestBuildDoctorUnknownTarget(t *testing.T) {
	_, err := RunBuildDoctor("nowhere", "", nil)
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("expected 'unknown target' error, got: %v", err)
	}
}

func TestBuildDoctorWithVault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	vs, _ := NewVaultStore("p")

	_ = vs.Set(VaultEntry{Name: "CLOUDFLARE_API_TOKEN", Project: "web", Value: "scoped"})
	_ = vs.Set(VaultEntry{Name: "CLOUDFLARE_ACCOUNT_ID", Value: "global"})

	r, err := RunBuildDoctor("cloudflare", "web", vs)
	if err != nil {
		t.Fatalf("RunBuildDoctor: %v", err)
	}
	secretsByName := map[string]BuildSecretResult{}
	for _, s := range r.Secrets {
		secretsByName[s.Name] = s
	}
	if s := secretsByName["CLOUDFLARE_API_TOKEN"]; !s.Found || s.Source != "vault:project" {
		t.Errorf("project secret wrong: %+v", s)
	}
	if s := secretsByName["CLOUDFLARE_ACCOUNT_ID"]; !s.Found || s.Source != "vault:global" {
		t.Errorf("global fallback wrong: %+v", s)
	}
}
