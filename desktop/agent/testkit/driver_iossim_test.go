package testkit

import "testing"

func TestPickSimulatorFallsBackToVisionProWhenNoIPhoneInstalled(t *testing.T) {
	out := `
-- visionOS 2.4 --
    Apple Vision Pro (11111111-2222-3333-4444-555555555555) (Shutdown)
`
	got, ok := pickSimulatorFromList(out, "")
	if !ok {
		t.Fatal("pickSimulatorFromList() did not find a simulator")
	}
	if got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("pickSimulatorFromList() = %q, want Vision Pro UDID", got)
	}
}

func TestPickSimulatorPrefersIPhoneWhenAvailable(t *testing.T) {
	out := `
-- visionOS 2.4 --
    Apple Vision Pro (11111111-2222-3333-4444-555555555555) (Booted)
-- iOS 18.4 --
    iPhone 16 Pro (aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee) (Shutdown)
`
	got, ok := pickSimulatorFromList(out, "")
	if !ok {
		t.Fatal("pickSimulatorFromList() did not find a simulator")
	}
	if got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("pickSimulatorFromList() = %q, want already-booted Vision Pro UDID", got)
	}

	got, ok = pickSimulatorFromList(out, "iPhone")
	if !ok {
		t.Fatal("pickSimulatorFromList(iPhone) did not find a simulator")
	}
	if got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("pickSimulatorFromList(iPhone) = %q, want iPhone UDID", got)
	}
}
