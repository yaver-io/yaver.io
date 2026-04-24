package main

import (
	"os"
	"strings"
	"sync"
)

var (
	runtimeVaultMu    sync.RWMutex
	runtimeVaultStore *VaultStore
)

func setRuntimeVaultStore(vs *VaultStore) {
	runtimeVaultMu.Lock()
	runtimeVaultStore = vs
	runtimeVaultMu.Unlock()
}

func currentRuntimeVaultStore() *VaultStore {
	runtimeVaultMu.RLock()
	defer runtimeVaultMu.RUnlock()
	return runtimeVaultStore
}

func providerEnvCandidates(name string) []string {
	switch strings.TrimSpace(name) {
	case "GLM_API_KEY":
		return []string{"GLM_API_KEY", "ZAI_API_KEY"}
	case "ZAI_API_KEY":
		return []string{"ZAI_API_KEY", "GLM_API_KEY"}
	default:
		return []string{strings.TrimSpace(name)}
	}
}

func hostSecretValue(name string) (string, string) {
	for _, candidate := range providerEnvCandidates(name) {
		if value := strings.TrimSpace(os.Getenv(candidate)); value != "" {
			return value, candidate
		}
	}
	vs := currentRuntimeVaultStore()
	if vs == nil {
		return "", ""
	}
	for _, candidate := range providerEnvCandidates(name) {
		entry, err := vs.Get("", candidate)
		if err != nil {
			continue
		}
		if value := strings.TrimSpace(entry.Value); value != "" {
			return value, "vault:" + candidate
		}
	}
	return "", ""
}

func collectHostSecretEnv(names []string) map[string]string {
	keys := map[string]string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if value, _ := hostSecretValue(name); value != "" {
			keys[name] = value
		}
	}
	return keys
}

