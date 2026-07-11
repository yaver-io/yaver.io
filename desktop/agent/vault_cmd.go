package main

// vault_cmd.go — `yaver vault` CLI. Subcommands:
//
//   add      — create/update a secret (optionally --project X)
//   list     — list entries in a project (flag --project; "*" = every project)
//   get      — print a secret's value
//   delete   — soft-delete a secret (tombstone; propagates via sync)
//   export   — plaintext JSON dump (global + all projects, non-deleted)
//   import   — load plaintext JSON back
//   env      — emit shell "export KEY=VAL" lines for a project (for deploys)
//   exec     — run a command with the project env loaded
//   check    — test a passphrase without printing secrets
//   unlock   — rekey a passphrase-locked vault under the current auth token
//   lock     — rekey the vault under a manual passphrase
//   reset    — archive the current vault and create a fresh empty one
//   projects — list distinct projects
//   sync     — pull + push sync with a peer device (owner-auth, P2P)

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
)

func runVault(args []string) {
	if len(args) == 0 {
		printVaultUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "add":
		runVaultAdd(args[1:])
	case "list", "ls":
		runVaultList(args[1:])
	case "get":
		runVaultGet(args[1:])
	case "delete", "rm":
		runVaultDelete(args[1:])
	case "export":
		runVaultExport()
	case "import":
		runVaultImport(args[1:])
	case "env":
		runVaultEnv(args[1:])
	case "exec":
		runVaultExec(args[1:])
	case "check-passphrase", "check":
		runVaultCheckPassphrase(args[1:])
	case "unlock":
		runVaultUnlock(args[1:])
	case "lock":
		runVaultLock(args[1:])
	case "reset":
		runVaultReset(args[1:])
	case "projects":
		runVaultProjects()
	case "sync":
		runVaultSync(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown vault subcommand: %s\n", args[0])
		printVaultUsage()
		os.Exit(1)
	}
}

func printVaultUsage() {
	fmt.Print(`Usage:
  yaver vault add <name> [--project <p>] [--category <cat>] [--value <val>] [--notes <text>]
  yaver vault list [--project <p>|*]       List entries (empty=global, *=all)
  yaver vault get <name> [--project <p>]   Print secret value
  yaver vault delete <name> [--project <p>]Soft-delete (tombstone)
  yaver vault export                       Plaintext JSON dump (careful!)
  yaver vault import <file.json>           Import from plaintext JSON
  yaver vault env --project <p> [--no-globals]
                                           Emit shell export KEY=VAL lines
  yaver vault exec --project <p> -- <cmd ...>
                                           Run command with env loaded
  yaver vault check-passphrase             Test a passphrase without printing secrets
  yaver vault unlock                       Re-encrypt vault under current auth token
  yaver vault lock                         Re-encrypt vault under a manual passphrase
  yaver vault reset                        Archive old vault and create a fresh one
  yaver vault projects                     List distinct projects
  yaver vault sync [--from <deviceId>]     Pull + push with peer (P2P)

Categories: api-key, signing-key, ssh-key, git-credential, custom

The vault is encrypted at rest (NaCl secretbox + Argon2id). Unlock uses
your auth token by default; override with:
  YAVER_VAULT_PASSPHRASE=<pass> yaver vault ...
`)
}

// openVault is the CLI-facing wrapper. Logic lives in openVaultE so
// it can be unit-tested without exit-on-error semantics.
func openVault() *VaultStore {
	vs, err := openVaultE()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	return vs
}

// openVaultE loads the vault using auth token or custom passphrase.
//
// Order of attempts:
//
//  1. YAVER_VAULT_PASSPHRASE env var (manual override; no fallback,
//     no rekey — user picked a stable passphrase decoupled from auth).
//  2. Current cfg.AuthToken via DerivePassphraseFromToken.
//  3. cfg.PreviousAuthToken via the same derivation. On success,
//     auto-rekey the vault under the current token + clear
//     PreviousAuthToken. This is the recovery path for when
//     SetAuthToken couldn't rekey at rotation time (older agent on
//     disk had already written AuthToken, partial write, etc.).
//
// If all three fail with a "wrong passphrase" we return an error
// with the same guidance message as before so the user can supply
// YAVER_VAULT_PASSPHRASE manually.
func openVaultE() (*VaultStore, error) {
	// Manual override path stays passphrase-only. Documented use: an
	// operator who picked a stable passphrase explicitly decoupled from
	// auth doesn't want the v2 master-key path silently re-encrypting
	// the file under a new scheme. If they want v2, they can clear the
	// env var and re-run.
	if pass := os.Getenv("YAVER_VAULT_PASSPHRASE"); pass != "" {
		vs, err := NewVaultStoreWithDevice(pass, "")
		if err != nil {
			return nil, fmt.Errorf("Error opening vault: %v", err)
		}
		return vs, nil
	}

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		return nil, fmt.Errorf("Not authenticated. Run 'yaver auth' first.")
	}

	// v2 fast path — master key from ~/.yaver/master.key + macOS
	// Keychain mirror, gated by the user_id in master.key.meta.
	// Most calls land here once the user has migrated; the fallback
	// chain below is the one-time migration path on first run.
	userID := resolveUserIDForVault(cfg)
	if masterKey, mkErr := EnsureMasterKey(userID, cfg.DeviceID); mkErr == nil {
		vs, v2Err := NewVaultStoreV2(masterKey, cfg.DeviceID)
		if v2Err == nil {
			return vs, nil
		}
		if !errors.Is(v2Err, ErrVaultIsLegacyV1) {
			// A v2-magic file the current master key can't decrypt: the
			// sealing key is gone (master.key lost/swapped, or the file
			// came from another machine). No auth token recovers a v2
			// vault, so on a headless box this bricks EVERY vault op
			// forever — which is exactly how a cloud box's opencode GLM
			// key couldn't be stored. Two escapes:
			//   - YAVER_VAULT_AUTO_RESET=1 (set on cloud boxes): archive
			//     the dead file + start fresh under the current master
			//     key. Nothing recoverable is lost — the key is already
			//     gone — and the archive keeps the bytes for forensics.
			//   - otherwise: a clear error that points at the ACTUAL fix
			//     (`yaver vault reset`), not the v1-only YAVER_VAULT_PASSPHRASE.
			// Don't silently retry via v1 (that would mask a swapped
			// vault.enc); only self-heal when explicitly opted in.
			if isWrongPassphraseErr(v2Err) && vaultAutoResetEnabled() {
				if fresh, rErr := resetDeadVaultToV2(masterKey, cfg.DeviceID); rErr == nil {
					fmt.Fprintln(os.Stderr, "[vault] unreadable vault auto-reset (YAVER_VAULT_AUTO_RESET=1); previously stored secrets must be re-added.")
					return fresh, nil
				} else {
					return nil, fmt.Errorf("Error opening vault (v2): %v; auto-reset failed: %v", v2Err, rErr)
				}
			}
			return nil, fmt.Errorf("Error opening vault (v2): %v\n"+
				"This machine's master key can't decrypt the vault (key lost/swapped, or the file came from another machine). "+
				"No auth token recovers a v2 vault. Run `yaver vault reset --yes` to archive it and start fresh "+
				"(previously stored secrets are unrecoverable), or restore ~/.yaver/master.key. "+
				"On a headless box set YAVER_VAULT_AUTO_RESET=1 to self-heal automatically.", v2Err)
		}
		// File is legacy v1 — fall through to the passphrase chain
		// below. The successful unlock there triggers a one-time
		// RekeyToMasterKey(masterKey) migration.
	} else {
		// Master-key provisioning failed (e.g. ~/.yaver unwritable, or
		// another user's meta refusing us). Fall through to v1 — the
		// vault still works under the old scheme; auto-migration just
		// doesn't kick in until the next call.
		fmt.Fprintf(os.Stderr, "Warning: master-key unavailable (%v) — falling back to legacy v1 vault.\n", mkErr)
	}

	currentPass := DerivePassphraseFromToken(cfg.AuthToken)
	vs, err := NewVaultStoreWithDevice(currentPass, cfg.DeviceID)
	if err == nil {
		// v1 unlocked under the CURRENT token — migrate to v2 if a
		// master key is available so future opens skip this chain.
		migrateVaultToV2(vs, userID, cfg.DeviceID)
		return vs, nil
	}
	if !strings.Contains(err.Error(), "wrong passphrase") {
		return nil, fmt.Errorf("Error opening vault: %v", err)
	}

	// Current token didn't decrypt — walk the previous-token chain
	// (newest first), trying each. The single PreviousAuthToken field
	// is tried first for back-compat with configs written before the
	// chain existed. On first success we migrate to v2 (preferred —
	// breaks the rotation-corruption loop for good) or fall back to a
	// v1 rekey under the current token (older agents on the box
	// without keychain support).
	seen := map[string]bool{}
	candidates := make([]string, 0, 1+len(cfg.PreviousAuthTokens))
	if cfg.PreviousAuthToken != "" {
		candidates = append(candidates, cfg.PreviousAuthToken)
	}
	candidates = append(candidates, cfg.PreviousAuthTokens...)
	for _, tok := range candidates {
		tok = strings.TrimSpace(tok)
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		prevPass := DerivePassphraseFromToken(tok)
		vsPrev, prevErr := NewVaultStoreWithDevice(prevPass, cfg.DeviceID)
		if prevErr != nil {
			continue
		}
		if migrateVaultToV2(vsPrev, userID, cfg.DeviceID) {
			cfg.PreviousAuthToken = ""
			cfg.PreviousAuthTokens = nil
			if sErr := SaveConfig(cfg); sErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not clear previous-auth-token state from config: %v\n", sErr)
			}
			fmt.Fprintf(os.Stderr, "Vault migrated to v2 (keychain-backed) using a previous auth token — rotations no longer touch the vault.\n")
			return vsPrev, nil
		}
		// Keychain unavailable — fall back to the legacy v1 rekey
		// under the current auth token, same as before.
		if rkErr := vsPrev.RekeyTo(currentPass); rkErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: auto-rekey failed (%v) — vault still readable; will retry next call.\n", rkErr)
			return vsPrev, nil
		}
		cfg.PreviousAuthToken = ""
		cfg.PreviousAuthTokens = nil
		if sErr := SaveConfig(cfg); sErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not clear previous-auth-token state from config: %v\n", sErr)
		}
		fmt.Fprintf(os.Stderr, "Vault rekeyed automatically using a previous auth token.\n")
		return vsPrev, nil
	}

	return nil, fmt.Errorf("Error: %v\nIf you changed your auth token before this build shipped, set YAVER_VAULT_PASSPHRASE to the previous token (or its passphrase).", err)
}

// resolveUserIDForVault returns the user_id used as the vault's access
// guard, or "" when we can't resolve it (offline mode / Convex
// unreachable). An override env (YAVER_VAULT_USER_ID) lets headless +
// CI runs skip the network round-trip.
func resolveUserIDForVault(cfg *Config) string {
	if v := strings.TrimSpace(os.Getenv("YAVER_VAULT_USER_ID")); v != "" {
		return v
	}
	if cfg == nil || cfg.ConvexSiteURL == "" || cfg.AuthToken == "" {
		return ""
	}
	info, err := ValidateTokenInfo(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil || info == nil {
		return ""
	}
	return strings.TrimSpace(info.UserID)
}

// vaultAutoResetEnabled reports whether an unreadable (dead-master-key) v2
// vault should self-heal by archiving + recreating instead of hard-erroring.
// Opt-in only (YAVER_VAULT_AUTO_RESET=1), set on cloud/headless boxes by the
// bootstrap so a customer's box never bricks its own vault; a dev machine
// where the user could still restore ~/.yaver/master.key is never auto-wiped.
func vaultAutoResetEnabled() bool {
	return envTruthy(os.Getenv("YAVER_VAULT_AUTO_RESET"))
}

// isWrongPassphraseErr matches the shared "wrong passphrase or corrupted
// vault" sentinel string used by both the v1 and v2 open paths.
func isWrongPassphraseErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "wrong passphrase")
}

// resetDeadVaultToV2 archives an unreadable vault file (its sealing key is
// gone, so the ciphertext is dead) and creates a fresh empty v2 vault sealed
// with the current master key. Nothing recoverable is lost — no key opens the
// archived bytes — and the archive is kept for forensics. Used by the headless
// auto-reset path in openVaultE.
func resetDeadVaultToV2(masterKey [32]byte, deviceID string) (*VaultStore, error) {
	path, err := VaultPath()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if _, aErr := archiveVaultFile(path, "unreadable"); aErr != nil {
			return nil, aErr
		}
	}
	// File now absent → NewVaultStoreV2 creates a fresh empty v2 vault.
	return NewVaultStoreV2(masterKey, deviceID)
}

// migrateVaultToV2 flips an already-unlocked v1 vault to the v2 master-
// key format. Returns true on success, false when keychain provisioning
// fails (caller falls back to the legacy v1 rekey-to-current-token
// path so the user is never worse off). Non-fatal — failure is logged
// but doesn't abort the open.
func migrateVaultToV2(vs *VaultStore, userID, deviceID string) bool {
	masterKey, err := EnsureMasterKey(userID, deviceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Note: v1→v2 vault migration skipped (master key unavailable: %v).\n", err)
		return false
	}
	if rkErr := vs.RekeyToMasterKey(masterKey); rkErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: v1→v2 vault rekey failed (%v) — vault still readable as v1; will retry next call.\n", rkErr)
		return false
	}
	return true
}

func readVaultPassphrase(prompt string) (string, error) {
	if pass := strings.TrimSpace(os.Getenv("YAVER_VAULT_PASSPHRASE")); pass != "" {
		return pass, nil
	}
	if prompt == "" {
		prompt = "Vault passphrase"
	}
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	pass := strings.TrimSpace(string(b))
	if pass == "" {
		return "", fmt.Errorf("passphrase cannot be empty")
	}
	return pass, nil
}

func readVaultPassphraseConfirmed(prompt string) (string, error) {
	pass, err := readVaultPassphrase(prompt)
	if err != nil {
		return "", err
	}
	if os.Getenv("YAVER_VAULT_PASSPHRASE") != "" {
		return pass, nil
	}
	again, err := readVaultPassphrase("Confirm vault passphrase")
	if err != nil {
		return "", err
	}
	if pass != again {
		return "", fmt.Errorf("passphrases did not match")
	}
	return pass, nil
}

func existingVaultPath() (string, error) {
	path, err := VaultPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no vault file at %s", path)
		}
		return "", err
	}
	return path, nil
}

func loadVaultConfigForRekey() (*Config, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("not authenticated. Run 'yaver auth' first")
	}
	return cfg, nil
}

func archiveVaultFile(path, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "archived"
	}
	stamp := time.Now().Format("20060102-150405")
	archive := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s.%s.%s", filepath.Base(path), reason, stamp))
	if err := os.Rename(path, archive); err != nil {
		return "", err
	}
	return archive, nil
}

// splitArgs pulls flag-ish args to the front so flag.Parse handles them
// regardless of where they appear relative to positional args.
func splitArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			// If this looks like "--foo=bar" we've got the value inline.
			if strings.Contains(args[i], "=") {
				continue
			}
			// Otherwise if the next arg isn't another flag, treat it as the
			// value for this one. (This heuristic is fine for Yaver — we
			// never use bare boolean --flag-then-positional patterns.)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	return append(flags, positional...)
}

func runVaultAdd(args []string) {
	fs := flag.NewFlagSet("vault add", flag.ExitOnError)
	project := fs.String("project", "", "Project scope (empty = global)")
	category := fs.String("category", "api-key", "Category (api-key, signing-key, ssh-key, git-credential, custom)")
	value := fs.String("value", "", "Secret value (prompted if not provided)")
	notes := fs.String("notes", "", "Optional notes")
	fs.Parse(splitArgs(args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver vault add <name> [--project <p>] [--category <cat>] [--value <val>]")
		os.Exit(1)
	}
	name := fs.Arg(0)

	secretValue := *value
	if secretValue == "" {
		prompt := fmt.Sprintf("Enter value for %q", name)
		if *project != "" {
			prompt = fmt.Sprintf("Enter value for %q (project %s)", name, *project)
		}
		fmt.Printf("%s: ", prompt)
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			secretValue = scanner.Text()
		}
		if secretValue == "" {
			fmt.Fprintln(os.Stderr, "Error: value cannot be empty")
			os.Exit(1)
		}
	}

	vs := openVault()
	if err := vs.Set(VaultEntry{
		Name:     name,
		Project:  *project,
		Category: *category,
		Value:    secretValue,
		Notes:    *notes,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	label := name
	if *project != "" {
		label = *project + "/" + name
	}
	fmt.Printf("Saved %s to vault.\n", label)
}

func runVaultList(args []string) {
	fs := flag.NewFlagSet("vault list", flag.ExitOnError)
	project := fs.String("project", "*", "Project scope (empty = global, * = all)")
	fs.Parse(splitArgs(args))

	vs := openVault()
	entries := vs.List(*project)

	if len(entries) == 0 {
		if *project == "*" {
			fmt.Println("Vault is empty. Add entries with 'yaver vault add <name> --project <p>'.")
		} else if *project == "" {
			fmt.Println("No global entries. Add with 'yaver vault add <name>'.")
		} else {
			fmt.Printf("No entries in project %q.\n", *project)
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tNAME\tCATEGORY\tUPDATED\tDEVICE")
	for _, e := range entries {
		t := time.UnixMilli(e.UpdatedAt)
		proj := e.Project
		if proj == "" {
			proj = "(global)"
		}
		dev := e.DeviceID
		if len(dev) > 8 {
			dev = dev[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", proj, e.Name, e.Category, t.Format("2006-01-02 15:04"), dev)
	}
	w.Flush()
}

func runVaultGet(args []string) {
	fs := flag.NewFlagSet("vault get", flag.ExitOnError)
	project := fs.String("project", "", "Project scope (empty = global)")
	fs.Parse(splitArgs(args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver vault get <name> [--project <p>]")
		os.Exit(1)
	}
	name := fs.Arg(0)

	vs := openVault()
	entry, err := vs.Get(*project, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(entry.Value)
	if fi, _ := os.Stdout.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Println()
	}
}

func runVaultDelete(args []string) {
	fs := flag.NewFlagSet("vault delete", flag.ExitOnError)
	project := fs.String("project", "", "Project scope (empty = global)")
	fs.Parse(splitArgs(args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver vault delete <name> [--project <p>]")
		os.Exit(1)
	}
	name := fs.Arg(0)

	vs := openVault()
	if err := vs.Delete(*project, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	label := name
	if *project != "" {
		label = *project + "/" + name
	}
	fmt.Printf("Deleted %s from vault (tombstone will propagate to peers).\n", label)
}

func runVaultExport() {
	vs := openVault()
	data, err := vs.ExportPlaintext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if fi, _ := os.Stdout.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintln(os.Stderr, "WARNING: Exporting vault as plaintext. Pipe to a file:")
		fmt.Fprintln(os.Stderr, "  yaver vault export > vault-backup.json")
		fmt.Fprintln(os.Stderr, "")
	}

	fmt.Println(string(data))
}

func runVaultImport(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver vault import <file.json>")
		os.Exit(1)
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Fprintf(os.Stderr, "Error: file must contain a JSON array of vault entries\n")
		os.Exit(1)
	}

	vs := openVault()
	count, err := vs.ImportPlaintext(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Imported %d entries into vault.\n", count)
}

func runVaultEnv(args []string) {
	fs := flag.NewFlagSet("vault env", flag.ExitOnError)
	project := fs.String("project", "", "Project scope (required)")
	noGlobals := fs.Bool("no-globals", false, "Exclude global entries from the output")
	fs.Parse(splitArgs(args))

	if *project == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver vault env --project <p> [--no-globals]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  eval \"$(yaver vault env --project yaver)\"")
		os.Exit(1)
	}
	vs := openVault()
	fmt.Print(vs.EnvExport(*project, !*noGlobals))
}

func runVaultExec(args []string) {
	fs := flag.NewFlagSet("vault exec", flag.ExitOnError)
	project := fs.String("project", "", "Project scope (required)")
	noGlobals := fs.Bool("no-globals", false, "Exclude global entries from env")
	// We stop parsing at the first "--" so everything after is the command.
	var cmdArgs []string
	for i, a := range args {
		if a == "--" {
			cmdArgs = args[i+1:]
			args = args[:i]
			break
		}
	}
	fs.Parse(splitArgs(args))
	if *project == "" || len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver vault exec --project <p> -- <cmd ...>")
		os.Exit(1)
	}
	vs := openVault()
	// Build env = os.Environ() + vault entries (project-scoped wins over globals,
	// vault wins over os.Environ for keys the user put in the vault).
	env := append([]string{}, os.Environ()...)
	seen := map[string]int{}
	for i, kv := range env {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			seen[kv[:eq]] = i
		}
	}
	setEnv := func(k, v string) {
		kv := k + "=" + v
		if idx, ok := seen[k]; ok {
			env[idx] = kv
		} else {
			seen[k] = len(env)
			env = append(env, kv)
		}
	}
	// Include globals first so project entries can override.
	include := []string{""}
	if *noGlobals {
		include = nil
	}
	include = append(include, *project)
	for _, proj := range include {
		for _, e := range vs.List(proj) {
			// List() doesn't include Value; fetch each.
			entry, err := vs.Get(proj, e.Name)
			if err != nil {
				continue
			}
			setEnv(entry.Name, entry.Value)
		}
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Forward common signals so subprocess exits cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for s := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runVaultCheckPassphrase(args []string) {
	fs := flag.NewFlagSet("vault check-passphrase", flag.ExitOnError)
	passFlag := fs.String("passphrase", "", "Passphrase to test (prefer prompt/env; this can leak via shell history)")
	fs.Parse(splitArgs(args))
	if _, err := existingVaultPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pass := strings.TrimSpace(*passFlag)
	if pass == "" {
		var err error
		pass, err = readVaultPassphrase("Vault passphrase to test")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading passphrase: %v\n", err)
			os.Exit(1)
		}
	}
	vs, err := NewVaultStore(pass)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid vault passphrase.")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	entries := vs.List("*")
	projects := vs.ListProjects()
	if len(projects) == 0 {
		fmt.Printf("Valid vault passphrase. Vault opens; %d entries, no project-scoped entries.\n", len(entries))
		return
	}
	fmt.Printf("Valid vault passphrase. Vault opens; %d entries across projects: %s\n", len(entries), strings.Join(projects, ", "))
}

func runVaultUnlock(args []string) {
	fs := flag.NewFlagSet("vault unlock", flag.ExitOnError)
	passFlag := fs.String("passphrase", "", "Passphrase to unlock with (prefer prompt/env; this can leak via shell history)")
	fs.Parse(splitArgs(args))
	if _, err := existingVaultPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cfg, err := loadVaultConfigForRekey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pass := strings.TrimSpace(*passFlag)
	if pass == "" {
		pass, err = readVaultPassphrase("Vault passphrase to unlock")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading passphrase: %v\n", err)
			os.Exit(1)
		}
	}
	vs, err := NewVaultStoreWithDevice(pass, cfg.DeviceID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid vault passphrase; vault was not changed.")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := vs.RekeyTo(DerivePassphraseFromToken(cfg.AuthToken)); err != nil {
		fmt.Fprintf(os.Stderr, "Error re-encrypting vault: %v\n", err)
		os.Exit(1)
	}
	cfg.PreviousAuthToken = ""
	cfg.PreviousAuthTokens = nil
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: vault rekeyed, but could not clear previous-token state: %v\n", err)
	}
	fmt.Println("Vault unlocked and re-encrypted under the current Yaver auth token.")
}

func runVaultLock(args []string) {
	fs := flag.NewFlagSet("vault lock", flag.ExitOnError)
	passFlag := fs.String("passphrase", "", "New manual vault passphrase (prefer prompt; this can leak via shell history)")
	fs.Parse(splitArgs(args))
	if _, err := existingVaultPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	vs := openVault()
	pass := strings.TrimSpace(*passFlag)
	var err error
	if pass == "" {
		pass, err = readVaultPassphraseConfirmed("New manual vault passphrase")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading passphrase: %v\n", err)
			os.Exit(1)
		}
	}
	if err := vs.RekeyTo(pass); err != nil {
		fmt.Fprintf(os.Stderr, "Error locking vault: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Vault locked under the manual passphrase.")
	fmt.Println("Use 'yaver vault unlock' to re-encrypt it under your current Yaver auth token, or set YAVER_VAULT_PASSPHRASE for individual commands.")
}

func runVaultReset(args []string) {
	fs := flag.NewFlagSet("vault reset", flag.ExitOnError)
	yes := fs.Bool("yes", false, "Do not prompt for confirmation")
	manual := fs.Bool("manual-passphrase", false, "Create the fresh vault under a prompted manual passphrase instead of the current auth token")
	fs.Parse(splitArgs(args))

	path, err := existingVaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !*yes {
		fmt.Fprintf(os.Stderr, "This will archive %s and create a fresh empty vault. Type RESET to continue: ", path)
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || scanner.Text() != "RESET" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			os.Exit(1)
		}
	}

	var pass, deviceID string
	if *manual {
		pass, err = readVaultPassphraseConfirmed("New manual vault passphrase")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading passphrase: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg, err := loadVaultConfigForRekey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		pass = DerivePassphraseFromToken(cfg.AuthToken)
		deviceID = cfg.DeviceID
	}

	archive, err := archiveVaultFile(path, "reset-bak")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error archiving vault: %v\n", err)
		os.Exit(1)
	}
	if _, err := NewVaultStoreWithDevice(pass, deviceID); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating fresh vault: %v\n", err)
		fmt.Fprintf(os.Stderr, "Archived vault remains at %s\n", archive)
		os.Exit(1)
	}
	fmt.Printf("Created a fresh empty vault. Archived previous vault at %s\n", archive)
}

func runVaultProjects() {
	vs := openVault()
	projects := vs.ListProjects()
	if len(projects) == 0 {
		fmt.Println("No project-scoped entries yet. Use 'yaver vault add <name> --project <p>' to create one.")
		return
	}
	for _, p := range projects {
		count := len(vs.List(p))
		fmt.Printf("%-24s %d entries\n", p, count)
	}
}

// runVaultSync performs a P2P sync against a specific peer device.
// Without --from, iterates through all the user's known devices.
func runVaultSync(args []string) {
	fs := flag.NewFlagSet("vault sync", flag.ExitOnError)
	peer := fs.String("from", "", "Specific peer deviceId (default: every online peer)")
	fs.Parse(splitArgs(args))

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run 'yaver auth' first.")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var peers []string
	if *peer != "" {
		peers = []string{*peer}
	} else {
		convex := cfg.ConvexSiteURL
		if convex == "" {
			convex = defaultConvexSiteURL
		}
		devices, err := primaryListDevices(ctx, cfg.AuthToken, convex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing devices: %v\n", err)
			os.Exit(1)
		}
		for _, d := range devices {
			if d.DeviceID != "" && d.DeviceID != cfg.DeviceID {
				peers = append(peers, d.DeviceID)
			}
		}
		if len(peers) == 0 {
			fmt.Println("No peer devices found — sync needs at least two devices registered under the same account.")
			return
		}
	}

	vs := openVault()
	var totals VaultSyncReport
	totals.Peer = fmt.Sprintf("%d peers", len(peers))
	for _, p := range peers {
		rpt, err := vaultSyncWithPeer(ctx, vs, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", p, err)
			continue
		}
		fmt.Printf("  %s: pulled %d (superseded-local %d), pushed %d (rejected %d), %s\n",
			p, rpt.Pulled, rpt.SupersededLocal, rpt.Pushed, rpt.Rejected,
			time.Duration(rpt.DurationMs*int64(time.Millisecond)).Round(time.Millisecond))
		totals.Pulled += rpt.Pulled
		totals.SupersededLocal += rpt.SupersededLocal
		totals.Pushed += rpt.Pushed
		totals.Rejected += rpt.Rejected
	}
	fmt.Printf("Sync complete: pulled %d, superseded-local %d, pushed %d, rejected %d across %d peers.\n",
		totals.Pulled, totals.SupersededLocal, totals.Pushed, totals.Rejected, len(peers))
	if totals.SupersededLocal > 0 || totals.Rejected > 0 {
		fmt.Fprintln(os.Stderr,
			"  Note: a non-zero 'superseded-local' or 'rejected' means two devices wrote\n"+
				"  the same (project, name) around the same time and the loser was silently\n"+
				"  overwritten by last-writer-wins. If the lost value mattered, reconstruct\n"+
				"  it from `yaver vault list` history + `yaver vault get`.")
	}
}

// VaultSyncReport is the structured per-peer outcome of one sync.
// Fields:
//
//	Peer             — peer deviceID.
//	Pulled           — how many of the peer's entries we accepted.
//	SupersededLocal  — within Pulled, how many were newer than a
//	                   value we already had (i.e. our old value was
//	                   silently replaced).
//	Pushed           — how many of our entries the peer accepted.
//	Rejected         — how many entries we sent that the peer
//	                   rejected (peer was already as-new-or-newer).
//	DurationMs       — wall time for the full handshake.
type VaultSyncReport struct {
	Peer            string `json:"peer"`
	Pulled          int    `json:"pulled"`
	SupersededLocal int    `json:"superseded_local"`
	Pushed          int    `json:"pushed"`
	Rejected        int    `json:"rejected"`
	DurationMs      int64  `json:"duration_ms"`
}

// vaultSyncWithPeer exchanges digests with the peer agent and applies
// any newer revisions in each direction. Returns a structured report
// so the caller can surface conflicts (SupersededLocal / Rejected).
func vaultSyncWithPeer(ctx context.Context, vs *VaultStore, peerDeviceID string) (VaultSyncReport, error) {
	report := VaultSyncReport{Peer: peerDeviceID}
	start := time.Now()

	localDigest := vs.Digest()
	// Pre-index local UpdatedAt per (project, name) so we can tell
	// "pulled something we had nothing for" from "pulled something
	// that overrode an older local value" — the latter is the
	// potentially-losable case worth surfacing.
	localHave := make(map[string]int64, len(localDigest))
	for _, d := range localDigest {
		localHave[d.Project+"\x00"+d.Name] = d.UpdatedAt
	}

	// Pull.
	req := map[string]interface{}{"digest": localDigest}
	pullStatus, pullBody, err := proxyToDevice(ctx, "vault_sync", peerDeviceID, "POST", "/vault/sync", mustJSON(req))
	if err != nil {
		return report, fmt.Errorf("pull: %w", err)
	}
	if pullStatus >= 400 {
		return report, fmt.Errorf("pull: HTTP %d: %s", pullStatus, strings.TrimSpace(string(pullBody)))
	}
	var pullResp struct {
		Entries []VaultEntry `json:"entries"`
	}
	if err := json.Unmarshal(pullBody, &pullResp); err != nil {
		return report, fmt.Errorf("pull: decode: %w", err)
	}
	for _, e := range pullResp.Entries {
		prev := localHave[e.Project+"\x00"+e.Name]
		ok, err := vs.Upsert(e)
		if err != nil || !ok {
			continue
		}
		report.Pulled++
		if prev > 0 && prev < e.UpdatedAt {
			report.SupersededLocal++
		}
	}

	// Push: ask for peer's digest, send our newer entries.
	digStatus, digBody, err := proxyToDevice(ctx, "vault_digest", peerDeviceID, "GET", "/vault/digest", nil)
	if err != nil {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, fmt.Errorf("digest: %w", err)
	}
	if digStatus >= 400 {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, fmt.Errorf("digest: HTTP %d: %s", digStatus, strings.TrimSpace(string(digBody)))
	}
	var digResp struct {
		Entries []VaultDigestEntry `json:"entries"`
	}
	if err := json.Unmarshal(digBody, &digResp); err != nil {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, fmt.Errorf("digest: decode: %w", err)
	}
	ourNewer := vs.EntriesNewerThan(digResp.Entries)
	if len(ourNewer) == 0 {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, nil
	}
	pushReq := map[string]interface{}{"entries": ourNewer}
	pushStatus, pushBody, err := proxyToDevice(ctx, "vault_push", peerDeviceID, "POST", "/vault/push", mustJSON(pushReq))
	if err != nil {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, fmt.Errorf("push: %w", err)
	}
	if pushStatus >= 400 {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, fmt.Errorf("push: HTTP %d: %s", pushStatus, strings.TrimSpace(string(pushBody)))
	}
	var pushResp struct {
		Accepted int `json:"accepted"`
		Rejected int `json:"rejected"`
	}
	if err := json.Unmarshal(pushBody, &pushResp); err != nil {
		report.DurationMs = time.Since(start).Milliseconds()
		return report, fmt.Errorf("push: decode: %w", err)
	}
	report.Pushed = pushResp.Accepted
	report.Rejected = pushResp.Rejected
	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
