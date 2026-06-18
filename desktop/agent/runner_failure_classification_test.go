package main

import (
	"errors"
	"strings"
	"testing"
)

func TestSoftRunnerFailureRejectsCodexAuthAndModelErrors(t *testing.T) {
	output := strings.Repeat("OpenAI Codex banner\n", 20) +
		`ERROR: {"type":"error","status":400,"error":{"type":"invalid_request_error","message":"The 'gpt-5.3-codex' model is not supported when using Codex with a ChatGPT account."}}`

	if isSoftRunnerFailure("codex", output, errors.New("exit status 1")) {
		t.Fatal("codex auth/model errors must be hard failures, not review/completed")
	}
}

func TestSoftRunnerFailureRejectsOpenCodeProviderTransportErrors(t *testing.T) {
	output := strings.Repeat("opencode session\n", 20) +
		`ERROR service=llm providerID=zai modelID=glm-4.7 error={"name":"AI_APICallError","cause":{"code":"FailedToOpenSocket"}} stream error`

	if isSoftRunnerFailure("opencode", output, errors.New("exit status 1")) {
		t.Fatal("opencode provider transport errors must be hard failures, not review/completed")
	}
}

func TestSoftRunnerFailureStillAllowsRunnerEOFAfterSubstantialOutput(t *testing.T) {
	output := strings.Repeat("OpenAI Codex produced useful output\n", 20)

	if !isSoftRunnerFailure("codex", output, errors.New("exit status 1")) {
		t.Fatal("expected substantial codex output without hard-error markers to stay soft")
	}
}

func TestCodexBuiltinDefaultModelMatchesCatalogue(t *testing.T) {
	if got := GetRunnerConfig("codex").Model; got != "gpt-5.4" {
		t.Fatalf("codex default model = %q, want gpt-5.4", got)
	}
}
