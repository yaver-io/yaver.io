package main

// `yaver ci add <target>` — scaffolds a GitHub Actions workflow into a
// third-party project so the developer gets Yaver CI with the same
// single install they use locally:
//
//     npm install -g yaver-cli
//     yaver ci add hermes
//
// The generated workflow itself installs `yaver-cli` on the runner, so
// CI and the laptop share one command surface. No separate GitHub
// Action to maintain.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ciTarget struct {
	name        string
	filename    string
	description string
	// requires returns an explanation if the project directory does not
	// match the target (e.g. asking for `hermes` in a Flutter project).
	requires func(dir string) string
	// workflow returns the YAML body. Kept as a function so we can later
	// template in project-specific values without refactoring the map.
	workflow func(dir string) string
	// nextSteps are printed after the file is written so the developer
	// knows what secrets or follow-up actions are needed.
	nextSteps []string
}

var ciTargets = []ciTarget{
	{
		name:        "hermes",
		filename:    "yaver-hermes.yml",
		description: "Validate Hermes BC + native-module compatibility on every PR (push-to-device gate)",
		requires:    requireReactNativeProject,
		workflow:    hermesWorkflow,
		nextSteps: []string{
			"Commit .github/workflows/yaver-hermes.yml",
			"Open a PR — the workflow runs `yaver push doctor --strict` on the project",
			"Failing runs mean the project drifted from the Yaver SDK manifest; update deps or handle missing modules with NativeModules.YaverInfo checks",
		},
	},
	{
		name:        "feedback",
		filename:    "yaver-feedback.yml",
		description: "Typecheck / lint for the Feedback SDK integration",
		requires:    requireFeedbackProject,
		workflow:    feedbackWorkflow,
		nextSteps: []string{
			"Commit .github/workflows/yaver-feedback.yml",
			"The workflow enforces `yaver-feedback-*` is installed, the entrypoint wires `initExpo()` / `YaverFeedback.init`, and the project still typechecks",
		},
	},
	{
		name:        "push-to-device",
		filename:    "yaver-push-to-device.yml",
		description: "On tag push, bundle JS + compile Hermes bytecode and publish as a release artifact",
		requires:    requireReactNativeProject,
		workflow:    pushToDeviceWorkflow,
		nextSteps: []string{
			"Commit .github/workflows/yaver-push-to-device.yml",
			"Tag a release (e.g. `git tag v0.1.0 && git push --tags`) to produce a Hermes-compiled bundle as a release asset",
			"Clients can later `yaver push --bundle <url>` the artifact to devices",
		},
	},
}

func runCI(args []string) {
	if len(args) == 0 {
		printCIUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "add":
		runCIAdd(args[1:])
	case "list", "ls":
		printCIList()
	default:
		fmt.Fprintf(os.Stderr, "Unknown ci subcommand: %s\n\n", args[0])
		printCIUsage()
		os.Exit(1)
	}
}

func printCIUsage() {
	fmt.Print(`Usage:
  yaver ci add <target> [--dir <path>] [--force]
  yaver ci list

Targets:
`)
	printCIList()
	fmt.Print(`
The generated workflow installs yaver-cli via npm on the runner, so
your project inherits the same umbrella install you use locally:

  npm install -g yaver-cli
  yaver ci add hermes      # drops .github/workflows/yaver-hermes.yml
  git add .github && git commit && git push

No separate GitHub Action to maintain — the runner uses the same
yaver binary the developer has on their laptop.
`)
}

func printCIList() {
	for _, t := range ciTargets {
		fmt.Printf("  %-16s %s\n", t.name, t.description)
	}
}

func runCIAdd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver ci add <target> [--dir <path>] [--force]")
		os.Exit(1)
	}
	// Extract the positional target first so flags can follow.
	targetName := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	fs := flag.NewFlagSet("ci add", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	force := fs.Bool("force", false, "Overwrite an existing workflow file")
	fs.Parse(rest)

	var target *ciTarget
	for i := range ciTargets {
		if ciTargets[i].name == targetName {
			target = &ciTargets[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "Unknown ci target %q.\n\n", targetName)
		printCIUsage()
		os.Exit(1)
	}

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if target.requires != nil {
		if reason := target.requires(absDir); reason != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", reason)
			os.Exit(1)
		}
	}

	workflowsDir := filepath.Join(absDir, ".github", "workflows")
	if err := os.MkdirAll(workflowsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", workflowsDir, err)
		os.Exit(1)
	}

	outPath := filepath.Join(workflowsDir, target.filename)
	if _, err := os.Stat(outPath); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "Error: %s already exists. Re-run with --force to overwrite.\n", outPath)
		os.Exit(1)
	}

	body := target.workflow(absDir)
	if err := os.WriteFile(outPath, []byte(body), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outPath, err)
		os.Exit(1)
	}

	rel, _ := filepath.Rel(absDir, outPath)
	fmt.Printf("Project:  %s\n", absDir)
	fmt.Printf("Target:   %s\n", target.name)
	fmt.Printf("Wrote:    %s\n\n", rel)
	fmt.Println("Next steps:")
	for _, step := range target.nextSteps {
		fmt.Printf("  - %s\n", step)
	}
}

func requireReactNativeProject(dir string) string {
	if !hasFile(dir, "package.json") {
		return fmt.Sprintf("no package.json in %s (this target scaffolds CI for a React Native / Expo project)", dir)
	}
	pkg, err := readSmallFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return fmt.Sprintf("cannot read package.json: %v", err)
	}
	text := string(pkg)
	if !strings.Contains(text, "\"react-native\"") && !strings.Contains(text, "\"expo\"") {
		return "package.json does not depend on react-native or expo — run this in your RN/Expo project root"
	}
	return ""
}

func requireFeedbackProject(dir string) string {
	if reason := requireReactNativeProject(dir); reason == "" {
		return ""
	}
	// Fall through for web / Flutter projects that opted into the
	// feedback SDK but are not RN — detect via pubspec.yaml or a
	// web package.
	if hasFile(dir, "pubspec.yaml") || hasFile(dir, "package.json") {
		return ""
	}
	return fmt.Sprintf("no package.json or pubspec.yaml in %s — the feedback CI target needs a supported project", dir)
}

// hermesWorkflow drops a workflow that runs `yaver push doctor --strict`
// against the project. The runner installs yaver-cli from npm so CI and
// the laptop share a single install.
func hermesWorkflow(dir string) string {
	return `name: Yaver Hermes CI

on:
  pull_request:
  push:
    branches: [main]

jobs:
  hermes-compat:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'

      - name: Install project deps
        run: npm ci || npm install

      - name: Install Yaver (umbrella)
        run: npm install -g yaver-cli

      - name: Yaver compatibility gate
        run: yaver push doctor --strict
`
}

// feedbackWorkflow runs lint/typecheck and asserts the feedback SDK is
// actually wired in. Same umbrella install.
func feedbackWorkflow(dir string) string {
	return `name: Yaver Feedback CI

on:
  pull_request:
  push:
    branches: [main]

jobs:
  feedback-wired:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'

      - name: Install project deps
        run: npm ci || npm install

      - name: Install Yaver (umbrella)
        run: npm install -g yaver-cli

      - name: Assert Feedback SDK is installed
        run: |
          node -e "
            const pkg = require('./package.json');
            const deps = { ...(pkg.dependencies||{}), ...(pkg.devDependencies||{}) };
            const rn   = 'yaver-feedback-react-native';
            const web  = 'yaver-feedback-web';
            if (!deps[rn] && !deps[web]) {
              console.error('No yaver-feedback-* package found. Run: yaver sdk add feedback');
              process.exit(1);
            }
            console.log('✓ Feedback SDK present:', deps[rn] ? rn : web);
          "

      - name: Typecheck (if tsconfig present)
        run: |
          if [ -f tsconfig.json ]; then
            npx --yes tsc --noEmit
          else
            echo "No tsconfig.json — skipping typecheck"
          fi
`
}

// pushToDeviceWorkflow bundles JS + compiles Hermes bytecode on tag
// push and uploads the artifact to the GitHub release. Clients then
// fetch the release asset and push it to devices.
func pushToDeviceWorkflow(dir string) string {
	return `name: Yaver Push-to-Device Bundle

on:
  push:
    tags: ['v*']
  workflow_dispatch:

jobs:
  bundle:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'

      - name: Install project deps
        run: npm ci || npm install

      - name: Install Yaver (umbrella)
        run: npm install -g yaver-cli

      - name: Compatibility gate
        run: yaver push doctor --strict

      - name: Bundle (JS + Hermes bytecode)
        run: yaver push init && yaver push --force --bundle-only
        env:
          YAVER_CI: '1'

      - name: Upload bundle artifact
        if: startsWith(github.ref, 'refs/tags/')
        uses: softprops/action-gh-release@v2
        with:
          files: |
            .yaver-build/*.hbc
            .yaver-build/*.jsbundle
          fail_on_unmatched_files: false
`
}
