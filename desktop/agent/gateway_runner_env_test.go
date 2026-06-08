package main

import (
	"strings"
	"testing"
)

func TestCleanTenantEnvStripsSecrets(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/tenant",
		"SHELL=/bin/bash",
		"TERM=xterm",
		"LANG=en_US.UTF-8",
		// secrets that MUST be dropped:
		"ANTHROPIC_API_KEY=sk-ant-xxx",
		"OPENAI_API_KEY=sk-xxx",
		"GLM_API_KEY=ygw-no",
		"ZAI_API_KEY=zzz",
		"AWS_SECRET_ACCESS_KEY=aws",
		"GITHUB_TOKEN=ghp_x",
		"YAVER_AUTH_TOKEN=yav",
		"CONVEX_DEPLOY_KEY=cvx",
		"SOME_PASSWORD=p",
		"MY_PRIVATE_KEY=k",
		"RELAY_PASSWORD=r",
	}
	got := cleanTenantEnv(base)
	gotSet := map[string]bool{}
	for _, kv := range got {
		gotSet[kv] = true
	}
	// Benign vars survive.
	for _, keep := range []string{"PATH=/usr/bin:/bin", "HOME=/home/tenant", "SHELL=/bin/bash", "TERM=xterm", "LANG=en_US.UTF-8"} {
		if !gotSet[keep] {
			t.Errorf("expected to keep %q", keep)
		}
	}
	// No secret-shaped var survives.
	for _, kv := range got {
		name := kv[:strings.IndexByte(kv, '=')]
		if isSecretEnvName(name) {
			t.Errorf("secret env leaked through: %q", kv)
		}
	}
	if len(got) != 5 {
		t.Fatalf("expected exactly 5 benign vars, got %d: %v", len(got), got)
	}
}

func TestIsSecretEnvName(t *testing.T) {
	secret := []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GLM_API_KEY", "ZAI_API_KEY",
		"GITHUB_TOKEN", "YAVER_AUTH_TOKEN", "AWS_SECRET_ACCESS_KEY",
		"SOME_PASSWORD", "x_CREDENTIAL", "FOO_PRIVATE", "BAR_apikey",
	}
	for _, n := range secret {
		if !isSecretEnvName(n) {
			t.Errorf("%q should be flagged secret", n)
		}
	}
	benign := []string{"PATH", "HOME", "SHELL", "TERM", "LANG", "PWD", "USER", "TZ", "TMPDIR"}
	for _, n := range benign {
		if isSecretEnvName(n) {
			t.Errorf("%q should NOT be flagged secret", n)
		}
	}
}
