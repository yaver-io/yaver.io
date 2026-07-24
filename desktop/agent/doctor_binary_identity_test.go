package main

import "testing"

// Pins the version-segment parser. The whole point of this check is telling a
// hot-swapped or stale binary apart from a published one, so a parser that
// mislabels a normal install would make the warning noise people learn to
// ignore.
func TestVersionSegmentFromYaverBinPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/.yaver/bin/1.99.349/darwin-arm64/yaver", "1.99.349"},
		{"/home/y/.yaver/bin/1.99.350/linux-x64/yaver", "1.99.350"},
		// `current` is the symlink, not a version — must not be reported.
		{"/Users/x/.yaver/bin/current/darwin-arm64/yaver", ""},
		// Not a managed layout: a source build or packaged install.
		{"/usr/local/bin/yaver", ""},
		{"/Users/x/Workspace/yaver.io/desktop/agent/yaver", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := versionSegmentFromYaverBinPath(c.in); got != c.want {
			t.Fatalf("versionSegmentFromYaverBinPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDescribeBinaryIdentityAlwaysAnswers(t *testing.T) {
	// Must never panic and must always report the build's own version — a
	// diagnostic that can fail is not a diagnostic.
	id := DescribeBinaryIdentity()
	if id.ReportedVersion != version {
		t.Fatalf("ReportedVersion = %q, want %q", id.ReportedVersion, version)
	}
	if id.ExecPath == "" && id.Warning == "" {
		t.Fatal("with no exec path it must at least explain why")
	}
	// A clean, non-managed build (how tests run) must produce NO warning —
	// otherwise the signal is worthless.
	if id.VersionDirOnPath == "" && id.VersionMismatch {
		t.Fatal("mismatch reported without a version directory to compare against")
	}
}
