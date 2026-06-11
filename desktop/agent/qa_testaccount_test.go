package main

import (
	"strings"
	"testing"

	"github.com/yaver-io/agent/studio"
)

func TestApplyTestAccountTemplate(t *testing.T) {
	flows := []studio.Scenario{{
		Name: "signup",
		Goal: "sign up as {{fullName}} with {{email}} / {{password}}",
		Expectations: []string{
			"account {{email}} reached onboarding",
			"no literal {{password}} visible",
		},
	}}
	acct := &qaTestAccount{
		Email:    "e2e-redroid-abc@yaver.test",
		Password: "pw-secret",
		FullName: "Redroid QA 1234",
	}

	applyTestAccountTemplate(flows, acct)

	if strings.Contains(flows[0].Goal, "{{") {
		t.Fatalf("goal still has placeholders: %q", flows[0].Goal)
	}
	for _, want := range []string{acct.FullName, acct.Email, acct.Password} {
		if !strings.Contains(flows[0].Goal, want) {
			t.Errorf("goal missing %q: %q", want, flows[0].Goal)
		}
	}
	if !strings.Contains(flows[0].Expectations[0], acct.Email) {
		t.Errorf("expectation not templated: %q", flows[0].Expectations[0])
	}
	if strings.Contains(flows[0].Expectations[1], "{{password}}") {
		t.Errorf("expectation still has placeholder: %q", flows[0].Expectations[1])
	}
}

func TestApplyTestAccountTemplateNilIsNoop(t *testing.T) {
	flows := []studio.Scenario{{Goal: "use {{email}}"}}
	applyTestAccountTemplate(flows, nil)
	if flows[0].Goal != "use {{email}}" {
		t.Fatalf("nil account should not mutate flows, got %q", flows[0].Goal)
	}
}

func TestFlowsReferenceTestAccount(t *testing.T) {
	with := []studio.Scenario{{Goal: "plain"}, {Goal: "needs {{password}}"}}
	if !flowsReferenceTestAccount(with) {
		t.Error("expected placeholder to be detected in goal")
	}
	viaExpectation := []studio.Scenario{{Goal: "plain", Expectations: []string{"saw {{email}}"}}}
	if !flowsReferenceTestAccount(viaExpectation) {
		t.Error("expected placeholder to be detected in expectation")
	}
	without := []studio.Scenario{{Goal: "no placeholders here", Expectations: []string{"all good"}}}
	if flowsReferenceTestAccount(without) {
		t.Error("did not expect a placeholder match")
	}
}

func TestResolveQAConvexURLOverride(t *testing.T) {
	if got := resolveQAConvexURL("https://example.convex.site/"); got != "https://example.convex.site" {
		t.Errorf("override not honored/trimmed: %q", got)
	}
	// Empty override falls back to config or the build default — never empty.
	if got := resolveQAConvexURL(""); got == "" {
		t.Error("resolved convex URL should never be empty")
	}
}
