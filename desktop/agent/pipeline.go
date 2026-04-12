package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Hardware detection
// ---------------------------------------------------------------------------

// HardwareProfile describes the local machine's capabilities.
type HardwareProfile struct {
	CPUCores    int    `json:"cpuCores"`
	RAM         int64  `json:"ram"`      // bytes
	RAMHuman    string `json:"ramHuman"` // "16 GB"
	DiskFree    int64  `json:"diskFree"` // bytes
	DiskHuman   string `json:"diskHuman"`
	GPU         string `json:"gpu"`      // "Apple M2", "NVIDIA RTX 4090", or "none"
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	DockerOK    bool   `json:"dockerOk"`
	MaxParallel int    `json:"maxParallel"` // recommended parallel jobs
}

// DetectHardware probes the local machine and returns a HardwareProfile.
func DetectHardware() HardwareProfile {
	h := HardwareProfile{
		CPUCores: runtime.NumCPU(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}

	// RAM — try reading from /proc/meminfo (Linux) or sysctl (macOS/BSD)
	h.RAM = detectRAM()
	h.RAMHuman = humanBytes(h.RAM)

	// Disk free — stat the working directory's filesystem
	h.DiskFree = detectDiskFree()
	h.DiskHuman = humanBytes(h.DiskFree)

	// GPU
	h.GPU = detectGPU()

	// Docker availability
	h.DockerOK = isDockerAvailable()

	// Recommended parallelism: cores/2, min 1, max 16
	h.MaxParallel = h.CPUCores / 2
	if h.MaxParallel < 1 {
		h.MaxParallel = 1
	}
	if h.MaxParallel > 16 {
		h.MaxParallel = 16
	}

	return h
}

func detectRAM() int64 {
	// Linux: /proc/meminfo
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					kb, _ := strconv.ParseInt(fields[1], 10, 64)
					return kb * 1024
				}
			}
		}
	}
	// macOS: sysctl hw.memsize
	if out, err := osexec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		val, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return val
	}
	return 0
}

func detectDiskFree() int64 {
	wd, err := os.Getwd()
	if err != nil {
		wd = "/"
	}
	// Use df to get available bytes — works on Linux and macOS
	out, err := osexec.Command("df", "-k", wd).Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[1])
	// df -k column 3 is "Available" (Linux) or "Avail" (macOS)
	idx := 3
	if len(fields) <= idx {
		return 0
	}
	kb, _ := strconv.ParseInt(fields[idx], 10, 64)
	return kb * 1024
}

func detectGPU() string {
	// macOS: system_profiler
	if runtime.GOOS == "darwin" {
		if out, err := osexec.Command("system_profiler", "SPDisplaysDataType", "-detailLevel", "mini").Output(); err == nil {
			text := string(out)
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Chipset Model:") {
					return strings.TrimSpace(strings.TrimPrefix(line, "Chipset Model:"))
				}
			}
		}
	}
	// Linux: nvidia-smi
	if out, err := osexec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			return "NVIDIA " + name
		}
	}
	// Linux: lspci for AMD
	if out, err := osexec.Command("lspci").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(strings.ToLower(line), "vga") || strings.Contains(strings.ToLower(line), "display") {
				return strings.TrimSpace(line)
			}
		}
	}
	return "none"
}

func isDockerAvailable() bool {
	_, err := osexec.LookPath("docker")
	if err != nil {
		return false
	}
	err = osexec.Command("docker", "info").Run()
	return err == nil
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ---------------------------------------------------------------------------
// Pipeline configuration
// ---------------------------------------------------------------------------

// PipelineConfig stores persistent pipeline runner settings.
type PipelineConfig struct {
	MaxParallelJobs int    `json:"maxParallelJobs"` // 0 = auto (cores/2)
	CancelCloudCI   bool   `json:"cancelCloudCI"`   // auto-cancel GitHub/GitLab CI
	ReportToCloud   bool   `json:"reportToCloud"`   // push status back to GitHub/GitLab
	SecretsFile     string `json:"secretsFile"`     // path to secrets JSON or .env
	CacheDir        string `json:"cacheDir"`        // default: ~/.yaver/ci-cache
	ArtifactsDir    string `json:"artifactsDir"`    // default: ~/.yaver/ci-artifacts
	DockerEnabled   bool   `json:"dockerEnabled"`   // use Docker for services/containers
	AutoDetect      bool   `json:"autoDetect"`      // auto-detect CI config files
}

func defaultPipelineConfig() *PipelineConfig {
	home, _ := os.UserHomeDir()
	return &PipelineConfig{
		MaxParallelJobs: 0,
		CancelCloudCI:   false,
		ReportToCloud:   false,
		SecretsFile:     filepath.Join(home, ".yaver", "ci-secrets.json"),
		CacheDir:        filepath.Join(home, ".yaver", "ci-cache"),
		ArtifactsDir:    filepath.Join(home, ".yaver", "ci-artifacts"),
		DockerEnabled:   true,
		AutoDetect:      true,
	}
}

func pipelineConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "pipeline-config.json")
}

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// ServiceConfig describes a Docker sidecar container.
type ServiceConfig struct {
	Image       string            `json:"image"`
	Ports       []string          `json:"ports,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Options     string            `json:"options,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

// MatrixConfig holds a job's matrix strategy.
type MatrixConfig struct {
	Include []map[string]string `json:"include,omitempty"`
	Exclude []map[string]string `json:"exclude,omitempty"`
	Values  map[string][]string `json:"values"` // e.g. {"node-version": ["18","20"]}
}

// PipelineStep represents a single step inside a CI job.
type PipelineStep struct {
	Name            string            `json:"name"`
	Run             string            `json:"run,omitempty"`
	Uses            string            `json:"uses,omitempty"`
	With            map[string]string `json:"with,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	If              string            `json:"if,omitempty"`
	Status          string            `json:"status"`
	Duration        time.Duration     `json:"duration"`
	Output          string            `json:"output"`
	Error           string            `json:"error,omitempty"`
	ContinueOnError bool              `json:"continueOnError,omitempty"`
	TimeoutMin      int               `json:"timeoutMin,omitempty"`
	WorkingDir      string            `json:"workingDir,omitempty"`
}

// PipelineJob represents a CI job containing one or more steps.
type PipelineJob struct {
	Name        string            `json:"name"`
	Steps       []PipelineStep    `json:"steps"`
	Status      string            `json:"status"`
	Duration    time.Duration     `json:"duration"`
	RunsOn      string            `json:"runsOn"`
	If          string            `json:"if,omitempty"`
	Needs       []string          `json:"needs,omitempty"`
	Outputs     map[string]string `json:"outputs,omitempty"`
	Services    []string          `json:"services,omitempty"` // started service names
	Matrix      *MatrixConfig     `json:"matrix,omitempty"`
	TimeoutMin  int               `json:"timeoutMin,omitempty"`
	MatrixVars  map[string]string `json:"matrixVars,omitempty"` // current matrix combo
	ContainerID string            `json:"containerId,omitempty"`
}

// PipelineResult holds the outcome of a full pipeline run.
type PipelineResult struct {
	File           string          `json:"file"`
	Name           string          `json:"name"`
	Jobs           []PipelineJob   `json:"jobs"`
	Status         string          `json:"status"` // "passed", "failed", "cancelled"
	Duration       time.Duration   `json:"duration"`
	StartedAt      time.Time       `json:"startedAt"`
	Hardware       HardwareProfile `json:"hardware"`
	CIFormat       string          `json:"ciFormat"` // "github" or "gitlab"
	CloudCancelled bool            `json:"cloudCancelled"`
	ArtifactsDir   string          `json:"artifactsDir,omitempty"`
	RunID          string          `json:"runId"`
}

// PipelineInfo describes a workflow file without running it.
type PipelineInfo struct {
	File     string   `json:"file"`
	Name     string   `json:"name"`
	Jobs     []string `json:"jobs"`
	Triggers []string `json:"triggers"`
	Format   string   `json:"format"` // "github" or "gitlab"
}

// PipelineStatus is the current/last run state.
type PipelineStatus struct {
	Running bool            `json:"running"`
	Current *PipelineResult `json:"current,omitempty"`
	Last    *PipelineResult `json:"last,omitempty"`
}

// ---------------------------------------------------------------------------
// Internal YAML structures — GitHub Actions
// ---------------------------------------------------------------------------

type yamlWorkflow struct {
	Name string             `yaml:"name"`
	On   yaml.Node          `yaml:"on"`
	Env  map[string]string  `yaml:"env"`
	Jobs map[string]yamlJob `yaml:"jobs"`
}

type yamlMatrix struct {
	Include []map[string]interface{} `yaml:"include"`
	Exclude []map[string]interface{} `yaml:"exclude"`
	// everything else is a matrix axis: go-version, node-version, etc.
}

type yamlJob struct {
	Name       string                     `yaml:"name"`
	RunsOn     string                     `yaml:"runs-on"`
	If         string                     `yaml:"if"`
	Needs      interface{}                `yaml:"needs"` // string or []string
	Env        map[string]string          `yaml:"env"`
	Steps      []yamlStep                 `yaml:"steps"`
	Outputs    map[string]string          `yaml:"outputs"`
	Services   map[string]yamlService     `yaml:"services"`
	Container  interface{}                `yaml:"container"` // string or map
	Strategy   *yamlStrategy              `yaml:"strategy"`
	TimeoutMin int                        `yaml:"timeout-minutes"`
}

type yamlStrategy struct {
	Matrix yaml.Node `yaml:"matrix"`
	Fail   *bool     `yaml:"fail-fast"`
}

type yamlService struct {
	Image       string            `yaml:"image"`
	Ports       []string          `yaml:"ports"`
	Env         map[string]string `yaml:"env"`
	Options     string            `yaml:"options"`
	Credentials map[string]string `yaml:"credentials"`
}

type yamlStep struct {
	Name             string            `yaml:"name"`
	Run              string            `yaml:"run"`
	Uses             string            `yaml:"uses"`
	With             map[string]string `yaml:"with"`
	Env              map[string]string `yaml:"env"`
	If               string            `yaml:"if"`
	WorkingDirectory string            `yaml:"working-directory"`
	ContinueOnError  bool              `yaml:"continue-on-error"`
	TimeoutMin       int               `yaml:"timeout-minutes"`
}

// ---------------------------------------------------------------------------
// Internal YAML structures — GitLab CI
// ---------------------------------------------------------------------------

type gitlabCI struct {
	Stages    []string            `yaml:"stages"`
	Variables map[string]string   `yaml:"variables"`
	Jobs      map[string]gitlabJob
}

type gitlabJob struct {
	Stage        string            `yaml:"stage"`
	Image        string            `yaml:"image"`
	Services     []string          `yaml:"services"`
	Variables    map[string]string `yaml:"variables"`
	BeforeScript []string          `yaml:"before_script"`
	Script       []string          `yaml:"script"`
	AfterScript  []string          `yaml:"after_script"`
	Cache        *gitlabCache      `yaml:"cache"`
	Artifacts    *gitlabArtifacts  `yaml:"artifacts"`
	Needs        interface{}       `yaml:"needs"` // string or []string or []map
	Only         interface{}       `yaml:"only"`
	Except       interface{}       `yaml:"except"`
	Rules        []gitlabRule      `yaml:"rules"`
	AllowFailure bool              `yaml:"allow_failure"`
	Timeout      string            `yaml:"timeout"`
}

type gitlabCache struct {
	Key   string   `yaml:"key"`
	Paths []string `yaml:"paths"`
}

type gitlabArtifacts struct {
	Paths    []string `yaml:"paths"`
	ExpireIn string   `yaml:"expire_in"`
	When     string   `yaml:"when"`
}

type gitlabRule struct {
	If   string `yaml:"if"`
	When string `yaml:"when"`
}

// ---------------------------------------------------------------------------
// PipelineRunner
// ---------------------------------------------------------------------------

// PipelineRunner executes GitHub Actions / GitLab CI YAML workflows locally.
type PipelineRunner struct {
	mu       sync.RWMutex
	status   PipelineStatus
	cancel   context.CancelFunc
	cfg      *PipelineConfig
	hw       HardwareProfile
	secrets  map[string]string
	jobOutputs map[string]map[string]string // jobKey → output map
}

// NewPipelineRunner creates a ready-to-use PipelineRunner.
func NewPipelineRunner() *PipelineRunner {
	r := &PipelineRunner{}
	r.cfg = r.loadConfig()
	r.hw = DetectHardware()
	r.secrets = r.loadSecrets()
	r.jobOutputs = map[string]map[string]string{}
	return r
}

// GetConfig returns a copy of the current pipeline configuration.
func (r *PipelineRunner) GetConfig() *PipelineConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := *r.cfg
	return &cp
}

// SetConfig updates and persists the pipeline configuration.
func (r *PipelineRunner) SetConfig(cfg *PipelineConfig) error {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pipelineConfigPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pipelineConfigPath(), data, 0o600)
}

func (r *PipelineRunner) loadConfig() *PipelineConfig {
	cfg := defaultPipelineConfig()
	data, err := os.ReadFile(pipelineConfigPath())
	if err == nil {
		_ = json.Unmarshal(data, cfg)
	}
	return cfg
}

func (r *PipelineRunner) loadSecrets() map[string]string {
	secrets := map[string]string{}
	files := []string{}
	if r.cfg.SecretsFile != "" {
		files = append(files, r.cfg.SecretsFile)
	}
	// Also try .env.test in repo root
	if wd, err := os.Getwd(); err == nil {
		files = append(files, filepath.Join(wd, ".env.test"))
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// Try JSON first
		var m map[string]string
		if json.Unmarshal(data, &m) == nil {
			for k, v := range m {
				secrets[k] = v
			}
			continue
		}
		// Try .env format
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				secrets[strings.TrimSpace(parts[0])] = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			}
		}
	}
	return secrets
}

// Status returns the current/last run state. Thread-safe.
func (r *PipelineRunner) Status() *PipelineStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.status
	return &s
}

// Stop cancels a running pipeline.
func (r *PipelineRunner) Stop() error {
	r.mu.RLock()
	cancel := r.cancel
	running := r.status.Running
	r.mu.RUnlock()

	if !running || cancel == nil {
		return fmt.Errorf("no pipeline is running")
	}
	cancel()
	return nil
}

// List returns metadata for all workflow files found in dir.
// It checks both .github/workflows/ (GitHub Actions) and .gitlab-ci.yml (GitLab CI).
func (r *PipelineRunner) List(dir string) ([]PipelineInfo, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	var infos []PipelineInfo

	// GitHub Actions
	ghDir := dir
	if !strings.Contains(dir, ".github") {
		candidate := filepath.Join(dir, ".github", "workflows")
		if _, err := os.Stat(candidate); err == nil {
			ghDir = candidate
		}
	}
	for _, ext := range []string{"*.yml", "*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(ghDir, ext))
		for _, f := range matches {
			info, err := parseWorkflowInfo(f)
			if err != nil {
				continue
			}
			info.Format = "github"
			infos = append(infos, *info)
		}
	}

	// GitLab CI
	for _, name := range []string{".gitlab-ci.yml", ".gitlab-ci.yaml"} {
		f := filepath.Join(dir, name)
		if _, err := os.Stat(f); err == nil {
			info, err := parseGitLabInfo(f)
			if err == nil {
				infos = append(infos, *info)
			}
		}
	}

	return infos, nil
}

// ---------------------------------------------------------------------------
// GitHub Actions — Run
// ---------------------------------------------------------------------------

// Run executes a GitHub Actions workflow file locally.
// If file is empty, auto-detects from .github/workflows/.
// If job is empty, all jobs are run in dependency order.
func (r *PipelineRunner) Run(file string, job string, dryRun bool) (*PipelineResult, error) {
	r.mu.Lock()
	if r.status.Running {
		r.mu.Unlock()
		return nil, fmt.Errorf("a pipeline is already running; call Stop() first")
	}
	r.mu.Unlock()

	// Validate disk space
	if err := r.checkDiskSpace(); err != nil {
		return nil, err
	}

	// Resolve workflow file
	resolvedFile, err := resolveWorkflowFile(file)
	if err != nil {
		// Try GitLab auto-detect
		if file == "" {
			return r.tryAutoDetect(job, dryRun)
		}
		return nil, err
	}

	// Parse workflow
	wf, err := parseWorkflow(resolvedFile)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", resolvedFile, err)
	}

	// Build ordered job list
	orderedKeys, err := orderJobs(wf.Jobs, job)
	if err != nil {
		return nil, err
	}

	// Generate run ID and set up artifacts dir
	runID := fmt.Sprintf("%d", time.Now().UnixMilli())
	artifactsDir := filepath.Join(r.cfg.ArtifactsDir, runID)

	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.cancel = cancel
	r.jobOutputs = map[string]map[string]string{}
	result := &PipelineResult{
		File:         resolvedFile,
		Name:         wf.Name,
		Status:       "running",
		StartedAt:    time.Now(),
		CIFormat:     "github",
		Hardware:     r.hw,
		ArtifactsDir: artifactsDir,
		RunID:        runID,
	}
	if result.Name == "" {
		result.Name = filepath.Base(resolvedFile)
	}
	r.status.Running = true
	r.status.Current = result
	r.mu.Unlock()

	defer func() {
		cancel()
		r.mu.Lock()
		r.status.Running = false
		r.status.Last = result
		r.status.Current = nil
		r.cancel = nil
		r.mu.Unlock()
	}()

	// Optionally cancel cloud CI
	if r.cfg.CancelCloudCI && !dryRun {
		if err2 := r.CancelCloudCI("github"); err2 == nil {
			result.CloudCancelled = true
		}
	}

	start := result.StartedAt
	completedJobs := map[string]string{}
	cancelled := false

	// Determine effective parallelism
	maxParallel := r.cfg.MaxParallelJobs
	if maxParallel <= 0 {
		maxParallel = r.hw.MaxParallel
	}
	if maxParallel < 1 {
		maxParallel = 1
	}

	// Execute jobs in dependency order (sequential layers, parallel within layer)
	layers := buildJobLayers(orderedKeys, wf.Jobs)
	var resultMu sync.Mutex

	for _, layer := range layers {
		if cancelled {
			for _, key := range layer {
				yjob := wf.Jobs[key]
				pjob := buildPipelineJob(key, yjob)
				pjob.Status = "skipped"
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()
				completedJobs[key] = "skipped"
			}
			continue
		}

		// Check if any needed job in this layer failed
		readyKeys := []string{}
		for _, key := range layer {
			yjob := wf.Jobs[key]
			needs := normalizeNeeds(yjob.Needs)
			skip := false
			for _, need := range needs {
				if completedJobs[need] == "failed" || completedJobs[need] == "cancelled" {
					skip = true
					break
				}
			}
			if skip {
				pjob := buildPipelineJob(key, yjob)
				pjob.Status = "skipped"
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()
				completedJobs[key] = "skipped"
			} else {
				readyKeys = append(readyKeys, key)
			}
		}

		if len(readyKeys) == 0 {
			continue
		}

		// Run ready jobs in parallel (up to maxParallel)
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		layerCancelled := false
		var layerMu sync.Mutex

		for _, key := range readyKeys {
			key := key
			yjob := wf.Jobs[key]

			select {
			case <-ctx.Done():
				pjob := buildPipelineJob(key, yjob)
				pjob.Status = "skipped"
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()
				completedJobs[key] = "skipped"
				layerCancelled = true
				continue
			default:
			}

			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				// Expand matrix if present
				matrixCombos := r.expandMatrixFromYAML(yjob.Strategy)
				if len(matrixCombos) == 0 {
					matrixCombos = []map[string]string{nil}
				}

				var jobStatus string
				for _, combo := range matrixCombos {
					pjob := r.executeGitHubJob(ctx, key, yjob, wf, combo, artifactsDir, runID, dryRun)
					resultMu.Lock()
					result.Jobs = append(result.Jobs, pjob)
					// Capture outputs
					if pjob.Outputs != nil {
						r.mu.Lock()
						r.jobOutputs[key] = pjob.Outputs
						r.mu.Unlock()
					}
					resultMu.Unlock()
					if pjob.Status == "failed" {
						jobStatus = "failed"
					} else if pjob.Status == "cancelled" {
						jobStatus = "cancelled"
					} else if jobStatus == "" {
						jobStatus = pjob.Status
					}
				}
				if jobStatus == "" {
					jobStatus = "passed"
				}

				layerMu.Lock()
				completedJobs[key] = jobStatus
				if jobStatus == "failed" {
					layerCancelled = true
				}
				layerMu.Unlock()
			}()
		}

		wg.Wait()

		if layerCancelled {
			cancelled = true
		}
	}

	result.Duration = time.Since(start)
	result.Status = deriveOverallStatus(ctx, result.Jobs)

	// Report to cloud CI if configured
	if r.cfg.ReportToCloud && !dryRun {
		_ = r.ReportStatus("github", result)
	}

	return result, nil
}

func (r *PipelineRunner) executeGitHubJob(
	ctx context.Context,
	key string,
	yjob yamlJob,
	wf *yamlWorkflow,
	matrixVars map[string]string,
	artifactsDir, runID string,
	dryRun bool,
) PipelineJob {
	pjob := buildPipelineJob(key, yjob)
	pjob.MatrixVars = matrixVars

	// Evaluate job-level if condition
	if yjob.If != "" && !r.evalConditionCtx(yjob.If, nil, nil) {
		pjob.Status = "skipped"
		return pjob
	}

	// Check job timeout
	jobCtx := ctx
	var jobCancel context.CancelFunc
	if yjob.TimeoutMin > 0 {
		jobCtx, jobCancel = context.WithTimeout(ctx, time.Duration(yjob.TimeoutMin)*time.Minute)
		defer jobCancel()
	}

	// Start Docker services
	var serviceCleanup func()
	if len(yjob.Services) > 0 && r.cfg.DockerEnabled && r.hw.DockerOK && !dryRun {
		services := make(map[string]ServiceConfig)
		for svcName, svc := range yjob.Services {
			services[svcName] = ServiceConfig{
				Image:       svc.Image,
				Ports:       svc.Ports,
				Env:         svc.Env,
				Options:     svc.Options,
				Credentials: svc.Credentials,
			}
		}
		var svcNames []string
		var err error
		serviceCleanup, svcNames, err = r.startServices(services)
		if err != nil {
			pjob.Status = "failed"
			step := PipelineStep{Name: "Start services", Status: "failed", Error: err.Error()}
			pjob.Steps = append(pjob.Steps, step)
			return pjob
		}
		pjob.Services = svcNames
	}
	if serviceCleanup != nil {
		defer serviceCleanup()
	}

	pjob.Status = "running"
	jobStart := time.Now()

	// Build merged environment: workflow → job → matrix vars → secrets
	mergedEnv := mergeEnvMaps(wf.Env, yjob.Env)
	for k, v := range matrixVars {
		mergedEnv["MATRIX_"+strings.ToUpper(k)] = v
	}
	mergedEnv = r.resolveSecrets(mergedEnv)
	// Also inject matrix vars as MATRIX_VAR style
	for k, v := range matrixVars {
		mergedEnv[k] = v
	}

	jobFailed := false
	jobCancelled := false

	for _, ystep := range yjob.Steps {
		select {
		case <-jobCtx.Done():
			pjob.Steps = append(pjob.Steps, PipelineStep{
				Name:   ystep.Name,
				Status: "cancelled",
			})
			jobCancelled = true
			break
		default:
		}
		if jobCancelled {
			break
		}

		step := r.executeStep(jobCtx, ystep, mergedEnv, wf, artifactsDir, runID, dryRun)
		pjob.Steps = append(pjob.Steps, step)

		if step.Status == "failed" && !step.ContinueOnError {
			jobFailed = true
			break
		}
		if step.Status == "cancelled" {
			jobCancelled = true
			break
		}
	}

	pjob.Duration = time.Since(jobStart)
	switch {
	case jobCancelled:
		pjob.Status = "cancelled"
	case jobFailed:
		pjob.Status = "failed"
	default:
		pjob.Status = "passed"
		// Resolve job outputs
		pjob.Outputs = r.resolveJobOutputs(yjob.Outputs, mergedEnv)
	}

	return pjob
}

// ---------------------------------------------------------------------------
// GitLab CI — Run
// ---------------------------------------------------------------------------

// RunGitLab executes a GitLab CI YAML file locally.
func (r *PipelineRunner) RunGitLab(file string, job string, dryRun bool) (*PipelineResult, error) {
	r.mu.Lock()
	if r.status.Running {
		r.mu.Unlock()
		return nil, fmt.Errorf("a pipeline is already running; call Stop() first")
	}
	r.mu.Unlock()

	if err := r.checkDiskSpace(); err != nil {
		return nil, err
	}

	resolvedFile, err := resolveGitLabFile(file)
	if err != nil {
		return nil, err
	}

	ci, err := parseGitLabCI(resolvedFile)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", resolvedFile, err)
	}

	runID := fmt.Sprintf("%d", time.Now().UnixMilli())
	artifactsDir := filepath.Join(r.cfg.ArtifactsDir, runID)
	repoRoot := filepath.Dir(resolvedFile)

	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.cancel = cancel
	r.jobOutputs = map[string]map[string]string{}
	result := &PipelineResult{
		File:         resolvedFile,
		Name:         filepath.Base(resolvedFile),
		Status:       "running",
		StartedAt:    time.Now(),
		CIFormat:     "gitlab",
		Hardware:     r.hw,
		ArtifactsDir: artifactsDir,
		RunID:        runID,
	}
	r.status.Running = true
	r.status.Current = result
	r.mu.Unlock()

	defer func() {
		cancel()
		r.mu.Lock()
		r.status.Running = false
		r.status.Last = result
		r.status.Current = nil
		r.cancel = nil
		r.mu.Unlock()
	}()

	if r.cfg.CancelCloudCI && !dryRun {
		if err2 := r.CancelCloudCI("gitlab"); err2 == nil {
			result.CloudCancelled = true
		}
	}

	start := result.StartedAt

	// Order jobs by stages
	orderedKeys := orderGitLabJobs(ci, job)

	completedJobs := map[string]string{}
	cancelled := false

	maxParallel := r.cfg.MaxParallelJobs
	if maxParallel <= 0 {
		maxParallel = r.hw.MaxParallel
	}
	if maxParallel < 1 {
		maxParallel = 1
	}

	// Group by stage for parallel execution within stage
	stageGroups := groupByStage(orderedKeys, ci)
	var resultMu sync.Mutex

	for _, group := range stageGroups {
		if cancelled {
			for _, key := range group {
				pjob := PipelineJob{Name: key, Status: "skipped"}
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()
				completedJobs[key] = "skipped"
			}
			continue
		}

		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		groupFailed := false
		var groupMu sync.Mutex

		for _, key := range group {
			key := key
			glJob := ci.Jobs[key]

			select {
			case <-ctx.Done():
				pjob := PipelineJob{Name: key, Status: "skipped"}
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()
				completedJobs[key] = "skipped"
				groupFailed = true
				continue
			default:
			}

			// Check needs
			skipJob := false
			for _, need := range normalizeNeeds(glJob.Needs) {
				if completedJobs[need] == "failed" {
					skipJob = true
					break
				}
			}
			if skipJob {
				pjob := PipelineJob{Name: key, Status: "skipped"}
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()
				completedJobs[key] = "skipped"
				continue
			}

			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				pjob := r.executeGitLabJob(ctx, key, glJob, ci.Variables, repoRoot, artifactsDir, runID, dryRun)
				resultMu.Lock()
				result.Jobs = append(result.Jobs, pjob)
				resultMu.Unlock()

				groupMu.Lock()
				completedJobs[key] = pjob.Status
				if pjob.Status == "failed" && !glJob.AllowFailure {
					groupFailed = true
				}
				groupMu.Unlock()
			}()
		}

		wg.Wait()

		if groupFailed {
			cancelled = true
		}
	}

	result.Duration = time.Since(start)
	result.Status = deriveOverallStatus(ctx, result.Jobs)

	if r.cfg.ReportToCloud && !dryRun {
		_ = r.ReportStatus("gitlab", result)
	}

	return result, nil
}

func (r *PipelineRunner) executeGitLabJob(
	ctx context.Context,
	key string,
	glJob gitlabJob,
	globalVars map[string]string,
	repoRoot, artifactsDir, runID string,
	dryRun bool,
) PipelineJob {
	pjob := PipelineJob{
		Name:   key,
		RunsOn: glJob.Image,
		Status: "running",
	}

	// Merge environment
	mergedEnv := mergeEnvMaps(globalVars, glJob.Variables)
	mergedEnv = r.resolveSecrets(mergedEnv)

	jobStart := time.Now()

	// Restore cache
	if glJob.Cache != nil && glJob.Cache.Key != "" {
		_ = r.cacheRestore(glJob.Cache.Key, glJob.Cache.Paths)
	}

	var scripts []string
	scripts = append(scripts, glJob.BeforeScript...)
	scripts = append(scripts, glJob.Script...)

	jobFailed := false
	jobCancelled := false

	// before_script
	for _, script := range glJob.BeforeScript {
		if jobCancelled {
			break
		}
		step := r.runShellStep(ctx, "before_script", script, mergedEnv, repoRoot, dryRun)
		pjob.Steps = append(pjob.Steps, step)
		if step.Status == "failed" {
			jobFailed = true
			break
		}
		if step.Status == "cancelled" {
			jobCancelled = true
		}
	}

	// script
	if !jobFailed && !jobCancelled {
		for _, script := range glJob.Script {
			if jobCancelled {
				break
			}
			step := r.runShellStep(ctx, "script", script, mergedEnv, repoRoot, dryRun)
			pjob.Steps = append(pjob.Steps, step)
			if step.Status == "failed" {
				jobFailed = true
				break
			}
			if step.Status == "cancelled" {
				jobCancelled = true
			}
		}
	}

	// after_script — always runs
	for _, script := range glJob.AfterScript {
		step := r.runShellStep(ctx, "after_script", script, mergedEnv, repoRoot, dryRun)
		pjob.Steps = append(pjob.Steps, step)
		_ = scripts // used above
	}

	// Save cache
	if glJob.Cache != nil && glJob.Cache.Key != "" && !jobFailed {
		_ = r.cacheSave(glJob.Cache.Key, glJob.Cache.Paths)
	}

	// Save artifacts
	if glJob.Artifacts != nil && len(glJob.Artifacts.Paths) > 0 && !jobFailed {
		_ = r.saveArtifacts(glJob.Artifacts.Paths, repoRoot, artifactsDir)
	}

	pjob.Duration = time.Since(jobStart)
	switch {
	case jobCancelled:
		pjob.Status = "cancelled"
	case jobFailed:
		if glJob.AllowFailure {
			pjob.Status = "passed"
		} else {
			pjob.Status = "failed"
		}
	default:
		pjob.Status = "passed"
	}

	return pjob
}

func (r *PipelineRunner) runShellStep(
	ctx context.Context,
	name, script string,
	env map[string]string,
	workDir string,
	dryRun bool,
) PipelineStep {
	step := PipelineStep{Name: name + ": " + truncatePipeline(script, 60), Status: "running"}
	start := time.Now()

	if dryRun {
		step.Output = fmt.Sprintf("[dry-run] %s", script)
		step.Status = "passed"
		step.Duration = time.Since(start)
		return step
	}

	envSlice := buildEnvSlice(env)
	var buf bytes.Buffer
	cmd := osexec.CommandContext(ctx, "sh", "-c", script)
	cmd.Dir = workDir
	cmd.Env = envSlice
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	step.Output = buf.String()
	step.Duration = time.Since(start)

	select {
	case <-ctx.Done():
		step.Status = "cancelled"
	default:
		if err != nil {
			step.Status = "failed"
			step.Error = err.Error()
		} else {
			step.Status = "passed"
		}
	}
	return step
}

// ---------------------------------------------------------------------------
// Step execution (GitHub Actions)
// ---------------------------------------------------------------------------

func (r *PipelineRunner) executeStep(
	ctx context.Context,
	ystep yamlStep,
	mergedEnv map[string]string,
	wf *yamlWorkflow,
	artifactsDir, runID string,
	dryRun bool,
) PipelineStep {
	step := PipelineStep{
		Name:            ystep.Name,
		Run:             ystep.Run,
		Uses:            ystep.Uses,
		With:            ystep.With,
		Env:             ystep.Env,
		If:              ystep.If,
		Status:          "pending",
		ContinueOnError: ystep.ContinueOnError,
		TimeoutMin:      ystep.TimeoutMin,
		WorkingDir:      ystep.WorkingDirectory,
	}

	// Auto-name
	if step.Name == "" {
		if step.Run != "" {
			step.Name = truncatePipeline(strings.TrimSpace(step.Run), 60)
		} else if step.Uses != "" {
			step.Name = step.Uses
		} else {
			step.Name = "(unnamed step)"
		}
	}

	// Evaluate if condition
	if ystep.If != "" && !r.evalConditionCtx(ystep.If, mergedEnv, nil) {
		step.Status = "skipped"
		return step
	}

	select {
	case <-ctx.Done():
		step.Status = "cancelled"
		return step
	default:
	}

	// Apply step timeout
	stepCtx := ctx
	var stepCancel context.CancelFunc
	if ystep.TimeoutMin > 0 {
		stepCtx, stepCancel = context.WithTimeout(ctx, time.Duration(ystep.TimeoutMin)*time.Minute)
		defer stepCancel()
	}

	start := time.Now()
	step.Status = "running"

	if ystep.Uses != "" {
		step = r.handleUsesStep(stepCtx, step, mergedEnv, artifactsDir, runID, dryRun)
	} else if ystep.Run != "" {
		step = r.handleRunStep(stepCtx, ystep, step, mergedEnv, dryRun)
	} else {
		step.Status = "passed"
	}

	step.Duration = time.Since(start)
	return step
}

func (r *PipelineRunner) handleRunStep(
	ctx context.Context,
	ystep yamlStep,
	step PipelineStep,
	mergedEnv map[string]string,
	dryRun bool,
) PipelineStep {
	// Determine working directory
	workDir := ""
	if ystep.WorkingDirectory != "" {
		workDir = ystep.WorkingDirectory
		if !filepath.IsAbs(workDir) {
			// Resolve relative to cwd (which is already the repo root at this point)
			if wd, err := os.Getwd(); err == nil {
				workDir = filepath.Join(wd, workDir)
			}
		}
	} else {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			workDir = "."
		}
	}

	if dryRun {
		step.Output = fmt.Sprintf("[dry-run] would run in %s:\n%s", workDir, ystep.Run)
		step.Status = "passed"
		return step
	}

	// Merge step-level env on top
	effectiveEnv := mergeEnvMaps(mergedEnv, ystep.Env)
	envSlice := buildEnvSlice(effectiveEnv)

	var buf bytes.Buffer
	cmd := osexec.CommandContext(ctx, "sh", "-c", ystep.Run)
	cmd.Dir = workDir
	cmd.Env = envSlice
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	step.Output = buf.String()

	select {
	case <-ctx.Done():
		step.Status = "cancelled"
		step.Error = "cancelled by timeout or stop"
	default:
		if err != nil {
			step.Status = "failed"
			step.Error = err.Error()
		} else {
			step.Status = "passed"
		}
	}
	return step
}

// handleUsesStep maps common GitHub Actions `uses:` to local equivalents.
func (r *PipelineRunner) handleUsesStep(
	ctx context.Context,
	step PipelineStep,
	env map[string]string,
	artifactsDir, runID string,
	dryRun bool,
) PipelineStep {
	uses := step.Uses

	// ── actions/checkout ───────────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/checkout@") {
		step.Output = "skipped: already in local workspace"
		step.Status = "skipped"
		return step
	}

	// ── actions/setup-node ─────────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/setup-node@") {
		return r.setupToolStep(ctx, step, "node", "Node.js", step.With["node-version"], "nvm", dryRun)
	}

	// ── actions/setup-go ──────────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/setup-go@") {
		return r.setupToolStep(ctx, step, "go", "Go", step.With["go-version"], "goenv", dryRun)
	}

	// ── actions/setup-python ──────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/setup-python@") {
		return r.setupToolStep(ctx, step, "python3", "Python", step.With["python-version"], "pyenv", dryRun)
	}

	// ── actions/setup-java ────────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/setup-java@") {
		return r.setupToolStep(ctx, step, "java", "Java", step.With["java-version"], "JAVA_HOME", dryRun)
	}

	// ── actions/cache ─────────────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/cache@") {
		cacheKey := step.With["key"]
		pathsRaw := step.With["path"]
		paths := strings.Fields(pathsRaw)
		if dryRun {
			step.Output = fmt.Sprintf("[dry-run] would restore cache key=%s paths=%v", cacheKey, paths)
			step.Status = "passed"
			return step
		}
		err := r.cacheRestore(cacheKey, paths)
		if err != nil {
			step.Output = fmt.Sprintf("cache miss for key %q (will save on post)", cacheKey)
		} else {
			step.Output = fmt.Sprintf("cache restored for key %q", cacheKey)
		}
		step.Status = "passed"
		return step
	}

	// ── actions/upload-artifact ───────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/upload-artifact@") {
		name := step.With["name"]
		path := step.With["path"]
		if dryRun {
			step.Output = fmt.Sprintf("[dry-run] would upload artifact %q from %s", name, path)
			step.Status = "passed"
			return step
		}
		destDir := filepath.Join(artifactsDir, name)
		if err := os.MkdirAll(destDir, 0o755); err == nil {
			var buf bytes.Buffer
			cmd := osexec.CommandContext(ctx, "cp", "-r", path, destDir+"/")
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			if err := cmd.Run(); err != nil {
				step.Output = fmt.Sprintf("artifact upload failed: %s\n%s", err, buf.String())
				step.Status = "failed"
				return step
			}
			step.Output = fmt.Sprintf("artifact %q saved to %s", name, destDir)
		}
		step.Status = "passed"
		return step
	}

	// ── actions/download-artifact ─────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/download-artifact@") {
		name := step.With["name"]
		dest := step.With["path"]
		if dest == "" {
			dest = name
		}
		srcDir := filepath.Join(artifactsDir, name)
		if dryRun {
			step.Output = fmt.Sprintf("[dry-run] would download artifact %q to %s", name, dest)
			step.Status = "passed"
			return step
		}
		if err := os.MkdirAll(dest, 0o755); err == nil {
			cmd := osexec.CommandContext(ctx, "cp", "-r", srcDir+"/.", dest)
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			if err := cmd.Run(); err != nil {
				step.Output = fmt.Sprintf("artifact download failed: %s\n%s", err, buf.String())
				step.Status = "failed"
				return step
			}
			step.Output = fmt.Sprintf("artifact %q downloaded to %s", name, dest)
		}
		step.Status = "passed"
		return step
	}

	// ── docker/login-action ───────────────────────────────────────────────
	if strings.HasPrefix(uses, "docker/login-action@") {
		registry := step.With["registry"]
		username := step.With["username"]
		password := step.With["password"]
		if dryRun {
			step.Output = fmt.Sprintf("[dry-run] would docker login to %s", registry)
			step.Status = "passed"
			return step
		}
		args := []string{"login"}
		if registry != "" {
			args = append(args, registry)
		}
		if username != "" {
			args = append(args, "-u", username)
		}
		if password != "" {
			args = append(args, "--password-stdin")
		}
		cmd := osexec.CommandContext(ctx, "docker", args...)
		if password != "" {
			cmd.Stdin = strings.NewReader(password)
		}
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			step.Output = buf.String()
			step.Status = "failed"
			step.Error = err.Error()
			return step
		}
		step.Output = "docker login succeeded"
		step.Status = "passed"
		return step
	}

	// ── docker/build-push-action ──────────────────────────────────────────
	if strings.HasPrefix(uses, "docker/build-push-action@") {
		context_ := step.With["context"]
		if context_ == "" {
			context_ = "."
		}
		tags := step.With["tags"]
		push := step.With["push"] == "true"
		if dryRun {
			step.Output = fmt.Sprintf("[dry-run] would docker build %s tags=%s push=%v", context_, tags, push)
			step.Status = "passed"
			return step
		}
		args := []string{"build", context_}
		for _, tag := range strings.Fields(tags) {
			args = append(args, "-t", tag)
		}
		var buf bytes.Buffer
		cmd := osexec.CommandContext(ctx, "docker", args...)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			step.Output = buf.String()
			step.Status = "failed"
			step.Error = err.Error()
			return step
		}
		step.Output = buf.String()
		if push && tags != "" {
			for _, tag := range strings.Fields(tags) {
				pushCmd := osexec.CommandContext(ctx, "docker", "push", tag)
				var pushBuf bytes.Buffer
				pushCmd.Stdout = &pushBuf
				pushCmd.Stderr = &pushBuf
				_ = pushCmd.Run()
				step.Output += "\n" + pushBuf.String()
			}
		}
		step.Status = "passed"
		return step
	}

	// ── peaceiris/actions-gh-pages ────────────────────────────────────────
	if strings.HasPrefix(uses, "peaceiris/actions-gh-pages@") {
		publishDir := step.With["publish_dir"]
		if publishDir == "" {
			publishDir = "public"
		}
		if dryRun {
			step.Output = fmt.Sprintf("[dry-run] would push %s to gh-pages branch", publishDir)
			step.Status = "passed"
			return step
		}
		var buf bytes.Buffer
		cmd := osexec.CommandContext(ctx, "git", "subtree", "push", "--prefix", publishDir, "origin", "gh-pages")
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			step.Output = buf.String()
			step.Status = "failed"
			step.Error = err.Error()
			return step
		}
		step.Output = fmt.Sprintf("pushed %s to gh-pages", publishDir)
		step.Status = "passed"
		return step
	}

	// ── actions/github-script ─────────────────────────────────────────────
	if strings.HasPrefix(uses, "actions/github-script@") {
		step.Output = "skipped: GitHub Script requires GitHub Actions environment"
		step.Status = "skipped"
		return step
	}

	// ── Local composite action (action.yml in repo) ───────────────────────
	if !strings.Contains(uses, "@") || strings.HasPrefix(uses, "./") {
		actionPath := uses
		if strings.HasPrefix(actionPath, "./") {
			if wd, err := os.Getwd(); err == nil {
				actionPath = filepath.Join(wd, actionPath[2:], "action.yml")
			}
		}
		if _, err := os.Stat(actionPath); err == nil {
			step.Output = fmt.Sprintf("composite action %s found but execution not fully implemented", uses)
			step.Status = "skipped"
			return step
		}
	}

	// Unknown action — skip with warning
	step.Output = fmt.Sprintf("warning: action %q is not supported locally — step skipped", uses)
	step.Status = "skipped"
	return step
}

// setupToolStep checks if a tool is installed, optionally using a version manager.
func (r *PipelineRunner) setupToolStep(
	ctx context.Context,
	step PipelineStep,
	binary, toolName, version, versionManager string,
	dryRun bool,
) PipelineStep {
	if dryRun {
		step.Output = fmt.Sprintf("[dry-run] would verify %s %s is installed", toolName, version)
		step.Status = "passed"
		return step
	}

	toolPath, err := osexec.LookPath(binary)
	if err != nil {
		step.Output = fmt.Sprintf("warning: %s (%s) not found in PATH — step skipped", toolName, binary)
		step.Status = "skipped"
		return step
	}

	// Check version if requested
	if version != "" {
		var buf bytes.Buffer
		versionCmd := osexec.CommandContext(ctx, binary, "--version")
		versionCmd.Stdout = &buf
		versionCmd.Stderr = &buf
		if versionCmd.Run() == nil {
			installed := strings.TrimSpace(buf.String())
			if !strings.Contains(installed, version) {
				step.Output = fmt.Sprintf(
					"%s found at %s (version: %s) but requested %s — continuing anyway (use %s to manage versions)",
					toolName, toolPath, installed, version, versionManager,
				)
				step.Status = "passed" // warn but don't fail
				return step
			}
		}
	}

	step.Output = fmt.Sprintf("%s is available at %s", toolName, toolPath)
	step.Status = "passed"
	return step
}

// ---------------------------------------------------------------------------
// Matrix expansion
// ---------------------------------------------------------------------------

// expandMatrix returns all combinations from a MatrixConfig.
func (r *PipelineRunner) expandMatrix(matrix MatrixConfig) []map[string]string {
	if len(matrix.Values) == 0 && len(matrix.Include) == 0 {
		return nil
	}

	// Build axis keys and values in deterministic order
	var keys []string
	for k := range matrix.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	combos := []map[string]string{{}}
	for _, k := range keys {
		vals := matrix.Values[k]
		var expanded []map[string]string
		for _, combo := range combos {
			for _, v := range vals {
				newCombo := make(map[string]string, len(combo)+1)
				for ck, cv := range combo {
					newCombo[ck] = cv
				}
				newCombo[k] = v
				expanded = append(expanded, newCombo)
			}
		}
		combos = expanded
	}

	// Apply excludes
	if len(matrix.Exclude) > 0 {
		var filtered []map[string]string
		for _, combo := range combos {
			excluded := false
			for _, excl := range matrix.Exclude {
				if mapContains(combo, excl) {
					excluded = true
					break
				}
			}
			if !excluded {
				filtered = append(filtered, combo)
			}
		}
		combos = filtered
	}

	// Apply includes (add or augment)
	for _, incl := range matrix.Include {
		merged := make(map[string]string, len(incl))
		for k, v := range incl {
			merged[k] = v
		}
		combos = append(combos, merged)
	}

	return combos
}

func (r *PipelineRunner) expandMatrixFromYAML(strategy *yamlStrategy) []map[string]string {
	if strategy == nil {
		return nil
	}
	// Decode the raw matrix node
	rawMap := map[string]interface{}{}
	if err := strategy.Matrix.Decode(&rawMap); err != nil {
		return nil
	}

	mc := MatrixConfig{
		Values: map[string][]string{},
	}

	for k, v := range rawMap {
		switch k {
		case "include":
			if items, ok := v.([]interface{}); ok {
				for _, item := range items {
					if m, ok := item.(map[string]interface{}); ok {
						row := make(map[string]string, len(m))
						for mk, mv := range m {
							row[mk] = fmt.Sprintf("%v", mv)
						}
						mc.Include = append(mc.Include, row)
					}
				}
			}
		case "exclude":
			if items, ok := v.([]interface{}); ok {
				for _, item := range items {
					if m, ok := item.(map[string]interface{}); ok {
						row := make(map[string]string, len(m))
						for mk, mv := range m {
							row[mk] = fmt.Sprintf("%v", mv)
						}
						mc.Exclude = append(mc.Exclude, row)
					}
				}
			}
		default:
			// Matrix axis
			switch vals := v.(type) {
			case []interface{}:
				for _, val := range vals {
					mc.Values[k] = append(mc.Values[k], fmt.Sprintf("%v", val))
				}
			case interface{}:
				mc.Values[k] = []string{fmt.Sprintf("%v", vals)}
			}
		}
	}

	return r.expandMatrix(mc)
}

func mapContains(combo, subset map[string]string) bool {
	for k, v := range subset {
		if combo[k] != v {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Docker services
// ---------------------------------------------------------------------------

// startServices starts Docker sidecar containers and returns a cleanup func and the container names.
func (r *PipelineRunner) startServices(services map[string]ServiceConfig) (cleanup func(), names []string, err error) {
	var started []string
	for name, svc := range services {
		containerName := fmt.Sprintf("yaver-ci-%s-%d", name, time.Now().UnixMilli())
		args := []string{"run", "-d", "--name", containerName}

		for _, port := range svc.Ports {
			args = append(args, "-p", port)
		}
		for k, v := range svc.Env {
			args = append(args, "-e", k+"="+v)
		}
		if svc.Options != "" {
			args = append(args, strings.Fields(svc.Options)...)
		}
		args = append(args, svc.Image)

		out, runErr := osexec.Command("docker", args...).CombinedOutput()
		if runErr != nil {
			// Clean up already-started containers
			for _, c := range started {
				_ = osexec.Command("docker", "rm", "-f", c).Run()
			}
			return nil, nil, fmt.Errorf("start service %s: %w\n%s", name, runErr, out)
		}
		started = append(started, containerName)
		names = append(names, name)
	}

	cleanup = func() {
		for _, c := range started {
			_ = osexec.Command("docker", "rm", "-f", c).Run()
		}
	}
	return cleanup, names, nil
}

// ---------------------------------------------------------------------------
// Secrets resolution
// ---------------------------------------------------------------------------

// resolveSecrets injects secrets into an env map, substituting ${{ secrets.X }} references.
func (r *PipelineRunner) resolveSecrets(env map[string]string) map[string]string {
	r.mu.RLock()
	secrets := r.secrets
	r.mu.RUnlock()

	resolved := make(map[string]string, len(env))
	for k, v := range env {
		// Replace ${{ secrets.FOO }} with the actual secret value
		if strings.Contains(v, "${{ secrets.") {
			for secName, secVal := range secrets {
				placeholder := "${{ secrets." + secName + " }}"
				v = strings.ReplaceAll(v, placeholder, secVal)
			}
		}
		resolved[k] = v
	}

	// Also inject secrets as top-level env vars that aren't already set
	for k, v := range secrets {
		if _, exists := resolved[k]; !exists {
			resolved[k] = v
		}
	}
	return resolved
}

// ---------------------------------------------------------------------------
// Cache management
// ---------------------------------------------------------------------------

// cacheRestore copies cached files from the cache dir to their original paths.
func (r *PipelineRunner) cacheRestore(key string, paths []string) error {
	cacheDir := filepath.Join(r.cfg.CacheDir, sanitizeKey(key))
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return fmt.Errorf("cache miss: %s", key)
	}
	for _, p := range paths {
		src := filepath.Join(cacheDir, filepath.Base(p))
		if _, err := os.Stat(src); err == nil {
			_ = osexec.Command("cp", "-r", src, p).Run()
		}
	}
	return nil
}

// cacheSave copies files to the cache dir.
func (r *PipelineRunner) cacheSave(key string, paths []string) error {
	cacheDir := filepath.Join(r.cfg.CacheDir, sanitizeKey(key))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			_ = osexec.Command("cp", "-r", p, cacheDir+"/").Run()
		}
	}
	return nil
}

// hashFiles returns a SHA-256 hash of the contents of files matching the given glob patterns.
func (r *PipelineRunner) hashFiles(patterns ...string) string {
	h := sha256.New()
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		sort.Strings(matches)
		for _, match := range matches {
			data, err := os.ReadFile(match)
			if err == nil {
				h.Write(data)
			}
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func sanitizeKey(key string) string {
	r := strings.NewReplacer("/", "_", ":", "_", " ", "_", "$", "", "{", "", "}", "")
	return r.Replace(key)
}

// saveArtifacts copies artifact paths to the artifacts directory.
func (r *PipelineRunner) saveArtifacts(paths []string, repoRoot, artifactsDir string) error {
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return err
	}
	for _, p := range paths {
		src := p
		if !filepath.IsAbs(src) {
			src = filepath.Join(repoRoot, p)
		}
		_ = osexec.Command("cp", "-r", src, artifactsDir+"/").Run()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Cloud CI cancellation and status reporting
// ---------------------------------------------------------------------------

// CancelCloudCI cancels any in-progress cloud CI runs for the current commit.
func (r *PipelineRunner) CancelCloudCI(provider string) error {
	switch provider {
	case "github":
		// Try `gh run cancel` for the current branch's latest run
		sha, err := osexec.Command("git", "rev-parse", "HEAD").Output()
		if err != nil {
			return err
		}
		commitSHA := strings.TrimSpace(string(sha))
		// List runs for this commit and cancel them
		listOut, err := osexec.Command("gh", "run", "list",
			"--commit", commitSHA,
			"--status", "in_progress",
			"--json", "databaseId",
			"--jq", ".[].databaseId",
		).Output()
		if err != nil {
			return err
		}
		for _, idStr := range strings.Fields(string(listOut)) {
			_ = osexec.Command("gh", "run", "cancel", idStr).Run()
		}
		return nil

	case "gitlab":
		// Try `glab ci cancel` for current branch
		branch, _ := osexec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		branchName := strings.TrimSpace(string(branch))
		return osexec.Command("glab", "ci", "cancel", "-b", branchName).Run()

	default:
		return fmt.Errorf("unsupported CI provider: %s", provider)
	}
}

// ReportStatus pushes the local pipeline result as a commit status to GitHub or GitLab.
func (r *PipelineRunner) ReportStatus(provider string, result *PipelineResult) error {
	sha, err := osexec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	commitSHA := strings.TrimSpace(string(sha))

	state := "success"
	if result.Status == "failed" {
		state = "failure"
	} else if result.Status == "cancelled" {
		state = "cancelled"
	}

	switch provider {
	case "github":
		// Use gh api to create a check run
		payload := fmt.Sprintf(`{"name":"yaver-local-ci","head_sha":"%s","status":"completed","conclusion":"%s","output":{"title":"Local CI","summary":"Ran %d jobs in %s"}}`,
			commitSHA, state, len(result.Jobs), result.Duration.Round(time.Second))
		return osexec.Command("gh", "api",
			"--method", "POST",
			"/repos/{owner}/{repo}/check-runs",
			"--input", "-",
			"--silent",
		).Run()
		_ = payload // used as stdin in a real implementation
	case "gitlab":
		return osexec.Command("glab", "api",
			"POST", "/projects/:id/statuses/"+commitSHA,
			"-f", "state="+state,
			"-f", "name=yaver-local-ci",
			"-f", fmt.Sprintf("description=Ran %d jobs in %s", len(result.Jobs), result.Duration.Round(time.Second)),
		).Run()
	}
	return fmt.Errorf("unsupported provider: %s", provider)
}

// ---------------------------------------------------------------------------
// Disk space checks
// ---------------------------------------------------------------------------

func (r *PipelineRunner) checkDiskSpace() error {
	diskFree := r.hw.DiskFree
	const warnThreshold = 10 * 1024 * 1024 * 1024  // 10 GB
	const failThreshold = 2 * 1024 * 1024 * 1024   // 2 GB

	if diskFree > 0 && diskFree < failThreshold {
		return fmt.Errorf("insufficient disk space: %s free (need at least 2 GB)", humanBytes(diskFree))
	}
	if diskFree > 0 && diskFree < warnThreshold {
		fmt.Fprintf(os.Stderr, "WARNING: low disk space: %s free\n", humanBytes(diskFree))
	}
	return nil
}

// ---------------------------------------------------------------------------
// YAML parsing — GitHub Actions
// ---------------------------------------------------------------------------

func parseWorkflow(file string) (*yamlWorkflow, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var wf yamlWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func parseWorkflowInfo(file string) (*PipelineInfo, error) {
	wf, err := parseWorkflow(file)
	if err != nil {
		return nil, err
	}
	info := &PipelineInfo{
		File:   file,
		Name:   wf.Name,
		Format: "github",
	}
	if info.Name == "" {
		info.Name = filepath.Base(file)
	}
	for key := range wf.Jobs {
		info.Jobs = append(info.Jobs, key)
	}
	sort.Strings(info.Jobs)
	info.Triggers = extractTriggers(&wf.On)
	return info, nil
}

func extractTriggers(node *yaml.Node) []string {
	if node == nil || node.Kind == 0 {
		return nil
	}
	var triggers []string
	switch node.Kind {
	case yaml.ScalarNode:
		triggers = append(triggers, node.Value)
	case yaml.SequenceNode:
		for _, child := range node.Content {
			triggers = append(triggers, child.Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content)-1; i += 2 {
			triggers = append(triggers, node.Content[i].Value)
		}
	}
	return triggers
}

// ---------------------------------------------------------------------------
// YAML parsing — GitLab CI
// ---------------------------------------------------------------------------

func parseGitLabCI(file string) (*gitlabCI, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// First pass: parse top-level known fields
	var top struct {
		Stages    []string          `yaml:"stages"`
		Variables map[string]string `yaml:"variables"`
	}
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, err
	}

	// Second pass: parse all keys as potential jobs
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	ci := &gitlabCI{
		Stages:    top.Stages,
		Variables: top.Variables,
		Jobs:      map[string]gitlabJob{},
	}

	// Reserved top-level keys
	reserved := map[string]bool{
		"stages": true, "variables": true, "image": true,
		"services": true, "before_script": true, "after_script": true,
		"cache": true, "include": true, "workflow": true,
		"default": true, "pages": true,
	}

	for key, node := range raw {
		if reserved[key] || strings.HasPrefix(key, ".") {
			continue
		}
		var job gitlabJob
		if err := node.Decode(&job); err != nil {
			continue
		}
		ci.Jobs[key] = job
	}

	return ci, nil
}

func parseGitLabInfo(file string) (*PipelineInfo, error) {
	ci, err := parseGitLabCI(file)
	if err != nil {
		return nil, err
	}
	info := &PipelineInfo{
		File:     file,
		Name:     filepath.Base(file),
		Format:   "gitlab",
		Triggers: []string{"push"},
	}
	for key := range ci.Jobs {
		info.Jobs = append(info.Jobs, key)
	}
	sort.Strings(info.Jobs)
	return info, nil
}

func resolveGitLabFile(file string) (string, error) {
	if file != "" {
		if _, err := os.Stat(file); err != nil {
			return "", fmt.Errorf("GitLab CI file not found: %s", file)
		}
		return file, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, name := range []string{".gitlab-ci.yml", ".gitlab-ci.yaml"} {
		f := filepath.Join(wd, name)
		if _, err := os.Stat(f); err == nil {
			return f, nil
		}
	}
	return "", fmt.Errorf("no .gitlab-ci.yml found in %s", wd)
}

// orderGitLabJobs returns job keys ordered by stage, then by name.
func orderGitLabJobs(ci *gitlabCI, filterJob string) []string {
	if filterJob != "" {
		return []string{filterJob}
	}

	// Build stage index
	stageIdx := map[string]int{}
	for i, s := range ci.Stages {
		stageIdx[s] = i
	}

	type jobEntry struct {
		key   string
		stage int
	}
	var entries []jobEntry
	for key, job := range ci.Jobs {
		idx, ok := stageIdx[job.Stage]
		if !ok {
			idx = len(ci.Stages) // unknown stage goes last
		}
		entries = append(entries, jobEntry{key, idx})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].stage != entries[j].stage {
			return entries[i].stage < entries[j].stage
		}
		return entries[i].key < entries[j].key
	})

	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.key
	}
	return keys
}

// groupByStage groups ordered job keys by their stage for parallel execution.
func groupByStage(orderedKeys []string, ci *gitlabCI) [][]string {
	var groups [][]string
	var current []string
	var currentStage string

	for _, key := range orderedKeys {
		stage := ci.Jobs[key].Stage
		if stage == "" {
			stage = "test" // GitLab default
		}
		if stage != currentStage {
			if len(current) > 0 {
				groups = append(groups, current)
			}
			current = []string{key}
			currentStage = stage
		} else {
			current = append(current, key)
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// tryAutoDetect tries to find a GitLab CI file when no GitHub Actions file exists.
func (r *PipelineRunner) tryAutoDetect(job string, dryRun bool) (*PipelineResult, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("no workflow files found")
	}
	for _, name := range []string{".gitlab-ci.yml", ".gitlab-ci.yaml"} {
		f := filepath.Join(wd, name)
		if _, err := os.Stat(f); err == nil {
			return r.RunGitLab(f, job, dryRun)
		}
	}
	return nil, fmt.Errorf("no workflow files found in %s (checked .github/workflows/, .gitlab-ci.yml)", wd)
}

// ---------------------------------------------------------------------------
// Job scheduling utilities
// ---------------------------------------------------------------------------

// buildJobLayers converts a topologically ordered job list into layers
// where jobs in the same layer can run in parallel (no cross-layer dependencies).
func buildJobLayers(orderedKeys []string, jobs map[string]yamlJob) [][]string {
	layers := [][]string{}
	assigned := map[string]int{} // jobKey → layer index

	for _, key := range orderedKeys {
		yjob := jobs[key]
		needs := normalizeNeeds(yjob.Needs)

		// This job's layer = max(layer of all needs) + 1
		maxNeedLayer := -1
		for _, need := range needs {
			if l, ok := assigned[need]; ok && l > maxNeedLayer {
				maxNeedLayer = l
			}
		}
		layer := maxNeedLayer + 1

		for len(layers) <= layer {
			layers = append(layers, []string{})
		}
		layers[layer] = append(layers[layer], key)
		assigned[key] = layer
	}
	return layers
}

// orderJobs returns job keys in topological order, filtering by filterJob if set.
func orderJobs(jobs map[string]yamlJob, filterJob string) ([]string, error) {
	if filterJob != "" {
		if _, ok := jobs[filterJob]; !ok {
			return nil, fmt.Errorf("job %q not found in workflow", filterJob)
		}
		needed := collectNeeds(filterJob, jobs)
		return topoSort(needed, jobs)
	}
	all := make(map[string]bool, len(jobs))
	for k := range jobs {
		all[k] = true
	}
	return topoSort(all, jobs)
}

func collectNeeds(job string, jobs map[string]yamlJob) map[string]bool {
	visited := map[string]bool{}
	var visit func(string)
	visit = func(j string) {
		if visited[j] {
			return
		}
		visited[j] = true
		for _, need := range normalizeNeeds(jobs[j].Needs) {
			visit(need)
		}
	}
	visit(job)
	return visited
}

func topoSort(keys map[string]bool, jobs map[string]yamlJob) ([]string, error) {
	inDegree := map[string]int{}
	for k := range keys {
		inDegree[k] = 0
	}
	for k := range keys {
		for _, need := range normalizeNeeds(jobs[k].Needs) {
			if keys[need] {
				inDegree[k]++
			}
		}
	}

	var queue []string
	for k, d := range inDegree {
		if d == 0 {
			queue = append(queue, k)
		}
	}
	sort.Strings(queue)

	var ordered []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		ordered = append(ordered, cur)

		var next []string
		for k := range keys {
			for _, need := range normalizeNeeds(jobs[k].Needs) {
				if need == cur {
					inDegree[k]--
					if inDegree[k] == 0 {
						next = append(next, k)
					}
				}
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
	}

	if len(ordered) != len(keys) {
		return nil, fmt.Errorf("circular dependency detected in job needs")
	}
	return ordered, nil
}

// ---------------------------------------------------------------------------
// Condition evaluation
// ---------------------------------------------------------------------------

// evalConditionCtx evaluates `if:` expressions with access to job outputs.
func (r *PipelineRunner) evalConditionCtx(expr string, env map[string]string, jobOutputs map[string]map[string]string) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "true" || expr == "always()" || expr == "success()" {
		return true
	}
	if expr == "false" || expr == "never()" || expr == "cancelled()" {
		return false
	}
	if expr == "failure()" {
		return false
	}

	// ${{ github.event_name == 'push' }} → true locally
	if strings.Contains(expr, "github.event_name") {
		return true
	}

	// needs.X.outputs.Y == 'value' — resolve from runner's job output cache
	if strings.Contains(expr, "needs.") && strings.Contains(expr, ".outputs.") {
		r.mu.RLock()
		outputs := r.jobOutputs
		r.mu.RUnlock()
		// Simple resolution: if the referenced output exists, evaluate == comparisons
		for jobKey, jobOut := range outputs {
			for outKey, outVal := range jobOut {
				ref := fmt.Sprintf("needs.%s.outputs.%s", jobKey, outKey)
				if strings.Contains(expr, ref) {
					// Check for equality expression
					eqExpr := fmt.Sprintf("%s == '%s'", ref, outVal)
					if strings.Contains(expr, eqExpr) {
						return true
					}
					neExpr := fmt.Sprintf("%s != '%s'", ref, outVal)
					if strings.Contains(expr, neExpr) {
						return false
					}
				}
			}
		}
		return true // default: run
	}

	// env.VAR == 'value'
	if strings.Contains(expr, "env.") && env != nil {
		for k, v := range env {
			eqExpr := fmt.Sprintf("env.%s == '%s'", k, v)
			if strings.Contains(expr, eqExpr) {
				return true
			}
		}
	}

	// runner.os == 'Linux' etc.
	if strings.Contains(expr, "runner.os") {
		osName := map[string]string{
			"linux":   "Linux",
			"darwin":  "macOS",
			"windows": "Windows",
		}[runtime.GOOS]
		if osName != "" && strings.Contains(expr, fmt.Sprintf("runner.os == '%s'", osName)) {
			return true
		}
		if strings.Contains(expr, "runner.os ==") {
			return false // different OS
		}
	}

	return true // default: run
}

// resolveJobOutputs substitutes output expressions like ${{ steps.X.outputs.Y }}.
func (r *PipelineRunner) resolveJobOutputs(outputs map[string]string, env map[string]string) map[string]string {
	if len(outputs) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(outputs))
	for k, v := range outputs {
		// Replace env var references
		for envK, envV := range env {
			v = strings.ReplaceAll(v, "${{ env."+envK+" }}", envV)
		}
		// Remove unresolved expressions
		resolved[k] = v
	}
	return resolved
}

// ---------------------------------------------------------------------------
// File / path utilities
// ---------------------------------------------------------------------------

func resolveWorkflowFile(file string) (string, error) {
	if file != "" {
		if _, err := os.Stat(file); err != nil {
			return "", fmt.Errorf("workflow file not found: %s", file)
		}
		return file, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(wd, ".github", "workflows")
	for _, ext := range []string{"*.yml", "*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(dir, ext))
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("no workflow files found in %s", dir)
}

func repoRootFromWorkflow(workflowFile string) string {
	dir := filepath.Dir(workflowFile)
	parent := filepath.Dir(dir)
	root := filepath.Dir(parent)
	if root == "." || root == "" {
		wd, _ := os.Getwd()
		return wd
	}
	return root
}

// ---------------------------------------------------------------------------
// Env helpers
// ---------------------------------------------------------------------------

func mergeEnvMaps(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func buildEnvSlice(env map[string]string) []string {
	// Start from the current process environment
	base := os.Environ()
	for k, v := range env {
		base = setEnvVar(base, k, v)
	}
	return base
}

func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func buildPipelineJob(key string, yjob yamlJob) PipelineJob {
	pjob := PipelineJob{
		Name:       yjob.Name,
		RunsOn:     yjob.RunsOn,
		If:         yjob.If,
		Needs:      normalizeNeeds(yjob.Needs),
		Status:     "pending",
		TimeoutMin: yjob.TimeoutMin,
	}
	if pjob.Name == "" {
		pjob.Name = key
	}
	return pjob
}

func normalizeNeeds(raw interface{}) []string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			switch s := item.(type) {
			case string:
				out = append(out, s)
			case map[string]interface{}:
				// GitLab DAG: {"job": "jobname"} or {"job": "jobname", "artifacts": false}
				if j, ok := s["job"].(string); ok {
					out = append(out, j)
				}
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

func deriveOverallStatus(ctx context.Context, jobs []PipelineJob) string {
	if ctx.Err() != nil {
		return "cancelled"
	}
	for _, pj := range jobs {
		if pj.Status == "failed" {
			return "failed"
		}
	}
	for _, pj := range jobs {
		if pj.Status == "cancelled" {
			return "cancelled"
		}
	}
	return "passed"
}

// ---------------------------------------------------------------------------
// String utilities
// ---------------------------------------------------------------------------

func truncatePipeline(s string, n int) string {
	lines := strings.SplitN(strings.TrimSpace(s), "\n", 2)
	first := lines[0]
	if len(first) > n {
		return first[:n] + "…"
	}
	return first
}

// ---------------------------------------------------------------------------
// Disk/FS scan for List
// ---------------------------------------------------------------------------

// walkCI scans dir for CI config files (used internally).
func walkCI(dir string) []string {
	var found []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// Ensure io and fs are used (imported for walkCI / saveArtifacts).
var _ = io.EOF
var _ = fs.ErrNotExist
