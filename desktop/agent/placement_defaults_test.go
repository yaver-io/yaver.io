package main

import "testing"

func TestDefaultWorkspacePlacement(t *testing.T) {
	// No stack at all → the creation default.
	got := DefaultWorkspacePlacement(nil)
	if got.Stack != "react-native-expo" || got.Preview != PreviewBrowser || got.MachineClass != "standard" {
		t.Fatalf("nil detection: got %+v", got)
	}
	// Redroid tags force a bigger class — the one workload that does.
	d := &StackDetection{Frameworks: []string{"react-native"}, Tags: []string{"redroid"}}
	if c := defaultMachineClassForStack(d); c != "build" {
		t.Fatalf("redroid should force build, got %q", c)
	}
	// Monorepo starts one class up (Metro's known 4GB ceiling).
	m := &StackDetection{Frameworks: []string{"nextjs"}, IsMonorepo: true}
	if c := defaultMachineClassForStack(m); c != "heavy" {
		t.Fatalf("monorepo should be heavy, got %q", c)
	}
	// Default preview must NEVER be redroid.
	for _, s := range []string{"nextjs", "vite", "flutter", "react-native", "react-native-expo", ""} {
		if p := DefaultPreviewModeForStack(s); p == PreviewRedroid {
			t.Fatalf("stack %q defaulted to redroid", s)
		}
	}
}
