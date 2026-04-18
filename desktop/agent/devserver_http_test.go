package main

import "testing"

func TestCanBootstrapPackageManager(t *testing.T) {
	if !canBootstrapPackageManager("yarn", true, false) {
		t.Fatal("expected yarn to be bootstrap-able with npm")
	}
	if !canBootstrapPackageManager("pnpm", false, true) {
		t.Fatal("expected pnpm to be bootstrap-able with corepack")
	}
	if canBootstrapPackageManager("bun", true, true) {
		t.Fatal("did not expect bun to be bootstrap-able from npm-only assumptions")
	}
}

func TestDefaultPackageManagerInstallSpec(t *testing.T) {
	if got := defaultPackageManagerInstallSpec("yarn"); got != "yarn@1.22.22" {
		t.Fatalf("unexpected yarn default spec: %s", got)
	}
	if got := defaultPackageManagerInstallSpec("pnpm"); got != "pnpm@latest" {
		t.Fatalf("unexpected pnpm default spec: %s", got)
	}
}
