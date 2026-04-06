package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sandboxImage       = "yaver-sandbox"
	sandboxDockerfile  = "Dockerfile.sandbox"
	containerStopGrace = 5 * time.Second
)

// ContainerRunner executes tasks inside Docker containers for isolation.
// Used for both guest (security) and host (optional clean builds) tasks.
type ContainerRunner struct {
	mu          sync.Mutex
	imageReady  bool
	dockerPath  string
	agentDir    string // path to desktop/agent/ (for Dockerfile)

	// Cache mount paths (persisted across container runs for speed)
	cacheDirs []string
}

// NewContainerRunner creates a runner that uses Docker for task isolation.
func NewContainerRunner() *ContainerRunner {
	dockerPath, _ := exec.LookPath("docker")

	// Find agent dir (where Dockerfile.sandbox lives)
	agentDir := ""
	if exePath, err := os.Executable(); err == nil {
		agentDir = filepath.Dir(exePath)
	}
	// Fallback: check if Dockerfile.sandbox exists relative to cwd
	if agentDir == "" || !sandboxFileExists(filepath.Join(agentDir, sandboxDockerfile)) {
		if cwd, err := os.Getwd(); err == nil {
			agentDir = cwd
		}
	}

	return &ContainerRunner{
		dockerPath: dockerPath,
		agentDir:   agentDir,
		cacheDirs: []string{
			"npm-cache:/root/.npm",
			"gradle-cache:/root/.gradle",
			"cargo-cache:/root/.cargo/registry",
			"go-mod-cache:/root/go/pkg/mod",
		},
	}
}

// IsAvailable checks if Docker is installed and running.
func (cr *ContainerRunner) IsAvailable() bool {
	if cr.dockerPath == "" {
		return false
	}
	cmd := exec.Command(cr.dockerPath, "info")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// IsImageReady checks if the sandbox image has been built.
func (cr *ContainerRunner) IsImageReady() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.imageReady {
		return true
	}
	cmd := exec.Command(cr.dockerPath, "image", "inspect", sandboxImage)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() == nil {
		cr.imageReady = true
		return true
	}
	return false
}

// BuildImage builds the sandbox Docker image. Can take a few minutes the first time.
func (cr *ContainerRunner) BuildImage(ctx context.Context) error {
	dockerfile := filepath.Join(cr.agentDir, sandboxDockerfile)
	if !sandboxFileExists(dockerfile) {
		return fmt.Errorf("sandbox Dockerfile not found at %s", dockerfile)
	}

	log.Printf("[SANDBOX] Building image %s from %s ...", sandboxImage, dockerfile)
	cmd := exec.CommandContext(ctx, cr.dockerPath,
		"build",
		"-f", dockerfile,
		"-t", sandboxImage,
		cr.agentDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	cr.mu.Lock()
	cr.imageReady = true
	cr.mu.Unlock()
	log.Printf("[SANDBOX] Image %s built successfully", sandboxImage)
	return nil
}

// RunTask runs a command inside a Docker container with the project directory mounted.
// Returns stdout reader and a cleanup function.
func (cr *ContainerRunner) RunTask(ctx context.Context, opts ContainerTaskOpts) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	if !cr.IsImageReady() {
		return nil, nil, nil, fmt.Errorf("sandbox image not built — run 'yaver sandbox build' first")
	}

	image := sandboxImage
	if opts.CustomImage != "" {
		image = opts.CustomImage
	}

	// Build docker run args
	args := []string{"run", "--rm", "-i"}

	// Container name for cleanup
	containerName := fmt.Sprintf("yaver-task-%s", opts.TaskID)
	args = append(args, "--name", containerName)

	// Resource limits
	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemoryLimit != "" {
		args = append(args, "--memory", opts.MemoryLimit)
	}

	// Read-only root filesystem — only /workspace and /tmp are writable
	if opts.ReadOnly {
		args = append(args, "--read-only")
		args = append(args, "--tmpfs", "/tmp:rw,noexec,nosuid,size=512m")
		args = append(args, "--tmpfs", "/root:rw,noexec,nosuid,size=256m")
	}

	// Mount project directory
	args = append(args, "-v", fmt.Sprintf("%s:/workspace", opts.ProjectDir))
	args = append(args, "-w", "/workspace")

	// Mount cache volumes for build performance
	for _, cache := range cr.cacheDirs {
		args = append(args, "-v", cache)
	}

	// Environment variables (API keys, etc.)
	for k, v := range opts.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Network mode — default to "host" so AI agents can reach their APIs
	networkMode := opts.NetworkMode
	if networkMode == "" {
		networkMode = "host"
	}
	args = append(args, "--network", networkMode)

	// Extra volume mounts (e.g. project-specific tools)
	for _, mount := range opts.ExtraMounts {
		args = append(args, "-v", mount)
	}

	// Image + command
	args = append(args, image)

	// The actual command to run inside the container
	cmdStr := strings.Join(opts.Command, " ")
	args = append(args, cmdStr)

	cmd := exec.CommandContext(ctx, cr.dockerPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	log.Printf("[SANDBOX] Starting container %s: %s", containerName, cmdStr)
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("docker run failed: %w", err)
	}

	return cmd, stdout, stderr, nil
}

// StopContainer forcefully stops a running container by task ID.
func (cr *ContainerRunner) StopContainer(taskID string) {
	containerName := fmt.Sprintf("yaver-task-%s", taskID)
	cmd := exec.Command(cr.dockerPath, "stop", "-t", "5", containerName)
	cmd.Run() // best effort
}

// ContainerTaskOpts configures how a task runs inside a container.
type ContainerTaskOpts struct {
	TaskID      string
	ProjectDir  string            // host path to mount as /workspace
	Command     []string          // command + args to run inside container
	Env         map[string]string // environment variables (API keys, etc.)
	CustomImage string            // override sandbox image (e.g. project-specific)
	CPULimit    string            // e.g. "2.0" for 2 cores
	MemoryLimit string            // e.g. "4g" for 4GB
	NetworkMode string            // "host" (default), "bridge", or "none"
	ReadOnly    bool              // read-only root filesystem (/workspace and /tmp writable)
	ExtraMounts []string          // additional -v mounts
}

// DetectProjectImage checks if the project has a Dockerfile.yaver for custom container setup.
// If found, builds and returns the custom image name. Otherwise returns "".
func (cr *ContainerRunner) DetectProjectImage(ctx context.Context, projectDir string) string {
	customDockerfile := filepath.Join(projectDir, "Dockerfile.yaver")
	if !sandboxFileExists(customDockerfile) {
		return ""
	}

	imageName := "yaver-project-" + filepath.Base(projectDir)
	log.Printf("[SANDBOX] Found project Dockerfile.yaver, building image %s", imageName)

	cmd := exec.CommandContext(ctx, cr.dockerPath,
		"build", "-f", customDockerfile, "-t", imageName, projectDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[SANDBOX] Project image build failed: %v — using default image", err)
		return ""
	}
	return imageName
}

// CollectAPIKeys gathers API keys from the host environment that AI agents need.
// Only passes explicitly needed keys — not the entire host environment.
func CollectAPIKeys() map[string]string {
	keys := map[string]string{}
	envVars := []string{
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"GOOGLE_API_KEY",
		"MISTRAL_API_KEY",
		"GROQ_API_KEY",
		"TOGETHER_API_KEY",
		"DEEPSEEK_API_KEY",
		"XAI_API_KEY",
		"OPENROUTER_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_REGION",
		"ANTHROPIC_MODEL",
	}
	for _, k := range envVars {
		if v := os.Getenv(k); v != "" {
			keys[k] = v
		}
	}
	return keys
}

// Status returns the current state of the container runner.
type ContainerStatus struct {
	Available    bool   `json:"available"`    // Docker installed and running
	ImageReady   bool   `json:"imageReady"`   // yaver-sandbox image built
	DockerPath   string `json:"dockerPath"`   // path to docker binary
	ImageName    string `json:"imageName"`    // sandbox image name
}

func (cr *ContainerRunner) Status() ContainerStatus {
	return ContainerStatus{
		Available:  cr.IsAvailable(),
		ImageReady: cr.IsImageReady(),
		DockerPath: cr.dockerPath,
		ImageName:  sandboxImage,
	}
}

// StopAllContainers stops all running yaver-task-* containers. Called on agent shutdown.
func (cr *ContainerRunner) StopAllContainers() {
	if cr.dockerPath == "" {
		return
	}
	// List running containers with our naming prefix
	out, err := exec.Command(cr.dockerPath, "ps", "-q", "--filter", "name=yaver-task-").Output()
	if err != nil || len(out) == 0 {
		return
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		return
	}
	log.Printf("[SANDBOX] Stopping %d running task containers...", len(ids))
	args := append([]string{"stop", "-t", "5"}, ids...)
	cmd := exec.Command(cr.dockerPath, args...)
	cmd.Run() // best effort
}

// AutoBuild builds the sandbox image if not already built. Returns true if image is ready.
// Blocks until build completes (or fails). Use for first-use auto-build.
func (cr *ContainerRunner) AutoBuild(ctx context.Context) bool {
	if cr.IsImageReady() {
		return true
	}
	log.Printf("[SANDBOX] Image not found — auto-building (this takes 2-3 minutes the first time)...")
	if err := cr.BuildImage(ctx); err != nil {
		log.Printf("[SANDBOX] Auto-build failed: %v — falling back to direct execution", err)
		return false
	}
	return true
}

// IsGPUAvailable checks if NVIDIA GPU is available for container passthrough (Linux only).
func (cr *ContainerRunner) IsGPUAvailable() bool {
	cmd := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader")
	out, err := cmd.Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// StreamOutput reads container stdout line by line and sends to a channel.
func StreamContainerOutput(reader io.ReadCloser, outputCh chan<- string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		outputCh <- scanner.Text()
	}
}

func sandboxFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
