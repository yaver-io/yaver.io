package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

func runRepo(args []string) {
	if len(args) == 0 {
		printRepoUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "list", "ls":
		runRepoList()
	case "auth":
		runRepoAuth(args[1:])
	case "switch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver repo switch <name-or-path>")
			os.Exit(1)
		}
		runRepoSwitch(strings.Join(args[1:], " "))
	case "refresh":
		runRepoRefresh()
	case "current":
		runRepoCurrent()
	default:
		fmt.Fprintf(os.Stderr, "Unknown repo subcommand: %s\n\n", args[0])
		printRepoUsage()
		os.Exit(1)
	}
}

func printRepoUsage() {
	fmt.Print(`Usage:
  yaver repo list              List discovered projects
  yaver repo auth status       Show Git provider / credential / vault status
  yaver repo auth setup <github|gitlab> [--token <pat>] [--host <host>] [--ssh]
                              Verify and save one provider for clone + CI/deploy
  yaver repo auth remove <github|gitlab> [--host <host>]
                              Remove provider, local git credential, and CI token
  yaver repo switch <query>    Switch working directory to a project
  yaver repo refresh           Re-run project discovery
  yaver repo current           Show current project context

Projects are auto-discovered from git repos in your home directory.
Works with or without GitHub/GitLab integration.
`)
}

func runRepoList() {
	projects := listDiscoveredProjects()
	if len(projects) == 0 {
		fmt.Println("No projects found. Run 'yaver repo refresh' to scan.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tBRANCH\tPATH")
	for _, p := range projects {
		name := filepath.Base(p.Path)
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, p.Branch, p.Path)
	}
	w.Flush()
}

func runRepoSwitch(query string) {
	match, err := findProject(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Try to update running agent's work directory
	_, err = localAgentRequest("POST", "/agent/workdir", map[string]interface{}{
		"workDir": match,
	})
	if err != nil {
		// Agent not running — just update config
		fmt.Printf("Agent not running. Setting default work directory.\n")
	} else {
		fmt.Printf("Switched to: %s\n", match)
	}

	// Persist in config
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
		return
	}
	// Store preferred work dir (used on next serve start)
	// We don't add a field — the user uses --work-dir or we print instructions
	fmt.Printf("\nTo start the agent in this directory:\n")
	fmt.Printf("  yaver serve --work-dir %s\n", match)
	_ = cfg // suppress unused
}

func runRepoRefresh() {
	fmt.Println("Scanning for projects...")
	discoverProjects()
	fp, _ := projectsFilePath()
	fmt.Printf("Done. Projects saved to %s\n", fp)
}

func runRepoCurrent() {
	// Try to get from running agent
	resp, err := localAgentRequest("GET", "/agent/context", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Agent not running. Start with 'yaver serve'.")
		os.Exit(1)
	}

	if workDir, ok := resp["workDir"].(string); ok {
		fmt.Printf("Project: %s\n", filepath.Base(workDir))
		fmt.Printf("Path:    %s\n", workDir)
	}
	if branch, ok := resp["branch"].(string); ok && branch != "" {
		fmt.Printf("Branch:  %s\n", branch)
	}
	if langs, ok := resp["languages"].([]interface{}); ok && len(langs) > 0 {
		strs := make([]string, len(langs))
		for i, l := range langs {
			strs[i] = fmt.Sprint(l)
		}
		fmt.Printf("Langs:   %s\n", strings.Join(strs, ", "))
	}
}

func runRepoAuth(args []string) {
	if len(args) == 0 {
		printRepoAuthUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "status":
		runRepoAuthStatus()
	case "setup":
		runRepoAuthSetup(args[1:])
	case "remove", "rm":
		runRepoAuthRemove(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown repo auth subcommand: %s\n\n", args[0])
		printRepoAuthUsage()
		os.Exit(1)
	}
}

func printRepoAuthUsage() {
	fmt.Print(`Usage:
  yaver repo auth status
  yaver repo auth setup <github|gitlab> [--token <pat>] [--host <host>] [--ssh]
  yaver repo auth remove <github|gitlab> [--host <host>]

Setup saves the token in both places Yaver uses today:
  1. ~/.yaver/git-credentials.json for clone/pull/private repo access
  2. vault entry github-token / gitlab-token[.<host>] for deploy + CI helpers

Examples:
  yaver repo auth setup github --token ghp_xxx
  yaver repo auth setup gitlab --host gitlab.com --token glpat-xxx
  yaver repo auth status
  yaver repo auth remove github
`)
}

func runRepoAuthStatus() {
	providers, _ := loadGitProviders()
	creds, _ := loadGitCredentials()
	vs := openVault()
	githubVault := false
	gitlabVault := false
	if entry, _ := vs.Get("", "github-token"); entry != nil && strings.TrimSpace(entry.Value) != "" {
		githubVault = true
	}
	if keys, err := listGitLabVaultKeysOptional(); err == nil && len(keys) > 0 {
		gitlabVault = true
	}

	credByHost := map[string]GitCredential{}
	for _, c := range creds {
		credByHost[strings.ToLower(strings.TrimSpace(c.Host))] = c
	}

	if len(providers) == 0 && len(creds) == 0 && !githubVault && !gitlabVault {
		fmt.Println("No Git integrations configured.")
		fmt.Println("Run `yaver repo auth setup github` or `yaver repo auth setup gitlab`.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tHOST\tUSER\tCLONE\tCI/DEPLOY\tSSH\tUPDATED")

	seen := map[string]bool{}
	for _, p := range providers {
		hostKey := strings.ToLower(strings.TrimSpace(p.Host))
		seen[hostKey] = true
		cloneReady := "no"
		if cred, ok := credByHost[hostKey]; ok && strings.TrimSpace(cred.Token) != "" {
			cloneReady = "yes"
			if strings.TrimSpace(p.Username) == "" {
				p.Username = cred.Username
			}
		}
		ciReady := "no"
		switch strings.ToLower(strings.TrimSpace(p.Provider)) {
		case "github":
			if githubVault {
				ciReady = "yes"
			}
		case "gitlab":
			if entry, _, _ := loadGitLabVaultEntryOptional(p.Host); entry != nil {
				ciReady = "yes"
			}
		}
		sshReady := "no"
		if strings.TrimSpace(p.SSHKeyPath) != "" {
			sshReady = "yes"
		}
		updated := p.SetupAt
		if ts, err := time.Parse(time.RFC3339, p.SetupAt); err == nil {
			updated = ts.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Provider, p.Host, fallbackRepoAuthValue(p.Username), cloneReady, ciReady, sshReady, updated)
	}

	for _, c := range creds {
		hostKey := strings.ToLower(strings.TrimSpace(c.Host))
		if seen[hostKey] {
			continue
		}
		provider := inferProviderFromHost(c.Host)
		ciReady := "no"
		switch provider {
		case "github":
			if githubVault {
				ciReady = "yes"
			}
		case "gitlab":
			if entry, _, _ := loadGitLabVaultEntryOptional(c.Host); entry != nil {
				ciReady = "yes"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\tyes\t%s\tno\t-\n", provider, c.Host, fallbackRepoAuthValue(c.Username), ciReady)
	}
	w.Flush()
}

func runRepoAuthSetup(args []string) {
	fs := flag.NewFlagSet("repo auth setup", flag.ExitOnError)
	token := fs.String("token", "", "Personal access token")
	host := fs.String("host", "", "Provider host (defaults: github.com / gitlab.com)")
	username := fs.String("username", "", "Optional username override")
	generateSSH := fs.Bool("ssh", false, "Generate and upload an SSH key for this machine")

	var reordered, positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			reordered = append(reordered, args[i])
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				reordered = append(reordered, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	reordered = append(reordered, positional...)
	fs.Parse(reordered)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver repo auth setup <github|gitlab> [--token <pat>] [--host <host>] [--ssh]")
		os.Exit(1)
	}
	provider := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if provider != "github" && provider != "gitlab" {
		fmt.Fprintln(os.Stderr, "Provider must be github or gitlab.")
		os.Exit(1)
	}
	targetHost := strings.TrimSpace(*host)
	if targetHost == "" {
		if provider == "github" {
			targetHost = "github.com"
		} else {
			targetHost = "gitlab.com"
		}
	}
	secret := strings.TrimSpace(*token)
	if secret == "" {
		if provider == "github" {
			secret = detectGitHubToken()
		} else {
			secret = detectGitLabToken(targetHost)
		}
	}
	if secret == "" {
		fmt.Fprintf(os.Stderr, "No %s token provided or auto-detected.\n", provider)
		fmt.Fprintf(os.Stderr, "Pass one with: yaver repo auth setup %s --token <pat>\n", provider)
		os.Exit(1)
	}

	var verifiedUser, avatarURL string
	var err error
	switch provider {
	case "github":
		verifiedUser, avatarURL, err = verifyGitHubToken(secret)
	case "gitlab":
		verifiedUser, avatarURL, err = verifyGitLabToken(targetHost, secret)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Token verification failed: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*username) != "" {
		verifiedUser = strings.TrimSpace(*username)
	}

	repoCreds, _ := loadGitCredentials()
	foundCred := false
	for i := range repoCreds {
		if strings.EqualFold(repoCreds[i].Host, targetHost) {
			repoCreds[i].Token = secret
			repoCreds[i].Username = verifiedUser
			foundCred = true
			break
		}
	}
	if !foundCred {
		repoCreds = append(repoCreds, GitCredential{Host: targetHost, Username: verifiedUser, Token: secret})
	}
	if err := saveGitCredentials(repoCreds); err != nil {
		fmt.Fprintf(os.Stderr, "Saving git credential failed: %v\n", err)
		os.Exit(1)
	}

	gp := GitProvider{
		Host:      targetHost,
		Provider:  provider,
		Username:  verifiedUser,
		Token:     secret,
		AvatarURL: avatarURL,
		SetupAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if *generateSSH {
		keyLabel := fmt.Sprintf("yaver-agent@%s", hostname())
		privPath, pubKey, err := generateYaverSSHKey(keyLabel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "SSH key generation failed: %v\n", err)
			os.Exit(1)
		}
		title := fmt.Sprintf("Yaver Agent (%s)", hostname())
		switch provider {
		case "github":
			err = addSSHKeyToGitHub(secret, title, pubKey)
		case "gitlab":
			err = addSSHKeyToGitLab(targetHost, secret, title, pubKey)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Uploading SSH key failed: %v\n", err)
			os.Exit(1)
		}
		if err := configureSSHForProvider(targetHost, privPath); err != nil {
			fmt.Fprintf(os.Stderr, "Configuring SSH failed: %v\n", err)
			os.Exit(1)
		}
		gp.SSHKeyPath = privPath
		gp.SSHKeyName = title
	}

	providers, _ := loadGitProviders()
	foundProvider := false
	for i := range providers {
		if strings.EqualFold(providers[i].Host, targetHost) {
			providers[i] = gp
			foundProvider = true
			break
		}
	}
	if !foundProvider {
		providers = append(providers, gp)
	}
	if err := saveGitProviders(providers); err != nil {
		fmt.Fprintf(os.Stderr, "Saving provider config failed: %v\n", err)
		os.Exit(1)
	}

	vaultName := provider + "-token"
	if provider == "gitlab" {
		vaultName = gitLabVaultKey(targetHost)
	}
	vs := openVault()
	if err := vs.Set(VaultEntry{
		Name:     vaultName,
		Category: "git-credential",
		Value:    secret,
		Notes:    fmt.Sprintf("%s PAT for %s (%s)", provider, targetHost, verifiedUser),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Saving CI/deploy token to vault failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Configured %s for %s.\n", provider, targetHost)
	fmt.Printf("User: %s\n", verifiedUser)
	fmt.Println("Saved clone/pull credential: yes")
	fmt.Printf("Saved vault token: %s\n", vaultName)
	if gp.SSHKeyPath != "" {
		fmt.Printf("SSH key installed: %s\n", gp.SSHKeyPath)
	}
}

func runRepoAuthRemove(args []string) {
	fs := flag.NewFlagSet("repo auth remove", flag.ExitOnError)
	host := fs.String("host", "", "Provider host override")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver repo auth remove <github|gitlab> [--host <host>]")
		os.Exit(1)
	}
	provider := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if provider != "github" && provider != "gitlab" {
		fmt.Fprintln(os.Stderr, "Provider must be github or gitlab.")
		os.Exit(1)
	}
	targetHost := strings.TrimSpace(*host)
	if targetHost == "" {
		if provider == "github" {
			targetHost = "github.com"
		} else {
			targetHost = "gitlab.com"
		}
	}

	providers, _ := loadGitProviders()
	filteredProviders := make([]GitProvider, 0, len(providers))
	for _, p := range providers {
		if strings.EqualFold(p.Host, targetHost) {
			continue
		}
		filteredProviders = append(filteredProviders, p)
	}
	if err := saveGitProviders(filteredProviders); err != nil {
		fmt.Fprintf(os.Stderr, "Removing provider config failed: %v\n", err)
		os.Exit(1)
	}

	creds, _ := loadGitCredentials()
	filteredCreds := make([]GitCredential, 0, len(creds))
	for _, c := range creds {
		if strings.EqualFold(c.Host, targetHost) {
			continue
		}
		filteredCreds = append(filteredCreds, c)
	}
	if err := saveGitCredentials(filteredCreds); err != nil {
		fmt.Fprintf(os.Stderr, "Removing git credential failed: %v\n", err)
		os.Exit(1)
	}

	vs := openVault()
	if provider == "gitlab" {
		for _, key := range gitLabVaultKeyCandidates(targetHost) {
			_ = vs.Delete("", key)
		}
	} else {
		_ = vs.Delete("", provider+"-token")
	}

	fmt.Printf("Removed %s integration for %s.\n", provider, targetHost)
}

func inferProviderFromHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	switch {
	case strings.Contains(h, "github"):
		return "github"
	case strings.Contains(h, "gitlab"):
		return "gitlab"
	default:
		return "custom"
	}
}

func fallbackRepoAuthValue(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// listDiscoveredProjects parses PROJECTS.md for project entries.
func listDiscoveredProjects() []projectInfo {
	fp, err := projectsFilePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil
	}

	var projects []projectInfo
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Projects appear as "### /path/to/project" in PROJECTS.md
		if strings.HasPrefix(line, "### /") || strings.HasPrefix(line, "### ~/") {
			path := strings.TrimPrefix(line, "### ")
			path = strings.TrimSpace(path)
			// Expand ~
			if strings.HasPrefix(path, "~/") {
				home, _ := os.UserHomeDir()
				path = filepath.Join(home, path[2:])
			}

			p := projectInfo{Path: path}
			// Look for branch in next few lines
			for j := i + 1; j < i+5 && j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "- Branch: ") {
					p.Branch = strings.TrimPrefix(lines[j], "- Branch: ")
				}
			}
			projects = append(projects, p)
		}
	}
	return projects
}

// findProject fuzzy-matches a query against discovered projects.
// Matches by repo name, directory name, or path substring.
func findProject(query string) (string, error) {
	projects := listDiscoveredProjects()
	if len(projects) == 0 {
		// Try fresh discovery
		discoverProjects()
		projects = listDiscoveredProjects()
	}
	if len(projects) == 0 {
		return "", fmt.Errorf("no projects found")
	}

	query = strings.ToLower(query)

	// Exact name match first
	for _, p := range projects {
		name := strings.ToLower(filepath.Base(p.Path))
		if name == query {
			return p.Path, nil
		}
	}

	// Substring match
	var matches []projectInfo
	for _, p := range projects {
		lower := strings.ToLower(p.Path)
		name := strings.ToLower(filepath.Base(p.Path))
		if strings.Contains(name, query) || strings.Contains(lower, query) {
			matches = append(matches, p)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no project matching %q found. Run 'yaver repo list' to see available projects", query)
	}
	if len(matches) == 1 {
		return matches[0].Path, nil
	}

	// Multiple matches — prefer shortest name (most specific)
	best := matches[0]
	for _, m := range matches[1:] {
		if len(filepath.Base(m.Path)) < len(filepath.Base(best.Path)) {
			best = m
		}
	}
	return best.Path, nil
}
