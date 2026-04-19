package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// `yaver phone` — CLI wrapper around the phone-backend HTTP surface. Lets the
// developer export / import / push phone projects without opening the mobile
// app. Also the primary path for dogfooding the YC-demo flow:
//
//   yaver phone push my-todos --to https://relay.yaver.io/d/devABC --as-slug backup
//   yaver phone push my-todos --to https://cloud.yaver.io --conflict rename
//
// Anything the mobile app does through `pushPhoneProject()` should also be
// reachable here.

func runPhone(args []string) {
	if len(args) == 0 {
		printPhoneUsage()
		return
	}
	switch args[0] {
	case "list", "ls":
		runPhoneList()
	case "export":
		runPhoneExport(args[1:])
	case "import":
		runPhoneImport(args[1:])
	case "push":
		runPhonePush(args[1:])
	case "help", "--help", "-h":
		printPhoneUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown phone subcommand: %s\n", args[0])
		printPhoneUsage()
		os.Exit(1)
	}
}

func printPhoneUsage() {
	fmt.Println(`Usage: yaver phone <command> [args]

Commands:
  list                             List local phone projects
  export <slug> [--out <path>]     Export a project as a .tgz
  import <path> [--slug <name>]    Import a .tgz into this agent
  push <slug> --to <url>           Push a local project to a remote agent

Examples:
  yaver phone push my-todos --to https://relay.yaver.io/d/devABC
  yaver phone push my-todos --to https://cloud.yaver.io --include-data
  yaver phone push my-todos --to https://cloud.yaver.io --conflict rename
  yaver phone export my-todos --out my-todos.tgz --include-data
  yaver phone import my-todos.tgz --slug my-todos-backup`)
}

func runPhoneList() {
	projs, err := ListPhoneProjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
		os.Exit(1)
	}
	if len(projs) == 0 {
		fmt.Println("No phone projects. Create one from the mobile app or the web dashboard.")
		return
	}
	fmt.Printf("%-20s  %-24s  %s\n", "SLUG", "NAME", "UPDATED")
	for _, p := range projs {
		fmt.Printf("%-20s  %-24s  %s\n", p.Slug, truncPhone(p.Name, 24), p.UpdatedAt)
	}
}

func runPhoneExport(args []string) {
	fs := flag.NewFlagSet("phone export", flag.ExitOnError)
	out := fs.String("out", "", "output path (default: <slug>.tgz)")
	includeData := fs.Bool("include-data", false, "bundle the live SQLite file so runtime rows survive import")
	containerize := fs.Bool("containerize", false, "include Docker/compose scaffold for Yaver-lite backend deploys")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver phone export <slug> [--out <path>] [--include-data] [--containerize]")
		os.Exit(1)
	}
	slug := fs.Arg(0)
	data, err := ExportPhoneProjectWithOptions(slug, PhoneExportOptions{
		IncludeData:  *includeData,
		Containerize: *containerize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "export failed: %v\n", err)
		os.Exit(1)
	}
	path := *out
	if path == "" {
		path = slug + ".tgz"
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("Exported %s (%d bytes)\n", path, len(data))
}

func runPhoneImport(args []string) {
	fs := flag.NewFlagSet("phone import", flag.ExitOnError)
	slug := fs.String("slug", "", "override slug")
	conflict := fs.String("conflict", "reject", "reject|rename|overwrite")
	skipSeed := fs.Bool("skip-seed", false, "do not apply seed rows")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver phone import <path> [--slug NAME] [--conflict reject|rename|overwrite]")
		os.Exit(1)
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	p, err := ImportPhoneProject(data, PhoneImportOptions{
		SlugOverride: *slug,
		OnConflict:   *conflict,
		SkipSeed:     *skipSeed,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "import failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Imported as %s\n", p.Slug)
}

// runPhonePush exports <slug> locally then POSTs to <target>/phone/projects/receive.
// Target can be any reachable yaver serve agent — user's Mac/Pi/Linux/VPS,
// or Yaver's Hetzner cloud. Uses the caller's saved auth token.
func runPhonePush(args []string) {
	fs := flag.NewFlagSet("phone push", flag.ExitOnError)
	to := fs.String("to", "", "target base URL (required, e.g. https://relay.yaver.io/d/abc or https://cloud.yaver.io)")
	asSlug := fs.String("as-slug", "", "slug to use on the target (default: same as source)")
	conflict := fs.String("conflict", "reject", "reject|rename|overwrite")
	skipSeed := fs.Bool("skip-seed", false, "push schema+auth but no seed rows")
	includeData := fs.Bool("include-data", false, "bundle local.db so runtime rows survive promotion")
	containerize := fs.Bool("containerize", false, "include Docker/compose scaffold on the target project")
	tokenFlag := fs.String("token", "", "override auth token (also: YAVER_AUTH_TOKEN env). Useful for pushing to a managed cloud tenant whose CLOUD_OWNER_TOKEN differs from your local OAuth session")
	_ = fs.Parse(args)
	if fs.NArg() < 1 || *to == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver phone push <slug> --to <base-url> [--token TOKEN] [--as-slug NAME] [--conflict reject|rename|overwrite] [--include-data] [--containerize]")
		os.Exit(1)
	}
	slug := fs.Arg(0)

	// Export locally — bypass HTTP to avoid needing a running local server.
	bundle, err := ExportPhoneProjectWithOptions(slug, PhoneExportOptions{
		IncludeData:  *includeData,
		Containerize: *containerize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		os.Exit(1)
	}

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("YAVER_AUTH_TOKEN"))
	}
	if token == "" {
		cfg, err := LoadConfig()
		if err != nil || strings.TrimSpace(cfg.AuthToken) == "" {
			fmt.Fprintf(os.Stderr, "no auth token. pass --token <TOKEN>, set YAVER_AUTH_TOKEN, or run 'yaver auth'.\n")
			os.Exit(1)
		}
		token = cfg.AuthToken
	}

	base := strings.TrimRight(*to, "/")
	fmt.Printf("→ %s/phone/projects/receive (%d bytes)\n", base, len(bundle))
	t0 := time.Now()
	result, err := pushPhoneBundle(base, token, bundle, *asSlug, *conflict, *skipSeed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "push failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pushed to %s in %s\n", base, time.Since(t0).Round(time.Millisecond))
	fmt.Printf("  slug:   %s\n", result.Slug)
	fmt.Printf("  browse: %s%s\n", base, result.BrowseUrl)
}

type phonePushResult struct {
	Slug      string `json:"slug"`
	LocalUrl  string `json:"localUrl"`
	BrowseUrl string `json:"browseUrl"`
}

func pushPhoneBundle(base, token string, bundle []byte, slug, conflict string, skipSeed bool) (*phonePushResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("bundle", "project.tgz")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(bundle); err != nil {
		return nil, err
	}
	if slug != "" {
		_ = mw.WriteField("slug", slug)
	}
	if conflict != "" {
		_ = mw.WriteField("onConflict", conflict)
	}
	if skipSeed {
		_ = mw.WriteField("skipSeed", "true")
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, base+"/phone/projects/receive", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out phonePushResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(body))
	}
	return &out, nil
}

func truncPhone(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
