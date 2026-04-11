package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scaffold a new yaver-test-sdk project from zero.
//
// `yaver test init` drops a sensible default layout into the user's
// repo so they can go from "just installed Yaver" to "my first spec
// runs" in one command. The layout works for a React Native Expo
// project, a Next.js web project, and anything else with a local
// dev server on a port.
//
// Produces:
//
//   yaver-tests/
//     .gitignore                # ignores .history.jsonl and artifacts
//     README.md                 # one-page cheatsheet with the vocabulary
//     example-web.test.yaml     # hits http://127.0.0.1:3000 by default
//     example-rn.test.yaml      # ios-sim / android-emu template
//
// Anything that already exists is left alone so re-running `yaver
// test init` on an existing project is a no-op.

// ScaffoldOptions configures the scaffolder.
type ScaffoldOptions struct {
	// Dir is where to drop yaver-tests/. Defaults to the current
	// working directory.
	Dir string
	// WebURL is the local web dev server URL that goes into the web
	// example spec. Defaults to http://127.0.0.1:3000.
	WebURL string
	// Flavor hints which example files to emit. "rn" for React
	// Native, "web" for web only, "both" (default).
	Flavor string
}

// Scaffold creates the yaver-tests/ directory and drops example
// specs + README. Returns a list of files written (for the CLI to
// report).
func Scaffold(opts ScaffoldOptions) ([]string, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	root := filepath.Join(dir, "yaver-tests")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	webURL := opts.WebURL
	if webURL == "" {
		webURL = "http://127.0.0.1:3000"
	}
	flavor := opts.Flavor
	if flavor == "" {
		flavor = "both"
	}

	files := []string{}

	// .gitignore — always write (it's a couple of lines).
	giPath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(giPath); os.IsNotExist(err) {
		if err := os.WriteFile(giPath, []byte(gitignoreTemplate), 0o644); err != nil {
			return files, err
		}
		files = append(files, giPath)
	}

	// README
	rdPath := filepath.Join(root, "README.md")
	if _, err := os.Stat(rdPath); os.IsNotExist(err) {
		if err := os.WriteFile(rdPath, []byte(readmeTemplate), 0o644); err != nil {
			return files, err
		}
		files = append(files, rdPath)
	}

	// Web example
	if flavor == "web" || flavor == "both" {
		path := filepath.Join(root, "example-web.test.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			body := strings.ReplaceAll(webExampleTemplate, "{{URL}}", webURL)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return files, err
			}
			files = append(files, path)
		}
	}

	// React Native example
	if flavor == "rn" || flavor == "both" {
		path := filepath.Join(root, "example-rn.test.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(rnExampleTemplate), 0o644); err != nil {
				return files, err
			}
			files = append(files, path)
		}
	}

	return files, nil
}

const gitignoreTemplate = `# yaver-test-sdk runtime artifacts — nothing in here is worth
# committing; everything is regeneratable from the specs + source.
.history.jsonl
.history.jsonl.old
.yaver-test-results/
`

const readmeTemplate = `# yaver-tests

These are the specs that run on every push via ` + "`yaver test run`" + `.
Everything lives on your machine — the runner is the ` + "`yaver`" + ` Go
binary you already have installed, and failure artifacts land in
` + "`.yaver-test-results/`" + ` (gitignored).

## Vocabulary

Every spec is a YAML file ending in ` + "`.test.yaml`" + `. Steps use one of
a small set of actions:

- ` + "`goto: /path`" + ` — navigate (web) / deep link (native)
- ` + "`click: <selector>`" + ` — tap / click
- ` + "`fill: { selector, text }`" + ` — type into a field
- ` + "`wait_for: <selector>`" + ` — block until visible
- ` + "`wait_for_url: <substr>`" + ` — block until the URL contains a substring
- ` + "`sleep_ms: <N>`" + ` — plain sleep (use sparingly)
- ` + "`assert.visible: <selector>`" + `
- ` + "`assert.text: <substring>`" + ` — current page contains text
- ` + "`assert.title: <substring>`" + ` — page title contains text
- ` + "`assert.url: <substring>`" + ` — current URL contains text
- ` + "`snapshot: <name>`" + ` — visual baseline diff (auto-primes on first run)
- ` + "`inspect: <question>`" + ` — LLM visual check (uses your own API key)
- ` + "`screenshot: true`" + ` — dump a PNG
- ` + "`eval: 'return document.title'`" + ` — raw JS (web only)

## Selectors

On ` + "`target: web`" + ` we use plain CSS. On ` + "`target: android-emu`" + ` /
` + "`target: device` (platform: android)" + ` the selector supports:

- ` + "`text=Sign In`" + ` — match the element's visible text
- ` + "`testID=submit`" + ` — React Native ` + "`testID` → content-desc" + `
- ` + "`id=email`" + ` — android resource-id (short or fully-qualified)
- ` + "`class=Button`" + ` — android class-name suffix match

React Native devs: ` + "`testID={'foo'}`" + ` in your JSX becomes
` + "`testID=foo`" + ` in the spec. That's the fastest way to keep
tests stable across re-renders.

## Running

    yaver test run                         # run every *.test.yaml here
    yaver test run --watch                 # vibe-coding loop: re-run on save
    yaver test run --headful               # show the browser
    yaver test run --update-snapshots      # accept current pixels as baseline
    yaver test record --url http://...     # capture clicks into a new YAML
    yaver test history                     # tail of recent runs
    yaver test flake                       # which spec is annoying today

See the two example files for concrete starting points.
`

const webExampleTemplate = `# Example web spec — delete or adapt for your app.
#
# Run with:
#   npm run dev &    # or whatever starts your local server
#   yaver test run   # picks up every yaver-tests/*.test.yaml

name: home page loads
target: web
url: {{URL}}
viewport: { width: 1280, height: 800 }
timeout_ms: 10000

steps:
  - goto: /
  - wait_for: body
  - assert.visible: 'h1, h2, [role=heading]'
  - screenshot: true
`

const rnExampleTemplate = `# Example React Native spec — delete or adapt for your app.
#
# Before running:
#   1. Build your app for the simulator (or plug in a device).
#   2. yaver install android-sdk   # if you haven't already
#   3. Make sure an emulator is booted, or plug in a real phone.
#
# Run with:
#   yaver test run
#
# Selectors are RN-friendly: testID={'signin-button'} in JSX becomes
# testID=signin-button in the YAML.

name: rn login flow
target: android-emu        # or ios-sim or device
timeout_ms: 15000

steps:
  - wait_for: testID=email-input
  - fill:
      selector: testID=email-input
      text: 'dev@example.test'
  - fill:
      selector: testID=password-input
      text: 'local-password'
  - click: testID=signin-button
  - wait_for: testID=home-header
  - assert.visible: text=Welcome
  - screenshot: true
`

// ScaffoldSummary returns a short string summarising what Scaffold
// wrote, for the CLI to print.
func ScaffoldSummary(files []string) string {
	if len(files) == 0 {
		return "yaver-tests/ already exists and looks populated — nothing to do."
	}
	return fmt.Sprintf("Wrote %d file(s):\n  %s", len(files), strings.Join(files, "\n  "))
}
