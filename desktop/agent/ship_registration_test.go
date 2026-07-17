package main

import "testing"

// `yaver diagnose` shipped in 1.99.33 as a function that existed but was never
// wired to the mux. This asserts the barrier is actually reachable rather than
// merely written — the verbs are how every non-terminal surface (phone, watch,
// voice) reaches it, which is the whole point of the feature.
func TestShipVerbsAreRegistered(t *testing.T) {
	for _, name := range []string{
		"ship",
		"ship_status",
		"ship_prompts",
		"autorun_pause_all",
		"autorun_resume_all",
	} {
		spec, ok := lookupOpsVerb(name)
		if !ok {
			t.Errorf("ops verb %q is not registered — it cannot be called from any surface", name)
			continue
		}
		if spec.Handler == nil {
			t.Errorf("ops verb %q has a nil handler", name)
		}
		if spec.Schema == nil {
			t.Errorf("ops verb %q has no schema; agents cannot call what they cannot see", name)
		}
		if spec.Description == "" {
			t.Errorf("ops verb %q has no description", name)
		}
	}
}

// The barrier is owner-only. A guest with a deploy scope must not be able to
// freeze the owner's whole fleet.
func TestShipVerbsAreOwnerOnly(t *testing.T) {
	for _, name := range []string{"ship", "autorun_pause_all", "autorun_resume_all"} {
		spec, ok := lookupOpsVerb(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if spec.AllowGuest {
			t.Errorf("%q allows guests; freezing the fleet and deploying must stay owner-only", name)
		}
	}
}
