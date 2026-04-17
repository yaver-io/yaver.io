package main

import (
	"reflect"
	"testing"
)

func TestBuildAutoInitArgsIncludesRunner(t *testing.T) {
	body := AutoInitStart{
		Project: "demo",
		WorkDir: "/tmp/demo",
		Prompt:  "mobile monorepo",
		Engine:  "claude",
		Runner:  "codex",
		Output:  "init.md",
		Force:   true,
	}
	got := buildAutoInitArgs(body)
	want := []string{
		"autoinit", "demo",
		"--prompt", "mobile monorepo",
		"--engine", "claude",
		"--runner", "codex",
		"--output", "init.md",
		"--force",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildAutoInitArgs mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestBuildAutoInitArgsFallsBackToWorkDirBase(t *testing.T) {
	body := AutoInitStart{WorkDir: "/tmp/my-repo"}
	got := buildAutoInitArgs(body)
	want := []string{"autoinit", "my-repo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildAutoInitArgs mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
}
