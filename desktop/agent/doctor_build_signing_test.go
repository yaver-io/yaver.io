package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tests here deliberately avoid invoking `security`/`codesign`: those are
// machine-state dependent (and on a Mac with a locked keychain they'd be
// testing the operator's laptop, not this code). The exec-backed probes are
// thin wrappers; the logic worth pinning is the parsing, the identity
// preference order, and the remedy selection — that last one being the part
// whose absence cost a full session.

func TestIdentityLineParsesSecurityOutput(t *testing.T) {
	// Real `security find-identity -v -p codesigning` output shape.
	const out = `  1) C045746D9D8D3A2990EE66676AC555101CDD8155 "Apple Distribution: ACME LTD (5SJZ4KA39A)"
  2) E60465099CBA8358CD1CB20AD3C48A6321449F41 "Apple Development: Jane Dev (YBZM298HVG)"
     2 valid identities found`

	var got []signingIdentity
	for _, line := range strings.Split(out, "\n") {
		if m := identityLine.FindStringSubmatch(line); m != nil {
			got = append(got, signingIdentity{Hash: m[1], Name: m[2]})
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 identities, got %d (%+v)", len(got), got)
	}
	if got[0].Hash != "C045746D9D8D3A2990EE66676AC555101CDD8155" {
		t.Errorf("hash mismatch: %q", got[0].Hash)
	}
	if got[0].Name != "Apple Distribution: ACME LTD (5SJZ4KA39A)" {
		t.Errorf("name mismatch: %q", got[0].Name)
	}
	// The trailing "2 valid identities found" line must not parse as an identity.
	for _, id := range got {
		if strings.Contains(id.Name, "valid identities") {
			t.Errorf("summary line parsed as identity: %+v", id)
		}
	}
}

func TestPreferredSigningIdentityPrefersDistribution(t *testing.T) {
	// A store export cannot use a Development cert. If Development were
	// picked, the doctor would green-light a machine that fails at export.
	ids := []signingIdentity{
		{Hash: "AAA", Name: "Apple Development: Jane Dev (TEAM)"},
		{Hash: "BBB", Name: "Apple Distribution: ACME LTD (TEAM)"},
	}
	got, ok := preferredSigningIdentity(ids)
	if !ok {
		t.Fatal("expected an identity")
	}
	if got.Hash != "BBB" {
		t.Errorf("expected Distribution (BBB), got %+v", got)
	}
}

func TestPreferredSigningIdentityEmpty(t *testing.T) {
	if _, ok := preferredSigningIdentity(nil); ok {
		t.Error("expected ok=false for no identities")
	}
}

func TestSigningRemedyNamesTheRealFixForInternalComponent(t *testing.T) {
	// The whole point of this file: errSecInternalComponent must NOT be
	// reported as a generic signing problem. It has to name the private-key
	// path, set-key-partition-list, and the "login password won't help" trap.
	got := signingRemedy("/tmp/probe: errSecInternalComponent", "yaver-ci.keychain", true)
	for _, want := range []string{
		"PRIVATE KEY",
		"set-key-partition-list",
		"unlock-keychain",
		"YAVER_SIGNING_KEYCHAIN",
		"login password",
		"yaver-ci.keychain",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("remedy missing %q\ngot: %s", want, got)
		}
	}
}

func TestSigningRemedyLockedWithoutInternalComponent(t *testing.T) {
	got := signingRemedy("some other failure", "login.keychain", true)
	if !strings.Contains(got, "locked") {
		t.Errorf("expected a locked-keychain remedy, got: %s", got)
	}
}

func TestSigningRemedyHandlesUnknownKeychain(t *testing.T) {
	// locateIdentityKeychain returns "" when it can't attribute the identity;
	// the remedy must still be readable rather than interpolating an empty
	// string into the shell commands it suggests.
	got := signingRemedy("errSecInternalComponent", "", false)
	if strings.Contains(got, "  ") || strings.Contains(got, "-p <pw> \n") {
		t.Errorf("empty keychain produced malformed remedy: %s", got)
	}
	if !strings.Contains(got, "<keychain") {
		t.Errorf("expected a placeholder for the unknown keychain, got: %s", got)
	}
}

func TestCheckDiskHeadroomDisabledWhenNoRequirement(t *testing.T) {
	st := checkDiskHeadroom(0)
	if st.Checked {
		t.Errorf("expected no check when requiredGB=0, got %+v", st)
	}
}

func TestCheckDiskHeadroomReportsFreeSpace(t *testing.T) {
	// 1 GB is low enough that any machine running tests passes — we're
	// asserting the plumbing (statfs reached, fields populated), not the
	// specific capacity of the host.
	st := checkDiskHeadroom(1)
	if !st.Checked {
		t.Skip("statfs unavailable on this host")
	}
	if st.FreeGB <= 0 {
		t.Errorf("expected positive free space, got %+v", st)
	}
	if st.RequiredGB != 1 {
		t.Errorf("RequiredGB not echoed back: %+v", st)
	}
	if st.Mount == "" {
		t.Error("expected a mount point")
	}
}

func TestCheckDiskHeadroomFailsWhenBelowRequirement(t *testing.T) {
	// An absurd requirement must produce OK=false rather than silently
	// passing — the 2026-07-19 incident was a green doctor on 162 MB free.
	st := checkDiskHeadroom(1 << 20) // 1 PB
	if !st.Checked {
		t.Skip("statfs unavailable on this host")
	}
	if st.OK {
		t.Errorf("expected OK=false for an unmeetable requirement, got %+v", st)
	}
}

// Store targets must declare both new requirements, otherwise the checks
// silently never run for the target that motivated them.
func TestTestflightTargetDeclaresSigningAndDiskRequirements(t *testing.T) {
	tf, ok := buildTargets["testflight"]
	if !ok {
		t.Fatal("testflight target missing from catalogue")
	}
	if !tf.NeedsCodesign {
		t.Error("testflight must set NeedsCodesign — it produces a signed Apple binary")
	}
	if tf.MinFreeGB < 20 {
		t.Errorf("testflight MinFreeGB=%d — an iOS archive needs ~20 GB", tf.MinFreeGB)
	}
}

func TestTVOSTargetDeclaresSigningAndDiskRequirements(t *testing.T) {
	tv, ok := buildTargets["tvos"]
	if !ok {
		t.Fatal("tvos target missing from catalogue")
	}
	if !tv.NeedsCodesign {
		t.Error("tvos must set NeedsCodesign — App Store Connect upload needs a signed Apple TV archive")
	}
	if tv.MinFreeGB < 10 {
		t.Errorf("tvos MinFreeGB=%d — a tvOS archive/export needs real disk headroom", tv.MinFreeGB)
	}
	if len(tv.Secrets) == 0 {
		t.Error("tvos must declare App Store Connect secrets so doctor/deploy panes can report missing upload credentials")
	}
}

// The report is consumed over HTTP/MCP by the mobile + web deploy panes;
// the new blocks must survive a JSON round-trip.
func TestBuildDoctorReportRoundTripsSigningAndDisk(t *testing.T) {
	in := BuildDoctorReport{
		Target: "testflight",
		OK:     false,
		Signing: &BuildSigningStatus{
			Checked: true, CanSign: false, Locked: true,
			Identity: "Apple Distribution: ACME LTD (TEAM)",
			Keychain: "yaver-ci.keychain",
			Error:    "errSecInternalComponent",
			Remedy:   "unlock it",
		},
		Disk: &BuildDiskStatus{Checked: true, OK: false, FreeGB: 0.2, RequiredGB: 20, Mount: "/tmp"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out BuildDoctorReport
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Signing == nil || out.Signing.CanSign {
		t.Errorf("signing block lost or wrong: %+v", out.Signing)
	}
	if out.Signing.Keychain != "yaver-ci.keychain" {
		t.Errorf("keychain lost: %+v", out.Signing)
	}
	if out.Disk == nil || out.Disk.OK || out.Disk.RequiredGB != 20 {
		t.Errorf("disk block lost or wrong: %+v", out.Disk)
	}
	// Privacy: the identity name is fine, but nothing here should carry an
	// absolute home path.
	if strings.Contains(string(b), "/Users/") {
		t.Errorf("payload leaks a home path: %s", b)
	}
}
