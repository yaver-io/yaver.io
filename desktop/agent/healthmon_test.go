package main

import "testing"

func TestHealthMonitorEnsureDefaultTargetsAddsTalosWorker(t *testing.T) {
	hm := &HealthMonitor{
		targets:    make(map[string]*HealthTarget),
		statuses:   make(map[string]*HealthStatus),
		stopChs:    make(map[string]chan struct{}),
		configFile: t.TempDir() + "/healthmon.json",
	}

	hm.ensureDefaultTargets()

	if len(hm.targets) != 1 {
		t.Fatalf("expected 1 default target, got %d", len(hm.targets))
	}

	var found *HealthTarget
	for _, target := range hm.targets {
		found = target
		break
	}
	if found == nil {
		t.Fatal("expected a default target")
	}
	if found.URL != "https://talos.kivanccakmak.workers.dev" {
		t.Fatalf("expected talos worker URL, got %q", found.URL)
	}
	if found.Label != "Talos Cloudflare Worker" {
		t.Fatalf("expected talos worker label, got %q", found.Label)
	}
}

func TestHealthMonitorEnsureDefaultTargetsDoesNotDuplicateURL(t *testing.T) {
	existing := &HealthTarget{
		ID:    "existing",
		URL:   "https://talos.kivanccakmak.workers.dev",
		Label: "Existing Talos",
	}
	hm := &HealthMonitor{
		targets: map[string]*HealthTarget{
			existing.ID: existing,
		},
		statuses: map[string]*HealthStatus{
			existing.ID: {
				TargetID: existing.ID,
				URL:      existing.URL,
				Label:    existing.Label,
			},
		},
		stopChs:    make(map[string]chan struct{}),
		configFile: t.TempDir() + "/healthmon.json",
	}

	hm.ensureDefaultTargets()

	if len(hm.targets) != 1 {
		t.Fatalf("expected existing target to be reused without duplication, got %d targets", len(hm.targets))
	}
}
