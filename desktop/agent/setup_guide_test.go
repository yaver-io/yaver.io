package main

import (
	"strings"
	"testing"
)

func TestSetupCatalogueWellFormed(t *testing.T) {
	seen := map[string]bool{}
	ids := map[string]bool{}
	for i := range setupTasks {
		ids[setupTasks[i].ID] = true
	}
	for _, task := range setupTasks {
		if task.ID == "" || task.Title == "" || task.Summary == "" {
			t.Errorf("task %q missing id/title/summary", task.ID)
		}
		if seen[task.ID] {
			t.Errorf("duplicate task id %q", task.ID)
		}
		seen[task.ID] = true

		switch task.Platform {
		case "apple", "google", "both":
		default:
			t.Errorf("task %q bad platform %q", task.ID, task.Platform)
		}
		switch task.Automation {
		case setupAuto, setupAssisted, setupManual:
		default:
			t.Errorf("task %q bad automation %q", task.ID, task.Automation)
		}
		if task.RouteURL != "" && !strings.HasPrefix(task.RouteURL, "https://") {
			t.Errorf("task %q RouteURL not https: %q", task.ID, task.RouteURL)
		}
		// Honesty rules: a manual or assisted step MUST route the user
		// somewhere (we never make them hunt for the page).
		if (task.Automation == setupManual || task.Automation == setupAssisted) && task.RouteURL == "" {
			t.Errorf("task %q is %s but has no RouteURL to route the user", task.ID, task.Automation)
		}
		// Every dependency must reference a real task.
		for _, dep := range task.DependsOn {
			if !ids[dep] {
				t.Errorf("task %q depends on unknown task %q", task.ID, dep)
			}
		}
	}
	// The whole normie journey must be represented.
	for _, must := range []string{
		"apple-account", "apple-asc-key", "apple-testflight", "apple-iap", "apple-signin",
		"google-account", "google-keystore", "google-internal", "google-iap", "google-signin",
	} {
		if !seen[must] {
			t.Errorf("catalogue missing required task %q", must)
		}
	}
}

func TestEvalSetupStatusSecrets(t *testing.T) {
	task := &setupTask{ID: "x", Automation: setupAssisted, NeedsSecret: []string{"A", "B"}}

	// All secrets present + known → done.
	if s := evalSetupStatus(task, map[string]bool{"A": true, "B": true}, true, nil); s != statusDone {
		t.Errorf("all-present should be done, got %s", s)
	}
	// Missing one → todo.
	if s := evalSetupStatus(task, map[string]bool{"A": true}, true, nil); s != statusTodo {
		t.Errorf("missing secret should be todo, got %s", s)
	}
	// Vault not readable → unknown (never falsely "todo").
	if s := evalSetupStatus(task, map[string]bool{}, false, nil); s != statusUnknown {
		t.Errorf("unknown vault should be unknown, got %s", s)
	}
}

func TestEvalSetupStatusManualAndBlocked(t *testing.T) {
	manual := &setupTask{ID: "m", Automation: setupManual}
	if s := evalSetupStatus(manual, nil, true, nil); s != statusManual {
		t.Errorf("manual task should be action, got %s", s)
	}

	// A task blocked by an unsatisfied dependency.
	dep := &setupTask{ID: "child", Automation: setupAuto, DependsOn: []string{"parent"}}
	doneByID := map[string]setupStatus{"parent": statusTodo}
	if s := evalSetupStatus(dep, map[string]bool{}, true, doneByID); s != statusBlocked {
		t.Errorf("unsatisfied dep should block, got %s", s)
	}
	// Satisfied dependency → not blocked (falls through to its own eval).
	doneByID["parent"] = statusDone
	if s := evalSetupStatus(dep, map[string]bool{}, true, doneByID); s == statusBlocked {
		t.Error("satisfied dep should not block")
	}
}

func TestSetupTaskByID(t *testing.T) {
	if _, ok := setupTaskByID("apple-testflight"); !ok {
		t.Error("apple-testflight should resolve")
	}
	if _, ok := setupTaskByID("nope"); ok {
		t.Error("unknown id should not resolve")
	}
}
