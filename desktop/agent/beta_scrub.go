package main

// beta_scrub.go — secret scrubbing for the beta "shared project" seeder.
//
// When the owner shares a REAL project (sfmg / carrotbet) into a beta
// tenant's partition so they can develop on it, the tenant gets the full
// source — which means any secret committed to or sitting in that tree
// (a .env, a keystore, a service-account JSON, an SSH key, the .yaver
// vault) would be handed to a stranger. This file is the allow/deny gate
// that decides what must NEVER be copied into a tenant partition.
//
// It is intentionally a PURE, table-tested function (no FS side effects in
// the matcher) so the one security-critical decision — "is this a secret?"
// — is verifiable in CI without a box. seedScrubbedCopy walks a source
// tree and copies everything EXCEPT matches, logging what it withheld.
//
// Fail-closed bias: when unsure, DENY. A withheld non-secret is a harmless
// missing file the agent can re-add explicitly; a copied secret is a leak.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// secretDirSegments: any path segment equal to one of these ⇒ the whole
// subtree is withheld (keys/, secrets/, .ssh/, .aws/, .yaver vault, the
// gnupg dir, etc.).
var secretDirSegments = map[string]bool{
	"keys":     true,
	"secrets":  true,
	"secret":   true,
	".ssh":     true,
	".aws":     true,
	".gcloud":  true,
	".gnupg":   true,
	".yaver":   true, // vault + auth-token-derived material
	".convex":  true, // local convex deploy keys
	".vercel":  true,
	".netlify": true,
}

// secretExactBasenames: exact filename match ⇒ withheld.
var secretExactBasenames = map[string]bool{
	".npmrc":                 true, // often carries an _authToken
	".netrc":                 true,
	".pypirc":                true,
	"credentials":            true,
	"credentials.json":       true,
	"keystore.properties":     true, // android signing (CLAUDE.md force-tracked)
	"google-services.json":    true,
	"googleservice-info.plist": true, // keys are lowercase (base is lowercased before lookup)
	"terraform.tfstate":       true, // state files embed secrets
	"terraform.tfvars":        true,
}

// secretExts: file extension ⇒ withheld (private keys, keystores, certs).
var secretExts = map[string]bool{
	".pem":      true,
	".key":      true,
	".p8":       true, // Apple App Store Connect key
	".p12":      true,
	".pfx":      true,
	".keystore": true,
	".jks":      true,
	".mobileprovision": true,
}

// isBetaSecretPath reports whether a project-relative path must be withheld
// from a tenant partition. rel uses OS separators; matching is
// case-insensitive (macOS/Windows) and segment-aware.
func isBetaSecretPath(rel string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" {
		return false
	}
	lower := strings.ToLower(rel)
	segs := strings.Split(lower, "/")
	base := segs[len(segs)-1]

	// Never seed the source repo's git metadata — a fresh clone is made by
	// the broker; .git can also carry credentialed remotes / packed refs.
	for _, s := range segs {
		if s == ".git" || secretDirSegments[s] {
			return true
		}
	}

	if secretExactBasenames[base] {
		return true
	}
	if secretExts[filepath.Ext(base)] {
		return true
	}

	// dotenv family: .env, .env.local, .env.production, env.local … but NOT
	// .env.example / .env.sample / .env.template (those are safe scaffolds).
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasPrefix(base, "env.") {
		if strings.Contains(base, "example") || strings.Contains(base, "sample") ||
			strings.Contains(base, "template") || strings.HasSuffix(base, ".dist") {
			return false
		}
		return true
	}

	// SSH private keys by name (id_rsa, id_ed25519, …) — the matching .pub is
	// public and already allowed by ext (no .pub in secretExts).
	if base == "id_rsa" || strings.HasPrefix(base, "id_rsa") && !strings.HasSuffix(base, ".pub") {
		return true
	}
	if (strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa")) && !strings.HasSuffix(base, ".pub") {
		return true
	}

	// service-account / play / app-store key JSONs by name.
	if strings.HasSuffix(base, ".json") {
		if strings.Contains(base, "service-account") || strings.Contains(base, "serviceaccount") ||
			strings.Contains(base, "play-store") || strings.Contains(base, "playstore") ||
			strings.Contains(base, "appstore") || strings.Contains(lower, "auth") && strings.Contains(base, "key") {
			return true
		}
	}

	return false
}

// scanBetaSecrets walks root and returns the relative paths that WOULD be
// withheld — useful for an owner dry-run ("show me what won't be shared")
// before seeding carrotbet/sfmg.
func scanBetaSecrets(root string) ([]string, error) {
	var hits []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if isBetaSecretPath(rel) {
			hits = append(hits, filepath.ToSlash(rel))
			if info.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return hits, err
}

// seedScrubbedCopy copies src → dst, withholding every secret path. It
// returns the list of withheld relative paths (for the seed audit log).
// Regular files only; symlinks are skipped (a symlink could point outside
// the tenant root or at a host secret). Fail-closed.
func seedScrubbedCopy(src, dst string) (withheld []string, err error) {
	walkErr := filepath.Walk(src, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if isBetaSecretPath(rel) {
			withheld = append(withheld, filepath.ToSlash(rel))
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			withheld = append(withheld, filepath.ToSlash(rel)+" (symlink, skipped)")
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFileMode(p, target, info.Mode().Perm())
	})
	if walkErr != nil {
		return withheld, walkErr
	}
	return withheld, nil
}

func copyFileMode(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

// betaSeedReport is the human-readable summary the owner sees after a seed.
func betaSeedReport(project string, withheld []string) string {
	if len(withheld) == 0 {
		return fmt.Sprintf("seeded %q — no secrets detected (verify manually for a private repo)", project)
	}
	return fmt.Sprintf("seeded %q — WITHHELD %d secret path(s): %s",
		project, len(withheld), strings.Join(withheld, ", "))
}
