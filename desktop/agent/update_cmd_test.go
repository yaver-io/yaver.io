package main

import (
	"path/filepath"
	"testing"
)

func TestNPMStaleRenameDestinationIsPackageScoped(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, ".yaver-cli-HUsaI2WO")
	stderr := "npm error code ENOTEMPTY\nnpm error syscall rename\nnpm error dest " + want + "\n"
	if got := npmStaleRenameDestination(stderr, root, "yaver-cli"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	outside := filepath.Join(filepath.Dir(root), ".yaver-cli-HUsaI2WO")
	if got := npmStaleRenameDestination("npm error ENOTEMPTY rename\nnpm error dest "+outside, root, "yaver-cli"); got != "" {
		t.Fatalf("accepted staging directory outside npm root: %q", got)
	}
	if got := npmStaleRenameDestination("npm error ENOTEMPTY rename\nnpm error dest "+filepath.Join(root, ".other-HUsaI2WO"), root, "yaver-cli"); got != "" {
		t.Fatalf("accepted another package's staging directory: %q", got)
	}
}

func TestNPMStaleRenameDestinationRequiresExactFailure(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, ".yaver-cli-HUsaI2WO")
	if got := npmStaleRenameDestination("npm error EACCES\nnpm error dest "+dest, root, "yaver-cli"); got != "" {
		t.Fatalf("accepted non-ENOTEMPTY failure: %q", got)
	}
}
