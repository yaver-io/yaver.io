package main

import "testing"

// Regression guard for the false negative that made `yaver doctor` claim Xcode
// was missing on a working /Applications/Xcode-26.4.0.app install, because the
// check required the literal bundle name "Xcode.app/". Verified failing before
// the fix (Xcode-26.4.0.app → false) and passing after.
func TestXcodebuildPathIsRealXcode(t *testing.T) {
	cases := []struct {
		name     string
		resolved string
		want     bool
	}{
		{"canonical name", "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild", true},
		{"versioned name", "/Applications/Xcode-26.4.0.app/Contents/Developer/usr/bin/xcodebuild", true},
		{"beta name", "/Applications/Xcode-beta.app/Contents/Developer/usr/bin/xcodebuild", true},
		{"command line tools", "/Library/Developer/CommandLineTools/usr/bin/xcodebuild", false},
		{"bare dispatcher", "/usr/bin/xcodebuild", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := xcodebuildPathIsRealXcode(tc.resolved); got != tc.want {
				t.Fatalf("xcodebuildPathIsRealXcode(%q) = %v, want %v", tc.resolved, got, tc.want)
			}
		})
	}
}
