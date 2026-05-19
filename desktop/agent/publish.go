package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type PublishConfig struct {
	Version       int               `json:"version" yaml:"version"`
	DefaultTarget string            `json:"defaultTarget,omitempty" yaml:"defaultTarget,omitempty"`
	Fallback      PublishFallback   `json:"fallback,omitempty" yaml:"fallback,omitempty"`
	Targets       []PublishTarget   `json:"targets" yaml:"targets"`
	Metadata      map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type PublishFallback struct {
	GitHubAllowed bool     `json:"githubAllowed,omitempty" yaml:"githubAllowed,omitempty"`
	Repo          string   `json:"repo,omitempty" yaml:"repo,omitempty"`
	Workflow      string   `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Ref           string   `json:"ref,omitempty" yaml:"ref,omitempty"`
	RunnerLabels  []string `json:"runnerLabels,omitempty" yaml:"runnerLabels,omitempty"`
}

type PublishTarget struct {
	ID            string            `json:"id" yaml:"id"`
	Label         string            `json:"label,omitempty" yaml:"label,omitempty"`
	Kind          string            `json:"kind" yaml:"kind"`
	WorkDir       string            `json:"workDir,omitempty" yaml:"workDir,omitempty"`
	Uploader      string            `json:"uploader,omitempty" yaml:"uploader,omitempty"`
	Submitter     string            `json:"submitter,omitempty" yaml:"submitter,omitempty"`
	PrepareCmd    string            `json:"prepareCommand,omitempty" yaml:"prepareCommand,omitempty"`
	PublishCmd    string            `json:"publishCommand,omitempty" yaml:"publishCommand,omitempty"`
	BuildPlatform string            `json:"buildPlatform,omitempty" yaml:"buildPlatform,omitempty"`
	BuildArgs     []string          `json:"buildArgs,omitempty" yaml:"buildArgs,omitempty"`
	ArtifactGlobs []string          `json:"artifactGlobs,omitempty" yaml:"artifactGlobs,omitempty"`
	Env           map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFromVault  map[string]string `json:"envFromVault,omitempty" yaml:"envFromVault,omitempty"`
	EnvFromGitHub map[string]string `json:"envFromGitHub,omitempty" yaml:"envFromGitHub,omitempty"`
	RunnerLabels  []string          `json:"runnerLabels,omitempty" yaml:"runnerLabels,omitempty"`
	Fallback      *PublishFallback  `json:"fallback,omitempty" yaml:"fallback,omitempty"`
}

type PublishRunStatus string

const (
	PublishRunRunning    PublishRunStatus = "running"
	PublishRunCompleted  PublishRunStatus = "completed"
	PublishRunFailed     PublishRunStatus = "failed"
	PublishRunDispatched PublishRunStatus = "dispatched"
)

type PublishRun struct {
	ID           string            `json:"id"`
	ProjectDir   string            `json:"projectDir"`
	TargetID     string            `json:"targetId"`
	TargetKind   string            `json:"targetKind"`
	Provider     string            `json:"provider"`
	Status       PublishRunStatus  `json:"status"`
	WorkDir      string            `json:"workDir,omitempty"`
	Command      string            `json:"command,omitempty"`
	BuildID      string            `json:"buildId,omitempty"`
	ExecID       string            `json:"execId,omitempty"`
	ArtifactPath string            `json:"artifactPath,omitempty"`
	ArtifactName string            `json:"artifactName,omitempty"`
	Artifacts    []PublishArtifact `json:"artifacts,omitempty"`
	Message      string            `json:"message,omitempty"`
	Error        string            `json:"error,omitempty"`
	StartedAt    string            `json:"startedAt"`
	FinishedAt   string            `json:"finishedAt,omitempty"`
}

type PublishArtifact struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Bucket    string `json:"bucket,omitempty"`
	Key       string `json:"key,omitempty"`
	PublicURL string `json:"publicUrl,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type PublishManager struct {
	mu       sync.RWMutex
	runs     map[string]*PublishRun
	execMgr  *ExecManager
	buildMgr *BuildManager
	workDir  string
}

func NewPublishManager(execMgr *ExecManager, buildMgr *BuildManager, workDir string) *PublishManager {
	return &PublishManager{
		runs:     make(map[string]*PublishRun),
		execMgr:  execMgr,
		buildMgr: buildMgr,
		workDir:  workDir,
	}
}

func publishConfigPath(dir string) string {
	return filepath.Join(dir, ".yaver", "publish.yaml")
}

func loadPublishConfig(dir string) (*PublishConfig, error) {
	data, err := os.ReadFile(publishConfigPath(dir))
	if err != nil {
		return nil, err
	}
	var cfg PublishConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Targets == nil {
		cfg.Targets = []PublishTarget{}
	}
	return &cfg, nil
}

func savePublishConfig(dir string, cfg *PublishConfig) error {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Targets == nil {
		cfg.Targets = []PublishTarget{}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".yaver"), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(publishConfigPath(dir), data, 0o644)
}

func loadOrScaffoldPublishConfig(dir string) (*PublishConfig, bool, error) {
	cfg, err := loadPublishConfig(dir)
	if err == nil {
		return cfg, true, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, err
	}
	return scaffoldPublishConfig(dir), false, nil
}

func scaffoldPublishConfig(dir string) *PublishConfig {
	cfg := &PublishConfig{
		Version: 1,
		Fallback: PublishFallback{
			GitHubAllowed: false,
			Workflow:      "yaver-publish.yml",
			Ref:           "main",
		},
		Targets: []PublishTarget{},
	}
	provider, repo := detectRepoFromGit(dir)
	if provider == CIGitHub {
		cfg.Fallback.Repo = repo
	}
	targets := detectPublishTargets(dir)
	cfg.Targets = targets
	if len(targets) > 0 {
		cfg.DefaultTarget = targets[0].ID
	}
	return cfg
}

func detectPublishTargets(root string) []PublishTarget {
	type pkgInfo struct {
		Name    string `json:"name"`
		Private bool   `json:"private"`
	}
	seen := map[string]bool{}
	var targets []PublishTarget
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "dist": true, "build": true, "vendor": true,
		"ios": true, "android": true, ".dart_tool": true, ".next": true,
	}
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != root {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, path)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		relDir, _ := filepath.Rel(root, filepath.Dir(path))
		key := filepath.ToSlash(relDir)
		if key == "." {
			key = ""
		}

		switch d.Name() {
		case "package.json":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var pkg pkgInfo
			if json.Unmarshal(data, &pkg) != nil || pkg.Private {
				return nil
			}
			id := publishTargetID("npm", key)
			if !seen[id] {
				targets = append(targets, PublishTarget{
					ID:         id,
					Label:      publishTargetLabel("npm", key),
					Kind:       "npm",
					WorkDir:    key,
					Uploader:   "yaver",
					Submitter:  "yaver",
					PublishCmd: `PKG_FILE="$(npm pack | tail -n 1)" && npm publish "$PKG_FILE" --access public`,
					ArtifactGlobs: []string{
						"*.tgz",
					},
					EnvFromVault: map[string]string{
						"NODE_AUTH_TOKEN": "npm-token",
					},
					EnvFromGitHub: map[string]string{
						"NODE_AUTH_TOKEN": "NPM_TOKEN",
					},
				})
				seen[id] = true
			}
			if looksLikeMobileProject(filepath.Dir(path), string(data)) {
				tf := publishTargetID("testflight", key)
				if !seen[tf] {
					targets = append(targets, PublishTarget{
						ID:            tf,
						Label:         publishTargetLabel("testflight", key),
						Kind:          "testflight",
						WorkDir:       key,
						Uploader:      "yaver",
						Submitter:     "yaver",
						BuildPlatform: detectIOSBuildPlatform(filepath.Dir(path), string(data)),
						RunnerLabels:  []string{"self-hosted", "macOS"},
					})
					seen[tf] = true
				}
				ps := publishTargetID("playstore", key)
				if !seen[ps] {
					targets = append(targets, PublishTarget{
						ID:            ps,
						Label:         publishTargetLabel("playstore", key),
						Kind:          "playstore",
						WorkDir:       key,
						Uploader:      "yaver",
						Submitter:     "yaver",
						BuildPlatform: detectAndroidBuildPlatform(filepath.Dir(path), string(data)),
					})
					seen[ps] = true
				}
			}
		case "pubspec.yaml":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var pubspec struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(data, &pubspec) != nil || strings.TrimSpace(pubspec.Name) == "" {
				return nil
			}
			id := publishTargetID("pubdev", key)
			if !seen[id] {
				targets = append(targets, PublishTarget{
					ID:         id,
					Label:      publishTargetLabel("pub.dev", key),
					Kind:       "pubdev",
					WorkDir:    key,
					Uploader:   "yaver",
					Submitter:  "yaver",
					PublishCmd: "flutter pub publish --force",
				})
				seen[id] = true
			}
			if fileExists(filepath.Join(filepath.Dir(path), "ios")) {
				tf := publishTargetID("testflight", key)
				if !seen[tf] {
					targets = append(targets, PublishTarget{
						ID:            tf,
						Label:         publishTargetLabel("testflight", key),
						Kind:          "testflight",
						WorkDir:       key,
						Uploader:      "yaver",
						Submitter:     "yaver",
						BuildPlatform: string(PlatformFlutterIPA),
						RunnerLabels:  []string{"self-hosted", "macOS"},
					})
					seen[tf] = true
				}
			}
			if fileExists(filepath.Join(filepath.Dir(path), "android")) {
				ps := publishTargetID("playstore", key)
				if !seen[ps] {
					targets = append(targets, PublishTarget{
						ID:            ps,
						Label:         publishTargetLabel("playstore", key),
						Kind:          "playstore",
						WorkDir:       key,
						Uploader:      "yaver",
						Submitter:     "yaver",
						BuildPlatform: string(PlatformFlutterAAB),
					})
					seen[ps] = true
				}
			}
		case "pyproject.toml":
			id := publishTargetID("pypi", key)
			if !seen[id] {
				targets = append(targets, PublishTarget{
					ID:         id,
					Label:      publishTargetLabel("PyPI", key),
					Kind:       "pypi",
					WorkDir:    key,
					Uploader:   "yaver",
					Submitter:  "yaver",
					PublishCmd: "python -m build && python -m twine upload dist/*",
					ArtifactGlobs: []string{
						"dist/*",
					},
					Env: map[string]string{
						"TWINE_USERNAME": "__token__",
					},
					EnvFromVault: map[string]string{
						"TWINE_PASSWORD": "pypi-token",
					},
					EnvFromGitHub: map[string]string{
						"TWINE_PASSWORD": "PYPI_TOKEN",
					},
				})
				seen[id] = true
			}
		}
		return nil
	})

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].ID < targets[j].ID
	})
	return targets
}

func looksLikeMobileProject(dir, packageJSON string) bool {
	return strings.Contains(packageJSON, `"react-native"`) ||
		strings.Contains(packageJSON, `"expo"`) ||
		fileExists(filepath.Join(dir, "ios")) ||
		fileExists(filepath.Join(dir, "android"))
}

func detectIOSBuildPlatform(dir, packageJSON string) string {
	switch {
	case strings.Contains(packageJSON, `"expo"`):
		return string(PlatformXcodeIPA)
	case strings.Contains(packageJSON, `"react-native"`):
		return string(PlatformXcodeIPA)
	case fileExists(filepath.Join(dir, "ios")):
		return string(PlatformXcodeIPA)
	default:
		return string(PlatformXcodeIPA)
	}
}

func detectAndroidBuildPlatform(dir, packageJSON string) string {
	switch {
	case strings.Contains(packageJSON, `"expo"`):
		return string(PlatformGradleAAB)
	case strings.Contains(packageJSON, `"react-native"`):
		return string(PlatformGradleAAB)
	case fileExists(filepath.Join(dir, "android")):
		return string(PlatformGradleAAB)
	default:
		return string(PlatformGradleAAB)
	}
}

func publishTargetID(kind, rel string) string {
	if rel == "" {
		return kind
	}
	clean := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(rel)
	return kind + "-" + clean
}

func publishTargetLabel(kind, rel string) string {
	if rel == "" {
		return kind
	}
	return fmt.Sprintf("%s (%s)", kind, filepath.ToSlash(rel))
}

func (pm *PublishManager) StartRun(projectDir, targetID string, allowGitHubFallback bool) (*PublishRun, error) {
	cfg, err := loadPublishConfig(projectDir)
	if err != nil {
		return nil, err
	}
	target, err := findPublishTarget(cfg, targetID)
	if err != nil {
		return nil, err
	}
	workDir := projectDir
	if target.WorkDir != "" {
		workDir = filepath.Join(projectDir, filepath.FromSlash(target.WorkDir))
	}
	run := &PublishRun{
		ID:         uuid.New().String()[:8],
		ProjectDir: projectDir,
		TargetID:   target.ID,
		TargetKind: target.Kind,
		Provider:   "local",
		Status:     PublishRunRunning,
		WorkDir:    workDir,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	pm.mu.Lock()
	pm.runs[run.ID] = run
	pm.mu.Unlock()
	go pm.executeRun(run, cfg, target, allowGitHubFallback)
	return run, nil
}

func (pm *PublishManager) executeRun(run *PublishRun, cfg *PublishConfig, target PublishTarget, allowGitHubFallback bool) {
	if !pm.canRunLocally(target) {
		if allowGitHubFallback && publishGitHubAllowed(cfg, target) {
			if err := pm.dispatchGitHubFallback(run, cfg, target); err != nil {
				pm.failRun(run, err)
				return
			}
			return
		}
		pm.failRun(run, fmt.Errorf("%s requires local capability not available on this machine", target.Kind))
		return
	}
	var err error
	switch target.Kind {
	case "npm", "pypi", "pubdev", "custom":
		err = pm.runShellPublish(run, target)
	case "testflight", "playstore":
		err = pm.runMobilePublish(run, target)
	default:
		err = fmt.Errorf("unsupported publish target kind: %s", target.Kind)
	}
	if err == nil {
		return
	}
	if allowGitHubFallback && publishGitHubAllowed(cfg, target) {
		if dispatchErr := pm.dispatchGitHubFallback(run, cfg, target); dispatchErr == nil {
			return
		} else {
			err = fmt.Errorf("%v; github fallback failed: %v", err, dispatchErr)
		}
	}
	pm.failRun(run, err)
}

func (pm *PublishManager) canRunLocally(target PublishTarget) bool {
	if target.Kind == "testflight" {
		return runtime.GOOS == "darwin"
	}
	return true
}

func publishGitHubAllowed(cfg *PublishConfig, target PublishTarget) bool {
	if target.Fallback != nil && target.Fallback.GitHubAllowed {
		return true
	}
	return cfg.Fallback.GitHubAllowed
}

func (pm *PublishManager) dispatchGitHubFallback(run *PublishRun, cfg *PublishConfig, target PublishTarget) error {
	repo := cfg.Fallback.Repo
	if target.Fallback != nil && strings.TrimSpace(target.Fallback.Repo) != "" {
		repo = target.Fallback.Repo
	}
	if strings.TrimSpace(repo) == "" {
		_, repo = detectRepoFromGit(run.ProjectDir)
	}
	workflow := cfg.Fallback.Workflow
	if target.Fallback != nil && strings.TrimSpace(target.Fallback.Workflow) != "" {
		workflow = target.Fallback.Workflow
	}
	if strings.TrimSpace(workflow) == "" {
		workflow = "yaver-publish.yml"
	}
	ref := cfg.Fallback.Ref
	if target.Fallback != nil && strings.TrimSpace(target.Fallback.Ref) != "" {
		ref = target.Fallback.Ref
	}
	if strings.TrimSpace(ref) == "" {
		ref = "main"
	}
	labels := target.RunnerLabels
	if len(labels) == 0 && target.Fallback != nil && len(target.Fallback.RunnerLabels) > 0 {
		labels = target.Fallback.RunnerLabels
	}
	if len(labels) == 0 {
		labels = cfg.Fallback.RunnerLabels
	}
	if len(labels) == 0 {
		labels = []string{"self-hosted"}
		if target.Kind == "testflight" {
			labels = []string{"self-hosted", "macOS"}
		}
	}
	rawLabels, _ := json.Marshal(labels)
	if err := triggerGitHubWorkflow(optionalVaultToken("github-token"), repo, workflow, ref, map[string]string{
		"target":  target.ID,
		"runs_on": string(rawLabels),
	}); err != nil {
		return err
	}
	pm.mu.Lock()
	run.Provider = "github"
	run.Status = PublishRunDispatched
	run.Message = fmt.Sprintf("yaver local path failed; dispatched %s via %s to %s", target.ID, workflow, repo)
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	pm.mu.Unlock()
	return nil
}

func (pm *PublishManager) runShellPublish(run *PublishRun, target PublishTarget) error {
	command := strings.TrimSpace(target.PublishCmd)
	if command == "" {
		switch target.Kind {
		case "npm":
			command = `PKG_FILE="$(npm pack | tail -n 1)" && npm publish "$PKG_FILE" --access public`
		case "pypi":
			command = "python -m build && python -m twine upload dist/*"
		case "pubdev":
			command = "flutter pub publish --force"
		case "custom":
			command = ""
		}
	}
	if command == "" {
		return fmt.Errorf("publish command missing for target %s", target.ID)
	}
	if prep := strings.TrimSpace(target.PrepareCmd); prep != "" {
		command = prep + " && " + command
	}
	env := resolvePublishEnv(target)
	session, err := pm.execMgr.StartExec(command, run.WorkDir, "", env, 7200)
	if err != nil {
		return err
	}
	pm.mu.Lock()
	run.Command = command
	run.ExecID = session.ID
	pm.mu.Unlock()
	<-session.doneCh
	session.mu.RLock()
	status := session.Status
	exitCode := session.ExitCode
	stderr := session.Stderr
	session.mu.RUnlock()
	if status != ExecStatusCompleted || exitCode == nil || *exitCode != 0 {
		if strings.TrimSpace(stderr) == "" {
			stderr = "publish command failed"
		}
		return fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	if err := pm.archiveTargetArtifacts(run, target); err != nil {
		return err
	}
	pm.completeRun(run, "publish completed locally")
	return nil
}

func (pm *PublishManager) runMobilePublish(run *PublishRun, target PublishTarget) error {
	platform := BuildPlatform(target.BuildPlatform)
	if platform == "" {
		return fmt.Errorf("buildPlatform missing for target %s", target.ID)
	}
	env := resolvePublishEnv(target)
	command, patterns := resolveBuildCommand(platform, run.WorkDir, target.BuildArgs)
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("could not resolve build command for %s", platform)
	}
	session, err := pm.execMgr.StartExec(command, run.WorkDir, "", env, 10800)
	if err != nil {
		return err
	}
	pm.mu.Lock()
	run.Command = command
	run.ExecID = session.ID
	pm.mu.Unlock()
	<-session.doneCh
	session.mu.RLock()
	status := session.Status
	exitCode := session.ExitCode
	stderr := session.Stderr
	session.mu.RUnlock()
	if status != ExecStatusCompleted || exitCode == nil || *exitCode != 0 {
		if strings.TrimSpace(stderr) == "" {
			stderr = "mobile build failed"
		}
		return fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	artifact := detectArtifact(run.WorkDir, patterns)
	if artifact == "" {
		return fmt.Errorf("build succeeded but no artifact was found")
	}
	if pm.buildMgr != nil {
		if b, err := pm.buildMgr.RegisterArtifact(artifact, platform); err == nil {
			pm.mu.Lock()
			run.BuildID = b.ID
			pm.mu.Unlock()
		}
	}
	pm.mu.Lock()
	run.ArtifactPath = artifact
	run.ArtifactName = filepath.Base(artifact)
	pm.mu.Unlock()
	if err := pm.archiveArtifactPaths(run, []string{artifact}); err != nil {
		return err
	}
	var uploadErr error
	switch target.Kind {
	case "testflight":
		uploadErr = uploadToTestFlight(artifact)
	case "playstore":
		uploadErr = uploadToPlayStore(artifact)
	}
	if uploadErr != nil {
		return uploadErr
	}
	pm.completeRun(run, fmt.Sprintf("%s uploaded", target.Kind))
	return nil
}

func (pm *PublishManager) archiveTargetArtifacts(run *PublishRun, target PublishTarget) error {
	paths := collectPublishArtifacts(run.WorkDir, target.ArtifactGlobs)
	if len(paths) == 0 {
		return nil
	}
	return pm.archiveArtifactPaths(run, paths)
}

func collectPublishArtifacts(workDir string, globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, pattern := range globs {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(workDir, filepath.FromSlash(pattern)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			abs, err := filepath.Abs(match)
			if err != nil || seen[abs] {
				continue
			}
			seen[abs] = true
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out
}

func (pm *PublishManager) archiveArtifactPaths(run *PublishRun, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	projectName := filepath.Base(run.ProjectDir)
	bucket := "publishes"
	var archived []PublishArtifact
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		key := sanitizeBlobName(filepath.ToSlash(filepath.Join(projectName, run.TargetID, run.ID, filepath.Base(p))))
		meta, err := writeBlob(bucket, key, "", "publish:"+run.ID, data)
		if err != nil {
			return err
		}
		signed, err := signBlobURL(bucket, key, 7*24*time.Hour)
		if err != nil {
			return err
		}
		archived = append(archived, PublishArtifact{
			Name:      filepath.Base(p),
			Path:      p,
			Bucket:    bucket,
			Key:       key,
			PublicURL: signed,
			SHA256:    meta.SHA256,
		})
	}
	pm.mu.Lock()
	run.Artifacts = archived
	pm.mu.Unlock()
	return nil
}

func resolvePublishEnv(target PublishTarget) map[string]string {
	if len(target.Env) == 0 && len(target.EnvFromVault) == 0 && len(target.EnvFromGitHub) == 0 {
		return nil
	}
	out := make(map[string]string, len(target.Env)+len(target.EnvFromVault)+len(target.EnvFromGitHub))
	for k, v := range target.Env {
		out[k] = v
	}
	for envKey, vaultKey := range target.EnvFromVault {
		if val := optionalVaultToken(vaultKey); val != "" {
			out[envKey] = val
			continue
		}
		if val := os.Getenv(envKey); val != "" {
			out[envKey] = val
		}
	}
	for envKey, githubName := range target.EnvFromGitHub {
		if _, ok := out[envKey]; ok {
			continue
		}
		if val := os.Getenv(githubName); val != "" {
			out[envKey] = val
			continue
		}
		if val := os.Getenv(envKey); val != "" {
			out[envKey] = val
		}
	}
	return out
}

func optionalVaultToken(name string) string {
	passphrase := os.Getenv("YAVER_VAULT_PASSPHRASE")
	if passphrase == "" {
		cfg, err := LoadConfig()
		if err != nil || strings.TrimSpace(cfg.AuthToken) == "" {
			return ""
		}
		passphrase = DerivePassphraseFromToken(cfg.AuthToken)
	}
	vs, err := NewVaultStore(passphrase)
	if err != nil {
		return ""
	}
	entry, err := vs.Get("", name)
	if err != nil {
		return ""
	}
	return entry.Value
}

func findPublishTarget(cfg *PublishConfig, targetID string) (PublishTarget, error) {
	if strings.TrimSpace(targetID) == "" {
		targetID = cfg.DefaultTarget
	}
	for _, t := range cfg.Targets {
		if t.ID == targetID {
			return t, nil
		}
	}
	return PublishTarget{}, fmt.Errorf("publish target %q not found", targetID)
}

func (pm *PublishManager) completeRun(run *PublishRun, message string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	run.Status = PublishRunCompleted
	run.Message = message
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
}

func (pm *PublishManager) failRun(run *PublishRun, err error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	run.Status = PublishRunFailed
	run.Error = err.Error()
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
}

func (pm *PublishManager) ListRuns() []*PublishRun {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]*PublishRun, 0, len(pm.runs))
	for _, run := range pm.runs {
		cp := *run
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})
	return out
}

func (pm *PublishManager) GetRun(id string) (*PublishRun, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	run, ok := pm.runs[id]
	if !ok {
		return nil, false
	}
	cp := *run
	return &cp, true
}

func runPublish(args []string) {
	if len(args) == 0 {
		printPublishUsage()
		return
	}
	// Normie façade: `yaver publish ios|android|both [...]` delegates to
	// the existing /deploy/ship path (see publish_ship.go). Detected
	// before the subcommand switch so the project-config subcommands
	// (init/config/run/list/status) keep working unchanged.
	if isPublishStoreWord(args[0]) {
		runPublishStoreFacade(args)
		return
	}
	switch args[0] {
	case "init":
		runPublishInit(args[1:])
	case "config":
		runPublishConfig(args[1:])
	case "run":
		runPublishRun(args[1:])
	case "list", "ls":
		runPublishList()
	case "status":
		runPublishStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown publish subcommand: %s\n\n", args[0])
		printPublishUsage()
		os.Exit(1)
	}
}

func printPublishUsage() {
	fmt.Print(`Usage:
  yaver publish ios     [--app <name>] [--machine <deviceId>] [--path <dir>]
  yaver publish android [--app <name>] [--machine <deviceId>] [--path <dir>]
  yaver publish both    [--app <name>] [--machine <deviceId>] [--path <dir>]

  yaver publish init [--dir <path>] [--force]
  yaver publish config [--dir <path>]
  yaver publish run [--dir <path>] [--target <id>] [--allow-github-fallback]
  yaver publish list
  yaver publish status <run-id>

The store form (ios|android|both) is the simple path: one command ships to
the App Store and/or Google Play. Add --machine <deviceId> to run the build
on a Mac you own (a Mac-farm node) instead of this machine. Under the hood
it is the same vault-aware, preflighted /deploy/ship path as
'yaver deploy ship'.

The init/config/run subcommands are the project-config publish system
(.yaver/publish.yaml) for npm / PyPI / pub.dev and custom targets.
`)
}

func runPublishInit(args []string) {
	fs := flag.NewFlagSet("publish init", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	force := fs.Bool("force", false, "Overwrite existing .yaver/publish.yaml")
	fs.Parse(args)

	abs, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs path: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(publishConfigPath(abs)); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "%s already exists; use --force to overwrite\n", publishConfigPath(abs))
		os.Exit(1)
	}
	cfg := scaffoldPublishConfig(abs)
	if err := savePublishConfig(abs, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save publish config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s\n", publishConfigPath(abs))
	fmt.Printf("Targets: %d\n", len(cfg.Targets))
}

func runPublishConfig(args []string) {
	fs := flag.NewFlagSet("publish config", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	fs.Parse(args)

	abs, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs path: %v\n", err)
		os.Exit(1)
	}
	cfg, _, err := loadOrScaffoldPublishConfig(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load publish config: %v\n", err)
		os.Exit(1)
	}
	data, _ := yaml.Marshal(cfg)
	fmt.Print(string(data))
}

func runPublishRun(args []string) {
	fs := flag.NewFlagSet("publish run", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	target := fs.String("target", "", "Target ID from .yaver/publish.yaml")
	allowFallback := fs.Bool("allow-github-fallback", false, "Dispatch to workflow_dispatch when local capability is missing")
	fs.Parse(args)

	abs, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs path: %v\n", err)
		os.Exit(1)
	}
	runResp, err := localAgentRequest("POST", "/publish/run", map[string]interface{}{
		"dir":                 abs,
		"target":              *target,
		"allowGitHubFallback": *allowFallback,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish run: %v\n", err)
		os.Exit(1)
	}
	var run PublishRun
	if err := remarshal(runResp, &run); err != nil {
		fmt.Fprintf(os.Stderr, "decode run: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Publish started: %s (%s)\n", run.ID, run.TargetID)
}

func runPublishList() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish list: %v\n", err)
		os.Exit(1)
	}
	req, err := http.NewRequest(http.MethodGet, localAgentBaseURL()+"/publish/runs", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish list: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	resp, err2 := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err2 != nil {
		fmt.Fprintf(os.Stderr, "publish list: %v\n", err2)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "publish list: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	var runs []*PublishRun
	if err := json.NewDecoder(resp.Body).Decode(&runs); err != nil {
		fmt.Fprintf(os.Stderr, "decode runs: %v\n", err)
		os.Exit(1)
	}
	for _, run := range runs {
		fmt.Printf("%s\t%s\t%s\t%s\n", run.ID, run.Status, run.TargetID, run.Provider)
	}
}

func runPublishStatus(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver publish status <run-id>")
		os.Exit(1)
	}
	resp, err := localAgentRequest("GET", "/publish/runs/"+args[0], nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish status: %v\n", err)
		os.Exit(1)
	}
	var run PublishRun
	if err := remarshal(resp, &run); err != nil {
		fmt.Fprintf(os.Stderr, "decode run: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(run, "", "  ")
	fmt.Println(string(out))
}

func (s *HTTPServer) handlePublishConfig(w http.ResponseWriter, r *http.Request) {
	if s.publishMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "publish not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		dir := strings.TrimSpace(r.URL.Query().Get("dir"))
		if dir == "" {
			dir = s.publishMgr.workDir
		}
		cfg, exists, err := loadOrScaffoldPublishConfig(dir)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"config": cfg, "exists": exists, "path": publishConfigPath(dir)})
	case http.MethodPost:
		var body struct {
			Dir    string        `json:"dir"`
			Config PublishConfig `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		dir := strings.TrimSpace(body.Dir)
		if dir == "" {
			dir = s.publishMgr.workDir
		}
		if err := savePublishConfig(dir, &body.Config); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "path": publishConfigPath(dir)})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *HTTPServer) handlePublishRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.publishMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "publish not available"})
		return
	}
	var body struct {
		Dir                 string `json:"dir"`
		Target              string `json:"target"`
		AllowGitHubFallback bool   `json:"allowGitHubFallback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	dir := strings.TrimSpace(body.Dir)
	if dir == "" {
		dir = s.publishMgr.workDir
	}
	run, err := s.publishMgr.StartRun(dir, body.Target, body.AllowGitHubFallback)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, run)
}

func (s *HTTPServer) handlePublishRuns(w http.ResponseWriter, r *http.Request) {
	if s.publishMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "publish not available"})
		return
	}
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	jsonReply(w, http.StatusOK, s.publishMgr.ListRuns())
}

func (s *HTTPServer) handlePublishRunByID(w http.ResponseWriter, r *http.Request) {
	if s.publishMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "publish not available"})
		return
	}
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/publish/runs/")
	run, ok := s.publishMgr.GetRun(id)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "publish run not found"})
		return
	}
	jsonReply(w, http.StatusOK, run)
}
