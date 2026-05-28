package main

// voice_credentials.go — vendor-agnostic credential resolution for the
// voice subsystem. Every STT/TTS provider obtains its API key (or OAuth
// refresh token) through LookupVoiceCredential so a single place owns
// the storage order, the legacy-config fallback, and the P2P-sync story.
//
// Resolution order, first non-empty wins:
//
//  1. Vault entry  (project="voice", name="<provider>-<kind>")
//  2. Environment  YAVER_VOICE_<PROVIDER>_<KIND>
//  3. Legacy field (caller passes the value read from cfg.Voice.X — kept
//     so existing OpenAI/Deepgram/Cartesia installs don't break)
//
// New keys written via POST /voice/config land in the vault, which:
//   - Encrypts at rest (NaCl secretbox, key from EnsureMasterKey)
//   - Syncs P2P to the user's other devices via the existing owner-
//     authenticated agent-to-agent vault sync
//   - NEVER reaches Convex (privacy contract enforced by
//     convex_privacy_test.go's forbidden-keys fence)
//
// "Provider" is the vendor slug ("openai", "deepgram", "cartesia",
// "elevenlabs", "assemblyai", "google", "soniox", ...).  "Kind" is the
// credential type ("api-key", "refresh-token", "voice-id", ...).

import (
	"os"
	"strings"
)

// VoiceCredentialProject is the vault project all voice credentials
// live under. One project means `yaver vault list --project voice`
// shows every speech key in one place, and the existing project-key
// validation rules already cover it.
const VoiceCredentialProject = "voice"

// LookupVoiceCredential returns the first non-empty source for
// (provider, kind). legacyFallback is the value the caller already
// has from cfg.Voice.* — pass "" if there is no legacy field.
//
// Never logs the value. Caller is responsible for treating a non-empty
// return as a secret.
func LookupVoiceCredential(provider, kind, legacyFallback string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	kind = strings.ToLower(strings.TrimSpace(kind))
	if provider == "" || kind == "" {
		return strings.TrimSpace(legacyFallback)
	}

	// 1. Vault
	if vs, err := openVaultE(); err == nil && vs != nil {
		if entry, gerr := vs.Get(VoiceCredentialProject, provider+"-"+kind); gerr == nil {
			if v := strings.TrimSpace(entry.Value); v != "" {
				return v
			}
		}
	}

	// 2. Environment
	envName := "YAVER_VOICE_" +
		strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_" +
		strings.ToUpper(strings.ReplaceAll(kind, "-", "_"))
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		return v
	}

	// 3. Legacy config.json field passed by caller
	return strings.TrimSpace(legacyFallback)
}

// SetVoiceCredential writes a credential to the vault. Used by the
// /voice/config HTTP handler when the user pastes a key in mobile
// Settings or completes an OAuth flow.
//
// Pass value="" to delete the entry (soft tombstone, propagates to
// peer devices on next sync).
func SetVoiceCredential(provider, kind, value string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	kind = strings.ToLower(strings.TrimSpace(kind))
	if provider == "" || kind == "" {
		return nil
	}
	vs, err := openVaultE()
	if err != nil {
		return err
	}
	name := provider + "-" + kind
	value = strings.TrimSpace(value)
	if value == "" {
		// Soft-delete; tolerate "entry not found" since the user may
		// be clearing a never-set key.
		_ = vs.Delete(VoiceCredentialProject, name)
		return nil
	}
	return vs.Set(VaultEntry{
		Name:     name,
		Project:  VoiceCredentialProject,
		Category: "api-key",
		Value:    value,
	})
}

// HasVoiceCredential reports whether a credential is set in vault OR
// env OR legacyFallback. UI surfaces this so the user sees which
// providers are configured without ever echoing the secret back.
func HasVoiceCredential(provider, kind, legacyFallback string) bool {
	return LookupVoiceCredential(provider, kind, legacyFallback) != ""
}
