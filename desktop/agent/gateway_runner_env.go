package main

import (
	"os"
	"strings"
)

// Tenant runners inherit only a scrubbed process environment. Yaver does not
// mint or inject hosted-inference credentials: model credentials belong to the
// trusted endpoint or user-owned runner that calls the provider directly.

var secretEnvNameSubstrings = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE",
	"APIKEY", "AUTH",
}

var secretEnvPrefixes = []string{
	"ANTHROPIC_", "OPENAI_", "GLM_", "ZAI_", "AWS_", "GCP_", "GOOGLE_",
	"AZURE_", "CLOUDFLARE_", "CF_", "CONVEX_", "LEMONSQUEEZY_", "HCLOUD_",
	"NPM_", "GITHUB_", "GITLAB_", "YAVER_", "DEEPINFRA_", "OPENROUTER_",
	"DEEPGRAM_", "CARTESIA_", "STRIPE_", "DO_", "DIGITALOCEAN_",
}

func isSecretEnvName(name string) bool {
	up := strings.ToUpper(name)
	for _, p := range secretEnvPrefixes {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	for _, s := range secretEnvSubstr() {
		if strings.Contains(up, s) {
			return true
		}
	}
	return false
}

func secretEnvSubstr() []string { return secretEnvNameSubstrings }

// cleanTenantEnv strips secret-shaped variables from a base environment so a
// tenant process inherits none of the operator's keys or tokens.
func cleanTenantEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv)
			continue
		}
		if isSecretEnvName(kv[:eq]) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// tenantRunnerBaseEnv never injects a provider or Yaver gateway credential.
func (s *HTTPServer) tenantRunnerBaseEnv(tenantUserID string) []string {
	_ = s
	_ = tenantUserID
	return cleanTenantEnv(os.Environ())
}
