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
//   projects — list distinct projects
//   sync     — pull + push sync with a peer device (owner-auth, P2P)

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
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
  yaver vault projects                     List distinct projects
  yaver vault sync [--from <deviceId>]     Pull + push with peer (P2P)

Categories: api-key, signing-key, ssh-key, git-credential, custom

The vault is encrypted at rest (NaCl secretbox + Argon2id). Unlock uses
your auth token by default; override with:
  YAVER_VAULT_PASSPHRASE=<pass> yaver vault ...
`)
}

// openVault loads the vault using auth token or custom passphrase.
func openVault() *VaultStore {
	passphrase := os.Getenv("YAVER_VAULT_PASSPHRASE")
	if passphrase == "" {
		cfg, err := LoadConfig()
		if err != nil || cfg.AuthToken == "" {
			fmt.Fprintf(os.Stderr, "Not authenticated. Run 'yaver auth' first.\n")
			os.Exit(1)
		}
		passphrase = DerivePassphraseFromToken(cfg.AuthToken)
	}

	cfg, _ := LoadConfig()
	deviceID := ""
	if cfg != nil {
		deviceID = cfg.DeviceID
	}
	vs, err := NewVaultStoreWithDevice(passphrase, deviceID)
	if err != nil {
		if strings.Contains(err.Error(), "wrong passphrase") {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintf(os.Stderr, "If you changed your auth token, set YAVER_VAULT_PASSPHRASE to your previous passphrase.\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error opening vault: %v\n", err)
		os.Exit(1)
	}
	return vs
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
