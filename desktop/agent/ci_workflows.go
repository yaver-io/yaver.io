package main

// ci_workflows.go — scaffolds GitHub Actions workflow files pinned to the
// Yaver self-hosted runner (`runs-on: [self-hosted, yaver]`) so the user's
// deploy pipelines (npm publish / TestFlight / Play internal / test) run on
// THEIR hardware for $0 instead of GitHub's metered (and 10× macOS) minutes.
// Pairs with ci_selfhosted_runner.go: register the runner, scaffold the
// workflow, push the tag — the build runs locally. See
// docs/yaver-managed-cloud-ci-absorption.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CIWorkflowTemplate is one scaffoldable pipeline.
type CIWorkflowTemplate struct {
	Target      string   // "test" | "npm" | "testflight" | "play-internal"
	File        string   // .github/workflows/<file>
	RunsOn      string   // YAML runs-on array, incl. os label where it matters
	Secrets     []string // GitHub Actions secrets the user must set
	Description string
	yaml        string
}

// ciWorkflowTemplates returns the catalog. Secrets use the conventional names
// so they line up with the repo's existing CLAUDE.md guidance.
func ciWorkflowTemplates() map[string]CIWorkflowTemplate {
	return map[string]CIWorkflowTemplate{
		"test": {
			Target:      "test",
			File:        "yaver-ci.yml",
			RunsOn:      "[self-hosted, yaver]",
			Description: "Lint + test on every PR / push to main, on your own runner.",
			yaml: `name: yaver CI
on:
  pull_request:
  push:
    branches: [main]
  workflow_dispatch:
jobs:
  test:
    runs-on: [self-hosted, yaver]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npm ci
      - run: npm test --if-present
`,
		},
		"npm": {
			Target:      "npm",
			File:        "yaver-npm-publish.yml",
			RunsOn:      "[self-hosted, yaver]",
			Secrets:     []string{"NPM_TOKEN"},
			Description: "Publish to npm on a v* tag, on your own Linux runner ($0 vs GitHub minutes).",
			yaml: `name: yaver npm publish
on:
  push:
    tags: ['v*']
  workflow_dispatch:
jobs:
  publish:
    runs-on: [self-hosted, yaver]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          registry-url: 'https://registry.npmjs.org'
      - run: npm ci
      - run: npm publish --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
`,
		},
		"testflight": {
			Target:      "testflight",
			File:        "yaver-testflight.yml",
			RunsOn:      "[self-hosted, yaver, os:darwin]",
			Secrets:     []string{"APP_STORE_CONNECT_KEY_ID", "APP_STORE_CONNECT_ISSUER_ID", "APP_STORE_CONNECT_API_KEY", "APPLE_TEAM_ID"},
			Description: "Build + upload to TestFlight on your OWN Mac — this is the big win (GitHub macOS minutes are 10× Linux; here it's $0).",
			yaml: `name: yaver TestFlight
on:
  push:
    tags: ['mobile/v*']
  workflow_dispatch:
jobs:
  testflight:
    runs-on: [self-hosted, yaver, os:darwin]
    steps:
      - uses: actions/checkout@v4
      - name: Build + upload to TestFlight
        run: ./scripts/deploy-testflight.sh
        env:
          APP_STORE_KEY_ID: ${{ secrets.APP_STORE_CONNECT_KEY_ID }}
          APP_STORE_KEY_ISSUER: ${{ secrets.APP_STORE_CONNECT_ISSUER_ID }}
          APP_STORE_CONNECT_API_KEY: ${{ secrets.APP_STORE_CONNECT_API_KEY }}
          APPLE_TEAM_ID: ${{ secrets.APPLE_TEAM_ID }}
`,
		},
		"play-internal": {
			Target:      "play-internal",
			File:        "yaver-play-internal.yml",
			RunsOn:      "[self-hosted, yaver, os:linux]",
			Secrets:     []string{"ANDROID_KEYSTORE", "ANDROID_KEYSTORE_PASSWORD", "ANDROID_KEY_ALIAS", "ANDROID_KEY_PASSWORD", "PLAY_STORE_SERVICE_ACCOUNT_JSON"},
			Description: "Build a signed AAB + upload to the Play internal-test track, on your own Linux runner.",
			yaml: `name: yaver Play internal
on:
  push:
    tags: ['mobile/v*']
  workflow_dispatch:
jobs:
  play-internal:
    runs-on: [self-hosted, yaver, os:linux]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-java@v4
        with:
          distribution: 'temurin'
          java-version: '17'
      - name: Build signed AAB
        run: ./scripts/deploy-playstore.sh
        env:
          ANDROID_KEYSTORE: ${{ secrets.ANDROID_KEYSTORE }}
          ANDROID_KEYSTORE_PASSWORD: ${{ secrets.ANDROID_KEYSTORE_PASSWORD }}
          ANDROID_KEY_ALIAS: ${{ secrets.ANDROID_KEY_ALIAS }}
          ANDROID_KEY_PASSWORD: ${{ secrets.ANDROID_KEY_PASSWORD }}
      - name: Upload to Play internal track
        run: python3 scripts/upload-playstore.py --track internal
        env:
          PLAY_STORE_SERVICE_ACCOUNT_JSON: ${{ secrets.PLAY_STORE_SERVICE_ACCOUNT_JSON }}
`,
		},
	}
}

// ciWorkflowTargets returns the sorted catalog keys (for the UI dropdown).
func ciWorkflowTargets() []string {
	tpls := ciWorkflowTemplates()
	out := make([]string, 0, len(tpls))
	for k := range tpls {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scaffoldCIWorkflow returns (and optionally writes) the workflow YAML for a
// target. When write is true it writes <workDir>/.github/workflows/<file>
// (refusing to clobber an existing file unless overwrite is set).
func scaffoldCIWorkflow(target, workDir string, write, overwrite bool) (relPath, content string, secrets []string, err error) {
	tpl, ok := ciWorkflowTemplates()[strings.TrimSpace(target)]
	if !ok {
		return "", "", nil, fmt.Errorf("unknown workflow target %q (have: %s)", target, strings.Join(ciWorkflowTargets(), ", "))
	}
	relPath = filepath.Join(".github", "workflows", tpl.File)
	content = tpl.yaml
	secrets = tpl.Secrets
	if !write {
		return relPath, content, secrets, nil
	}
	if strings.TrimSpace(workDir) == "" {
		return "", "", nil, fmt.Errorf("workDir required to write a workflow")
	}
	dest := filepath.Join(workDir, relPath)
	if !overwrite {
		if _, statErr := os.Stat(dest); statErr == nil {
			return relPath, content, secrets, fmt.Errorf("%s already exists (pass overwrite:true to replace)", relPath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", "", nil, err
	}
	if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
		return "", "", nil, err
	}
	return relPath, content, secrets, nil
}
