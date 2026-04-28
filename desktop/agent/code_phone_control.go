package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// code_phone_control.go is the `yaver code phone *` command tree.
// It is the non-interactive twin of the interactive `/phone` slash
// palette in code_phone.go: same verbs, same target aliases, just
// usable from a script or CI.
//
// Verbs in Slice 0:
//   yaver code phone status [--workdir <path>]
//   yaver code phone push <slug> --to <target> [--token TOKEN]
//                                              [--as-slug NAME]
//                                              [--conflict reject|rename|overwrite]
//                                              [--include-data]
//                                              [--containerize]
//                                              [--skip-seed]
//
// Target aliases:
//   dev-hw       — local agent (http://127.0.0.1:18080)
//   yaver-cloud  — https://cloud.yaver.io (override via $YAVER_CLOUD_URL)
//   <url>        — anything starting with http:// or https://
//
// Pull / token verbs land in follow-up commits; this file is the
// scaffold those will hang from.

// runCodePhoneControl is registered from runCodeControl in
// code_control.go (`case "phone": return true, runCodePhoneControl(args[1:])`).
// Returning a nil error means the command handled itself and printed
// any output. Returning a non-nil error bubbles up to the caller for
// a one-line stderr message + non-zero exit.
func runCodePhoneControl(args []string) error {
	if len(args) == 0 {
		printCodePhoneUsage()
		return nil
	}
	switch args[0] {
	case "status":
		return runCodePhoneStatus(args[1:])
	case "push":
		return runCodePhonePush(args[1:])
	case "help", "-h", "--help":
		printCodePhoneUsage()
		return nil
	default:
		return fmt.Errorf("unknown phone subcommand %q (try `yaver code phone help`)", args[0])
	}
}

func printCodePhoneUsage() {
	fmt.Println("Usage:")
	fmt.Println("  yaver code phone status [--workdir <path>]")
	fmt.Println("  yaver code phone push <slug> --to <target> [flags]")
	fmt.Println()
	fmt.Println("Targets:")
	fmt.Println("  dev-hw         local agent (http://127.0.0.1:18080)")
	fmt.Println("  yaver-cloud    https://cloud.yaver.io ($YAVER_CLOUD_URL to override)")
	fmt.Println("  <url>          any reachable agent base URL")
	fmt.Println()
	fmt.Println("Push flags:")
	fmt.Println("  --as-slug NAME            slug to use on the target (default: same)")
	fmt.Println("  --conflict reject|rename|overwrite  (default: reject)")
	fmt.Println("  --include-data            bundle local.db with the project")
	fmt.Println("  --containerize            include Docker scaffold")
	fmt.Println("  --skip-seed               push schema+auth but no seed rows")
	fmt.Println("  --token TOKEN             override auth token (also: $YAVER_AUTH_TOKEN)")
}

func runCodePhoneStatus(args []string) error {
	fs := flag.NewFlagSet("code phone status", flag.ContinueOnError)
	workDir := fs.String("workdir", "", "directory to inspect (default: cwd)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := renderPhoneStatus(context.Background(), *workDir)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func runCodePhonePush(args []string) error {
	fs := flag.NewFlagSet("code phone push", flag.ContinueOnError)
	to := fs.String("to", "", "target alias or base URL (required: dev-hw | yaver-cloud | https://...)")
	asSlug := fs.String("as-slug", "", "slug to use on the target (default: same as source)")
	conflict := fs.String("conflict", "reject", "reject|rename|overwrite")
	skipSeed := fs.Bool("skip-seed", false, "push schema+auth but no seed rows")
	includeData := fs.Bool("include-data", false, "bundle local.db so runtime rows survive promotion")
	containerize := fs.Bool("containerize", false, "include Docker/compose scaffold on the target project")
	tokenFlag := fs.String("token", "", "override auth token (also: YAVER_AUTH_TOKEN env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printCodePhoneUsage()
		return fmt.Errorf("phone push: <slug> required")
	}
	if strings.TrimSpace(*to) == "" {
		printCodePhoneUsage()
		return fmt.Errorf("phone push: --to <target> required")
	}
	slug := fs.Arg(0)

	target, err := resolvePushTarget(*to)
	if err != nil {
		return err
	}

	// Build the bundle from the agent's local storage. Same code
	// path the existing `yaver phone push` uses — the "code" surface
	// is purely an ergonomic wrapper.
	bundle, err := ExportPhoneProjectWithOptions(slug, PhoneExportOptions{
		IncludeData:  *includeData,
		Containerize: *containerize,
	})
	if err != nil {
		return fmt.Errorf("export %q: %w", slug, err)
	}

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("YAVER_AUTH_TOKEN"))
	}
	if token == "" {
		cfg, err := LoadConfig()
		if err != nil || strings.TrimSpace(cfg.AuthToken) == "" {
			return fmt.Errorf("no auth token. pass --token <TOKEN>, set YAVER_AUTH_TOKEN, or run `yaver auth`")
		}
		token = cfg.AuthToken
	}

	fmt.Printf("→ %s/phone/projects/receive (%d bytes, target=%s)\n", target.baseURL, len(bundle), target.label)
	t0 := time.Now()
	result, err := pushPhoneBundle(target.baseURL, token, bundle, *asSlug, *conflict, *skipSeed)
	if err != nil {
		return fmt.Errorf("push to %s: %w", target.baseURL, err)
	}
	fmt.Printf("Pushed to %s in %s\n", target.baseURL, time.Since(t0).Round(time.Millisecond))
	fmt.Printf("  slug:   %s\n", result.Slug)
	if strings.TrimSpace(result.BrowseUrl) != "" {
		fmt.Printf("  browse: %s%s\n", target.baseURL, result.BrowseUrl)
	}
	return nil
}

// pushTarget is the resolved target the bundle ships to.
type pushTarget struct {
	baseURL string
	label   string // human-readable for the progress line
}

// resolvePushTarget converts a target alias or literal URL into the
// canonical baseURL the bundle should ship to.
//
// Aliases:
//
//	dev-hw       → http://127.0.0.1:18080
//	yaver-cloud  → $YAVER_CLOUD_URL (default: https://cloud.yaver.io)
//
// Anything starting with "http://" or "https://" is treated as a
// literal URL and trimmed of trailing slashes. An unknown alias is
// rejected up front so a typo doesn't push to the wrong place.
func resolvePushTarget(raw string) (pushTarget, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return pushTarget{}, fmt.Errorf("--to required")
	}
	switch {
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		return pushTarget{baseURL: strings.TrimRight(s, "/"), label: "explicit-url"}, nil
	case strings.EqualFold(s, "dev-hw"), strings.EqualFold(s, "devhw"), strings.EqualFold(s, "local"):
		return pushTarget{baseURL: "http://127.0.0.1:18080", label: "dev-hw"}, nil
	case strings.EqualFold(s, "yaver-cloud"), strings.EqualFold(s, "cloud"):
		base := strings.TrimSpace(os.Getenv("YAVER_CLOUD_URL"))
		if base == "" {
			base = "https://cloud.yaver.io"
		}
		return pushTarget{baseURL: strings.TrimRight(base, "/"), label: "yaver-cloud"}, nil
	default:
		return pushTarget{}, fmt.Errorf("unknown target %q (use dev-hw | yaver-cloud | https://...)", raw)
	}
}
