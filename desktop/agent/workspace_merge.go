package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type workspaceMergeOptions struct {
	Root string
	Name string
}

type workspaceMergeImport struct {
	Source     string
	TargetPath string
	App        WorkspaceApp
	Commit     string
}

type workspaceMergeResult struct {
	Root    string
	Name    string
	Imports []workspaceMergeImport
}

func runWorkspaceMerge(args []string) {
	fs := flag.NewFlagSet("workspace merge", flag.ExitOnError)
	root := fs.String("root", "", "Target monorepo root (default: current directory)")
	name := fs.String("name", "", "Workspace name (default: basename of root)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: yaver workspace merge [--root <dir>] [--name <workspace>] <repo-or-path>...\n")
		fmt.Fprintf(os.Stderr, "Example: yaver workspace merge --root ~/Workspace/acme ./web ./api git@github.com:me/mobile.git\n")
	}
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(1)
	}

	opts := workspaceMergeOptions{
		Root: strings.TrimSpace(*root),
		Name: strings.TrimSpace(*name),
	}
	res, err := mergeWorkspaceRepos(fs.Args(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace merge failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Merged %d repo(s) into %s\n", len(res.Imports), res.Root)
	for _, imp := range res.Imports {
		fmt.Printf("  - %-24s -> %-20s (%s)\n", imp.Source, imp.TargetPath, imp.App.Stack)
	}
	fmt.Printf("\nNext:\n")
	fmt.Printf("  cd %s\n", res.Root)
	fmt.Printf("  yaver workspace status\n")
	fmt.Printf("  yaver workspace init --autoinit\n")
}

func mergeWorkspaceRepos(rawSources []string, opts workspaceMergeOptions) (*workspaceMergeResult, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
		root = cwd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("abs root: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir root: %w", err)
	}
	if err := ensureWorkspaceMergeRoot(root); err != nil {
		return nil, err
	}
	if err := ensureWorkspaceMergeBaseCommit(root); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(root)
	}

	manifest, _ := loadWorkspaceManifestIfPresent(root)
	if manifest == nil {
		manifest = &WorkspaceManifest{
			Version: 1,
			Name:    name,
			Workspace: WorkspaceConfig{
				Root:          ".",
				PrimaryDevice: "auto",
				Relay:         "managed",
				Vault:         "local",
			},
		}
	} else if strings.TrimSpace(manifest.Name) == "" {
		manifest.Name = name
	}

	existingPaths := map[string]bool{}
	existingNames := map[string]bool{}
	for _, app := range manifest.Apps {
		existingPaths[cleanWorkspaceTargetPath(app.Path)] = true
		existingNames[strings.ToLower(strings.TrimSpace(app.Name))] = true
	}

	result := &workspaceMergeResult{Root: root, Name: manifest.Name}
	for idx, raw := range rawSources {
		src, override := parseWorkspaceMergeSource(raw)
		if src == "" {
			return nil, fmt.Errorf("empty source in %q", raw)
		}
		repoURL, displayName, ref, err := resolveWorkspaceMergeSource(src)
		if err != nil {
			return nil, err
		}
		remoteName := fmt.Sprintf("yaver-import-%d-%s", idx+1, sanitizeWorkspaceMergeName(displayName))
		if err := workspaceGit(root, "remote", "add", remoteName, repoURL); err != nil {
			return nil, fmt.Errorf("add remote for %s: %w", src, err)
		}
		imported := false
		func() {
			defer func() {
				_ = workspaceGit(root, "remote", "remove", remoteName)
			}()
			if err = workspaceGit(root, "fetch", "--no-tags", remoteName, ref); err != nil {
				err = fmt.Errorf("fetch %s: %w", src, err)
				return
			}
			files, treeErr := workspaceGitOutput(root, "ls-tree", "-r", "--name-only", "FETCH_HEAD")
			if treeErr != nil {
				err = fmt.Errorf("inspect %s tree: %w", src, treeErr)
				return
			}
			stack, role := detectWorkspaceMergeShape(displayName, strings.Split(strings.TrimSpace(files), "\n"))
			targetPath := chooseWorkspaceMergeTargetPath(root, manifest, displayName, stack, role, override, existingPaths)
			if targetPath == "" {
				err = fmt.Errorf("could not choose target path for %s", src)
				return
			}
			if existingPaths[targetPath] {
				err = fmt.Errorf("target path %s already exists in workspace", targetPath)
				return
			}
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(targetPath))); statErr == nil {
				err = fmt.Errorf("target path %s already exists on disk", targetPath)
				return
			}
			if mergeErr := importWorkspaceMergeRef(root, targetPath, displayName); mergeErr != nil {
				err = fmt.Errorf("import %s into %s: %w", src, targetPath, mergeErr)
				return
			}
			commit, revErr := workspaceGitOutput(root, "rev-parse", "FETCH_HEAD")
			if revErr != nil {
				err = fmt.Errorf("resolve imported commit for %s: %w", src, revErr)
				return
			}
			appName := uniqueWorkspaceAppName(targetPath, existingNames)
			app := WorkspaceApp{
				Name:  appName,
				Path:  "./" + targetPath,
				Stack: stack,
			}
			manifest.Apps = append(manifest.Apps, app)
			existingPaths[targetPath] = true
			existingNames[strings.ToLower(appName)] = true
			result.Imports = append(result.Imports, workspaceMergeImport{
				Source:     src,
				TargetPath: targetPath,
				App:        app,
				Commit:     strings.TrimSpace(commit),
			})
			imported = true
		}()
		if err != nil {
			if imported {
				_ = workspaceGit(root, "reset", "--hard", "HEAD")
			}
			return nil, err
		}
	}

	sort.Slice(manifest.Apps, func(i, j int) bool {
		return manifest.Apps[i].Path < manifest.Apps[j].Path
	})
	if err := writeWorkspaceMergeScaffold(root, manifest); err != nil {
		return nil, err
	}
	if dirty, err := workspaceRepoDirty(root); err == nil && dirty {
		if err := workspaceGit(root, "add", "README.md", ".gitignore", "package.json", WorkspaceManifestPath); err != nil {
			return nil, fmt.Errorf("stage workspace scaffold: %w", err)
		}
		if err := workspaceGit(root, "commit", "-m", "yaver: wire monorepo workspace"); err != nil {
			return nil, fmt.Errorf("commit workspace scaffold: %w", err)
		}
	}
	return result, nil
}

func ensureWorkspaceMergeRoot(root string) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		dirty, derr := workspaceRepoDirty(root)
		if derr != nil {
			return derr
		}
		if dirty {
			return fmt.Errorf("target repo %s has uncommitted changes; commit or stash them first", root)
		}
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read root: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("target %s is not a git repo and is not empty", root)
	}
	if err := workspaceGit(root, "init", "-b", "main"); err != nil {
		return fmt.Errorf("git init %s: %w", root, err)
	}
	return nil
}

func ensureWorkspaceMergeBaseCommit(root string) error {
	if _, err := workspaceGitOutput(root, "rev-parse", "--verify", "HEAD"); err == nil {
		return nil
	}
	readme := "# Yaver Monorepo\n\nThis workspace was bootstrapped by `yaver workspace merge`.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		return fmt.Errorf("write README.md: %w", err)
	}
	if err := workspaceGit(root, "add", "README.md"); err != nil {
		return fmt.Errorf("stage initial commit: %w", err)
	}
	if err := workspaceGit(root, "commit", "-m", "yaver: initialize monorepo"); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}
	return nil
}

func parseWorkspaceMergeSource(raw string) (source string, targetOverride string) {
	parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
	source = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		targetOverride = strings.Trim(strings.TrimSpace(parts[1]), "/")
	}
	return source, targetOverride
}

func resolveWorkspaceMergeSource(src string) (repoURL string, name string, ref string, err error) {
	ref = "HEAD"
	if stat, statErr := os.Stat(src); statErr == nil && stat.IsDir() {
		abs, absErr := filepath.Abs(src)
		if absErr != nil {
			return "", "", "", fmt.Errorf("abs path %s: %w", src, absErr)
		}
		if _, gitErr := os.Stat(filepath.Join(abs, ".git")); gitErr != nil {
			return "", "", "", fmt.Errorf("source path %s is not a git repo", abs)
		}
		name = filepath.Base(abs)
		if branch, branchErr := workspaceGitOutput(abs, "symbolic-ref", "--quiet", "--short", "HEAD"); branchErr == nil && strings.TrimSpace(branch) != "" {
			ref = strings.TrimSpace(branch)
		}
		return abs, name, ref, nil
	}
	name = repoNameFromURL(src)
	if strings.TrimSpace(name) == "" {
		return "", "", "", fmt.Errorf("could not infer repo name from %s", src)
	}
	return src, name, ref, nil
}

func detectWorkspaceMergeShape(name string, files []string) (stack string, role string) {
	hasRoot := func(candidates ...string) bool {
		for _, file := range files {
			for _, candidate := range candidates {
				if file == candidate {
					return true
				}
			}
		}
		return false
	}
	hasPrefix := func(prefixes ...string) bool {
		for _, file := range files {
			for _, prefix := range prefixes {
				if strings.HasPrefix(file, prefix) {
					return true
				}
			}
		}
		return false
	}

	switch {
	case hasRoot("app.json", "app.config.js", "app.config.ts", "eas.json"):
		return "react-native-expo", "mobile"
	case hasRoot("package.json") && (hasPrefix("android/") || hasPrefix("ios/")):
		return "react-native", "mobile"
	case hasRoot("pubspec.yaml"):
		return "flutter", "mobile"
	case hasRoot("next.config.js", "next.config.ts", "next.config.mjs"):
		return "nextjs", "web"
	case hasRoot("vite.config.ts", "vite.config.js", "vite.config.mjs"):
		return "vite", "web"
	case hasRoot("convex.json") || hasPrefix("convex/"):
		return "convex", "backend"
	case hasRoot("go.mod"):
		return "go", "backend"
	case hasRoot("pyproject.toml", "setup.py"):
		return "python", "backend"
	case hasRoot("Cargo.toml"):
		return "rust", "backend"
	case hasRoot("build.gradle", "build.gradle.kts"):
		return "gradle", "backend"
	case hasRoot("bun.lock", "bunfig.toml"):
		return "bun", inferWorkspaceMergeRoleFromName(name)
	case hasRoot("package.json"):
		return "node", inferWorkspaceMergeRoleFromName(name)
	default:
		return "node", inferWorkspaceMergeRoleFromName(name)
	}
}

func inferWorkspaceMergeRoleFromName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(lower, "mobile"), strings.Contains(lower, "ios"), strings.Contains(lower, "android"):
		return "mobile"
	case strings.Contains(lower, "web"), strings.Contains(lower, "frontend"), strings.Contains(lower, "client"), strings.Contains(lower, "site"):
		return "web"
	case strings.Contains(lower, "shared"), strings.Contains(lower, "package"), strings.Contains(lower, "sdk"), strings.Contains(lower, "lib"), strings.Contains(lower, "utils"):
		return "package"
	default:
		return "backend"
	}
}

func chooseWorkspaceMergeTargetPath(root string, manifest *WorkspaceManifest, repoName string, stack string, role string, override string, existing map[string]bool) string {
	if trimmed := cleanWorkspaceTargetPath(override); trimmed != "" {
		return uniqueWorkspaceTargetPath(trimmed, existing)
	}
	base := sanitizeWorkspaceMergeName(repoName)
	switch role {
	case "mobile":
		if base == "mobile" || base == "app" || base == "client" {
			return uniqueWorkspaceTargetPath("apps/mobile", existing)
		}
		return uniqueWorkspaceTargetPath("apps/"+base, existing)
	case "web":
		if base == "web" || base == "frontend" || base == "site" {
			return uniqueWorkspaceTargetPath("apps/web", existing)
		}
		return uniqueWorkspaceTargetPath("apps/"+base, existing)
	case "package":
		return uniqueWorkspaceTargetPath("packages/"+base, existing)
	default:
		if base == "backend" || base == "api" || base == "server" {
			return uniqueWorkspaceTargetPath("backend", existing)
		}
		if strings.HasPrefix(base, "backend-") || strings.HasPrefix(base, "api-") || strings.HasPrefix(base, "server-") {
			return uniqueWorkspaceTargetPath("backend/"+base, existing)
		}
		if len(manifest.Apps) == 0 && (stack == "go" || stack == "convex" || stack == "python" || stack == "node") {
			if _, err := os.Stat(filepath.Join(root, "backend")); err != nil {
				return uniqueWorkspaceTargetPath("backend", existing)
			}
		}
		return uniqueWorkspaceTargetPath("backend/"+base, existing)
	}
}

func cleanWorkspaceTargetPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func uniqueWorkspaceTargetPath(base string, existing map[string]bool) string {
	base = cleanWorkspaceTargetPath(base)
	if base == "" {
		return ""
	}
	if !existing[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !existing[candidate] {
			return candidate
		}
	}
}

func uniqueWorkspaceAppName(targetPath string, existing map[string]bool) string {
	base := filepath.Base(filepath.FromSlash(targetPath))
	base = sanitizeWorkspaceMergeName(base)
	if base == "" {
		base = "app"
	}
	if !existing[strings.ToLower(base)] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !existing[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func importWorkspaceMergeRef(root string, targetPath string, name string) error {
	prefix := cleanWorkspaceTargetPath(targetPath)
	if prefix == "" {
		return errors.New("empty target path")
	}
	if err := workspaceGit(root, "merge", "-s", "ours", "--no-commit", "--allow-unrelated-histories", "FETCH_HEAD"); err != nil {
		_ = workspaceGit(root, "merge", "--abort")
		return err
	}
	if err := workspaceGit(root, "read-tree", "--prefix="+prefix+"/", "-u", "FETCH_HEAD"); err != nil {
		_ = workspaceGit(root, "merge", "--abort")
		return err
	}
	msg := fmt.Sprintf("Import %s into %s", name, prefix)
	if err := workspaceGit(root, "commit", "-m", msg); err != nil {
		_ = workspaceGit(root, "merge", "--abort")
		return err
	}
	return nil
}

func writeWorkspaceMergeScaffold(root string, manifest *WorkspaceManifest) error {
	if manifest == nil {
		return fmt.Errorf("nil workspace manifest")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		manifest.Name = filepath.Base(root)
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal workspace manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, WorkspaceManifestPath), data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", WorkspaceManifestPath, err)
	}

	readmePath := filepath.Join(root, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		readme := fmt.Sprintf("# %s\n\nYaver monorepo. Apps are declared in `%s`.\n", manifest.Name, WorkspaceManifestPath)
		if err := os.WriteFile(readmePath, []byte(readme), 0o644); err != nil {
			return fmt.Errorf("write README.md: %w", err)
		}
	}
	gitignorePath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		gitignore := "node_modules/\n.dist/\ndist/\nbuild/\n.next/\n.expo/\n.DS_Store\n"
		if err := os.WriteFile(gitignorePath, []byte(gitignore), 0o644); err != nil {
			return fmt.Errorf("write .gitignore: %w", err)
		}
	}
	packageJSONPath := filepath.Join(root, "package.json")
	if _, err := os.Stat(packageJSONPath); os.IsNotExist(err) {
		pkg := fmt.Sprintf("{\n  \"name\": %q,\n  \"private\": true,\n  \"workspaces\": [\"apps/*\", \"packages/*\"]\n}\n", sanitizeWorkspaceMergeName(manifest.Name))
		if err := os.WriteFile(packageJSONPath, []byte(pkg), 0o644); err != nil {
			return fmt.Errorf("write package.json: %w", err)
		}
	}
	return nil
}

func loadWorkspaceManifestIfPresent(root string) (*WorkspaceManifest, error) {
	path := filepath.Join(root, WorkspaceManifestPath)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return LoadWorkspaceManifest(root)
}

func workspaceRepoDirty(root string) (bool, error) {
	out, err := workspaceGitOutput(root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func workspaceGit(dir string, args ...string) error {
	_, err := workspaceGitCombined(dir, args...)
	return err
}

func workspaceGitOutput(dir string, args ...string) (string, error) {
	out, err := workspaceGitCombined(dir, args...)
	return strings.TrimSpace(out), err
}

func workspaceGitCombined(dir string, args ...string) (string, error) {
	cmd := osexec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Yaver",
		"GIT_AUTHOR_EMAIL=yaver@example.invalid",
		"GIT_COMMITTER_NAME=Yaver",
		"GIT_COMMITTER_EMAIL=yaver@example.invalid",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s %s: %s", "git", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func sanitizeWorkspaceMergeName(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	lastDash := false
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}
