package testkit

import (
	"strings"
	"testing"
)

func TestBuildPlaywrightScriptStorageStateAndTrace(t *testing.T) {
	spec := &Spec{
		Name:   "pw",
		Target: TargetWebPlaywright,
		URL:    "http://127.0.0.1:3000",
		Steps:  []Step{{Goto: "/"}, {AssertText: "Talos"}},
	}
	script, labels := buildPlaywrightScript(spec, t.TempDir(), RunOptions{
		Headful:                true,
		PlaywrightStorageState: "/tmp/yaver-pw-state.json",
		PlaywrightTrace:        true,
	})

	for _, want := range []string{
		"import fs from 'node:fs';",
		"headless: false",
		"ctxOpts.storageState = storageStatePath",
		"ctx.tracing.start",
		"} finally {",
		"ctx.tracing.stop",
		"ctx.storageState({ path: storageStatePath })",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if len(labels) != 2 || labels[0] != "goto /" || labels[1] != "assert.text Talos" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestBuildPlaywrightScriptDefaultsHeadless(t *testing.T) {
	spec := &Spec{
		Name:   "pw",
		Target: TargetWebPlaywright,
		URL:    "http://127.0.0.1:3000",
		Steps:  []Step{{Goto: "/"}},
	}
	script, _ := buildPlaywrightScript(spec, t.TempDir(), RunOptions{})
	if !strings.Contains(script, "headless: true") {
		t.Fatalf("default script should be headless:\n%s", script)
	}
	if strings.Contains(script, "storageStatePath") {
		t.Fatalf("script should not reference storage state without option:\n%s", script)
	}
}
