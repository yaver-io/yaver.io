package main

import (
	"flag"
	"fmt"
	"os"
)

func runDeploy(args []string) {
	if len(args) == 0 {
		printDeployUsage()
		os.Exit(0)
	}

	// New subcommands: vault-aware shell-script generator.
	switch args[0] {
	case "generate", "gen":
		runDeployGenerateCmd(args[1:])
		return
	case "templates":
		runDeployTemplatesCmd()
		return
	}

	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	ci := fs.String("ci", "", "CI provider: github or gitlab")
	repo := fs.String("repo", "", "Repository (owner/repo for GitHub, project ID for GitLab)")
	workflow := fs.String("workflow", "", "GitHub Actions workflow filename (e.g., build.yml)")
	branch := fs.String("branch", "", "Branch to trigger (default: main)")
	tag := fs.String("tag", "", "Release tag for artifact upload (GitHub only)")
	file := fs.String("file", "", "File to deploy/upload")

	// Reorder args: flags before positional
	var reordered, positional []string
	for i := 0; i < len(args); i++ {
		if len(args[i]) > 0 && args[i][0] == '-' {
			reordered = append(reordered, args[i])
			if i+1 < len(args) && (len(args[i+1]) == 0 || args[i+1][0] != '-') {
				reordered = append(reordered, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	reordered = append(reordered, positional...)
	fs.Parse(reordered)

	if *ci == "" {
		// No CI specified — register artifact for P2P transfer
		if *file != "" {
			runBuildRegister([]string{*file})
			return
		}
		printDeployUsage()
		os.Exit(1)
	}

	switch CIProvider(*ci) {
	case CIGitHub:
		runDeployGitHub(*repo, *workflow, *branch, *tag, *file)
	case CIGitLab:
		runDeployGitLab(*repo, *branch)
	default:
		fmt.Fprintf(os.Stderr, "Unknown CI provider: %s (use github or gitlab)\n", *ci)
		os.Exit(1)
	}
}

func runDeployGitHub(repo, workflow, branch, tag, file string) {
	token := getVaultToken("github-token")
	if token == "" {
		fmt.Fprintln(os.Stderr, "GitHub token not found in vault.")
		fmt.Fprintln(os.Stderr, "Add it: yaver vault add github-token --category git-credential --value <token>")
		os.Exit(1)
	}

	// Auto-detect repo if not specified
	if repo == "" {
		wd, _ := os.Getwd()
		provider, detected := detectRepoFromGit(wd)
		if provider == CIGitHub && detected != "" {
			repo = detected
			fmt.Printf("Detected repository: %s\n", repo)
		} else {
			fmt.Fprintln(os.Stderr, "Could not detect repository. Use --repo owner/repo")
			os.Exit(1)
		}
	}

	// Upload file to release
	if file != "" && tag != "" {
		fmt.Printf("Uploading %s to GitHub Release %s...\n", file, tag)
		if err := uploadGitHubRelease(token, repo, tag, file); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Trigger workflow
	if workflow == "" {
		fmt.Fprintln(os.Stderr, "Specify --workflow <filename.yml> to trigger, or --file + --tag to upload")
		os.Exit(1)
	}

	fmt.Printf("Triggering %s/%s on branch %s...\n", repo, workflow, branch)
	if err := triggerGitHubWorkflow(token, repo, workflow, branch, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Workflow triggered successfully.")
	fmt.Printf("  View: https://github.com/%s/actions\n", repo)
}

func runDeployGitLab(projectID, branch string) {
	token := getVaultToken("gitlab-token")
	if token == "" {
		fmt.Fprintln(os.Stderr, "GitLab token not found in vault.")
		fmt.Fprintln(os.Stderr, "Add it: yaver vault add gitlab-token --category git-credential --value <token>")
		os.Exit(1)
	}

	if projectID == "" {
		fmt.Fprintln(os.Stderr, "Specify --repo <project-id> for GitLab")
		os.Exit(1)
	}

	fmt.Printf("Triggering pipeline for project %s on branch %s...\n", projectID, branch)
	if err := triggerGitLabPipeline(token, projectID, branch, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printDeployUsage() {
	fmt.Print(`Usage:
  # Script generation (vault-aware, runs on your own machine):
  yaver deploy generate --app <name> --target <target> [--out <file>]
                                                       Emit a bash deploy script
                                                       that sources secrets from
                                                       the Yaver vault.
  yaver deploy templates                               List supported (stack, target)
                                                       combinations.

  # CI / release automation (existing):
  yaver deploy --file <path>                           Register artifact for P2P transfer
  yaver deploy --ci github --workflow <file.yml>       Trigger GitHub Actions workflow
  yaver deploy --ci github --file <path> --tag <v1.0>  Upload artifact to GitHub Release
  yaver deploy --ci gitlab --repo <project-id>         Trigger GitLab CI pipeline

Options (CI mode):
  --ci        CI provider: github or gitlab
  --repo      Repository (owner/repo for GitHub, project ID for GitLab)
  --workflow  GitHub Actions workflow filename
  --branch    Branch to trigger (default: main)
  --tag       Release tag for artifact upload
  --file      File to deploy or upload

Tokens are read from the vault:
  yaver vault add github-token --category git-credential --value <token>
  yaver vault add gitlab-token --category git-credential --value <token>

P2P transfer is free and instant. CI is optional — your choice.
`)
}
