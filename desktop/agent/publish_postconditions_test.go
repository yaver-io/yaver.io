package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMicrosoftSubmissionProofRequiresRemoteID(t *testing.T) {
	id, err := microsoftSubmissionID("progress\n{\"submissionId\":\"sub-123\",\"pollingUrl\":\"https://example.test\"}")
	if err != nil || id != "sub-123" {
		t.Fatalf("receipt = %q err=%v", id, err)
	}
	if _, err := microsoftSubmissionID(`{"submissionId":null}`); err == nil {
		t.Fatal("null submission ID must not prove publication")
	}
}

func TestManifestNameVersionParsesPythonAndPubspec(t *testing.T) {
	dir := t.TempDir()
	pyproject := filepath.Join(dir, "pyproject.toml")
	if err := os.WriteFile(pyproject, []byte("[project]\nname = \"acme\"\nversion = \"1.2.3\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, version, err := manifestNameVersion(pyproject)
	if err != nil || name != "acme" || version != "1.2.3" {
		t.Fatalf("pyproject parsed as %q %q err=%v", name, version, err)
	}
	pubspec := filepath.Join(dir, "pubspec.yaml")
	if err := os.WriteFile(pubspec, []byte("name: acme_flutter\nversion: 2.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, version, err = manifestNameVersion(pubspec)
	if err != nil || name != "acme_flutter" || version != "2.0.1" {
		t.Fatalf("pubspec parsed as %q %q err=%v", name, version, err)
	}
}

func TestUnknownPostconditionFailsClosed(t *testing.T) {
	pm := &PublishManager{runs: map[string]*PublishRun{}}
	run := &PublishRun{ID: "x"}
	err := pm.verifyPostconditions(run, PublishTarget{Postconditions: []PublishPostcond{{Kind: "wishful-thinking"}}}, "ok")
	if err == nil || len(run.Proofs) != 1 || run.Proofs[0].OK {
		t.Fatalf("unknown proof must fail closed: err=%v proofs=%+v", err, run.Proofs)
	}
}
