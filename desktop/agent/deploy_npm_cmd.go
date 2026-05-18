package main

// deploy_npm_cmd.go — `yaver deploy npm` ships *just* the CLI npm release:
// bump cli/package.json (+ versions.json + lock + manifest), commit, tag
// `cli/vX.Y.Z`, and push. release-cli.yml on CI builds the cross-platform
// binaries and runs `npm publish`. This is the npm-only slice of
// `yaver deploy all` for when the mobile/Convex/Cloudflare stages aren't
// needed (e.g. a CLI-only fix like the build-status output changes).

import (
	"flag"
	"fmt"
	"os"
)

func runDeployNpmCmd(args []string) {
	fs := flag.NewFlagSet("deploy npm", flag.ExitOnError)
	bump := fs.String("bump", "patch", "Version bump: patch|minor|major")
	dryRun := fs.Bool("dry-run", false, "Print what would happen; no commit/tag/push")
	fs.Parse(args)

	switch *bump {
	case "patch", "minor", "major":
	default:
		fmt.Fprintf(os.Stderr, "deploy npm: --bump must be patch|minor|major (got %q)\n", *bump)
		os.Exit(2)
	}

	repoRoot, err := findYaverRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy npm: %v\n", err)
		os.Exit(2)
	}

	fmt.Println("yaver deploy npm")
	fmt.Println("repo:", repoRoot)
	if *dryRun {
		fmt.Println("mode: dry-run (no commit/tag/push)")
	}
	fmt.Println()

	ctx := &deployAllCtx{dryRun: *dryRun, prefix: "[npm]"}
	if err := runNpmCliRelease(repoRoot, *bump, *dryRun, ctx); err != nil {
		fmt.Fprintf(os.Stderr, "\ndeploy npm: %v\n", err)
		os.Exit(1)
	}

	if *dryRun {
		fmt.Println("\n[npm] dry-run complete — re-run without --dry-run to publish.")
		return
	}
	fmt.Println("\n[npm] done — CI is publishing to npm. Track it:")
	fmt.Println("  gh run list -w release-cli.yml -L 1")
}
