package main

// vault_rekey.go — keep the encrypted vault openable across auth-token
// rotations.
//
// Background. The vault key is derived from cfg.AuthToken via
// DerivePassphraseFromToken (vault.go) → Argon2id. Every time the
// agent replaces cfg.AuthToken (fresh login, account recovery, /auth/
// refresh ?rotate=1, pairing, bootstrap, MCP authLogin) the vault file
// stops decrypting under the new token. Historically the only
// recovery was YAVER_VAULT_PASSPHRASE=<old-token>, which the user
// rarely had — so re-auth silently bricked every stored entry.
//
// SetAuthToken is the single hook every token-write site should call
// instead of `cfg.AuthToken = newToken; SaveConfig(cfg)`. It:
//
//   1. Best-effort opens the existing vault under the OLD token and
//      rekeys it to the NEW token (RekeyTo writes a fresh salt+nonce
//      and atomically swaps vault.enc).
//   2. Stashes the old token in cfg.PreviousAuthToken so openVault has
//      a fallback if step 1 was skipped (older agent on disk had
//      already rotated, partial write, etc.). openVault auto-rekeys +
//      clears PreviousAuthToken on use.
//   3. Persists cfg via SaveConfig.
//
// All steps are best-effort except (3). A vault rekey failure is
// logged but does not block the token write — the worst case is the
// user falls through to the PreviousAuthToken fallback path on next
// vault access, which still recovers without re-authentication.

import (
	"log"
	"os"
	"strings"
)

// SetAuthToken updates cfg.AuthToken to newToken and rekeys the
// existing vault (if any) so it can be read under the new token.
// Stashes the previous token in cfg.PreviousAuthToken as a fallback
// in case the rekey step itself was skipped or failed. Persists.
//
// Callers MUST go through this instead of mutating cfg.AuthToken
// directly, otherwise the vault re-locks. The one exception is
// SaveConfigClearingAuth used for sign-out / factory-reset, which
// deliberately wipes the token — those paths can also wipe
// PreviousAuthToken.
func SetAuthToken(cfg *Config, newToken string) error {
	oldToken := strings.TrimSpace(cfg.AuthToken)
	newToken = strings.TrimSpace(newToken)
	if oldToken == newToken {
		// No-op rotation. Still call SaveConfig so the rest of cfg
		// gets persisted if the caller mutated other fields.
		return SaveConfig(cfg)
	}

	if oldToken != "" {
		rekeyVaultBetweenTokens(oldToken, newToken)
		cfg.PreviousAuthToken = oldToken
	} else {
		// First-ever token write: nothing to rekey from. Clear any
		// stale PreviousAuthToken (e.g. left over from a deleted
		// vault that the user re-paired).
		cfg.PreviousAuthToken = ""
	}
	cfg.AuthToken = newToken
	return SaveConfig(cfg)
}

// rekeyVaultBetweenTokens opens vault.enc under the passphrase derived
// from oldToken and re-encrypts it under the passphrase derived from
// newToken. Best-effort:
//
//   - vault.enc absent → nothing to migrate, return cleanly.
//   - YAVER_VAULT_PASSPHRASE set → user picked a manual passphrase
//     decoupled from auth tokens; rotation is a no-op for them.
//   - oldToken decrypt fails → vault was already locked under a
//     different key (probably an even-older token). Leave it; the
//     PreviousAuthToken fallback in openVault will catch any later
//     rotations.
//   - rekey write fails → log + continue. The PreviousAuthToken
//     fallback covers us.
//
// When the agent has a live runtime VaultStore (the long-lived one
// held by HTTPServer for /vault/sync, /vault/push, etc.), rekey it
// in place rather than opening a fresh copy from disk. Opening a
// fresh copy + RekeyTo would update vault.enc but leave the live
// store still encrypting writes under the OLD key — so any sync /
// push handler that runs after rotation would silently regress
// vault.enc back to the old key, and the next rotation would lose
// the trail (current and previous tokens no longer match what's on
// disk → "wrong passphrase or corrupted vault" until the user
// supplies YAVER_VAULT_PASSPHRASE manually).
func rekeyVaultBetweenTokens(oldToken, newToken string) {
	if strings.TrimSpace(os.Getenv("YAVER_VAULT_PASSPHRASE")) != "" {
		return
	}
	if vs := currentRuntimeVaultStore(); vs != nil {
		if err := vs.RekeyTo(DerivePassphraseFromToken(newToken)); err != nil {
			log.Printf("[vault] rekey runtime store to new token failed: %v", err)
		}
		return
	}
	path, err := VaultPath()
	if err != nil {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	vs, err := NewVaultStore(DerivePassphraseFromToken(oldToken))
	if err != nil {
		log.Printf("[vault] rekey: cannot open under previous token: %v", err)
		return
	}
	if err := vs.RekeyTo(DerivePassphraseFromToken(newToken)); err != nil {
		log.Printf("[vault] rekey to new token failed: %v", err)
	}
}
