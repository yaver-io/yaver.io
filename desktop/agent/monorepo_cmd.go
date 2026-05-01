package main

// CLI: `yaver monorepo [dir]` — print the framework composition of a directory.
// Wraps DetectMonorepo so terminal users (and `yaver code`) get the same
// classification mobile / web / MCP get.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

func runMonorepo(args []string) {
	// Subcommand dispatch — `yaver monorepo start` runs the
	// project-creation wizard (mirrors the mobile sandbox flow at
	// mobile/app/phone-projects.tsx). Bare `yaver monorepo` keeps
	// the existing detection / framework-listing behaviour for
	// backwards compatibility.
	if len(args) >= 1 {
		switch args[0] {
		case "start":
			runMonorepoStart(args[1:])
			return
		case "help", "--help", "-h":
			fmt.Println("Usage:")
			fmt.Println("  yaver monorepo                    Detect & list projects in cwd")
			fmt.Println("  yaver monorepo [--json] [dir]     Same, but for a specific dir")
			fmt.Println("  yaver monorepo start              Interactive wizard — create a new monorepo")
			return
		}
	}

	fs := flag.NewFlagSet("monorepo", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Emit raw JSON instead of a table")
	maxDepth := fs.Int("max-depth", 0, "Maximum recursion depth (default 6)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: yaver monorepo [--json] [--max-depth=N] [dir]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	dir := fs.Arg(0)
	if dir == "" {
		cwd, _ := os.Getwd()
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs %s: %v\n", dir, err)
		os.Exit(1)
	}

	mr, err := DetectMonorepo(abs, DetectOpts{MaxDepth: *maxDepth})
	if err != nil {
		fmt.Fprintf(os.Stderr, "monorepo detect: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(mr)
		return
	}

	// Pretty table — same shape as `yaver workspace status`.
	if len(mr.Projects) == 0 {
		fmt.Printf("No projects detected in %s\n", abs)
		return
	}
	header := "monorepo"
	if !mr.IsMonorepo {
		header = "single project"
	}
	fmt.Printf("%s — %s", abs, header)
	if mr.GitBranch != "" {
		fmt.Printf("  (git: %s)", mr.GitBranch)
	}
	if mr.HasManifest {
		fmt.Print("  [yaver.workspace.yaml]")
	}
	fmt.Println()
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FRAMEWORK\tNAME\tPATH\tTAGS")
	// Stable order — by path.
	sort.Slice(mr.Projects, func(i, j int) bool {
		return mr.Projects[i].RelPath < mr.Projects[j].RelPath
	})
	for _, p := range mr.Projects {
		tags := strings.Join(p.Tags, ",")
		if len(tags) > 32 {
			tags = tags[:29] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Framework, p.Name, p.RelPath, tags)
	}
	w.Flush()

	if len(mr.Frameworks) > 0 {
		fmt.Println()
		fmt.Printf("Frameworks present: %s\n", strings.Join(mr.Frameworks, ", "))
		fmt.Println()
		// Suggest the right native_build aliases for the frameworks found.
		suggestions := []string{}
		for _, fw := range mr.Frameworks {
			switch fw {
			case "iosNative":
				suggestions = append(suggestions, "yaver iosNative <project-dir>")
			case "androidNative":
				suggestions = append(suggestions, "yaver androidNative <project-dir>")
			case "flutter":
				suggestions = append(suggestions, "yaver flutter <project-dir>")
			}
		}
		if len(suggestions) > 0 {
			fmt.Println("Try:")
			for _, s := range suggestions {
				fmt.Println("  " + s)
			}
		}
	}
}
