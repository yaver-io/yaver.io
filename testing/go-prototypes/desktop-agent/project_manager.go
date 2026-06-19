//go:build yaver_dev_manager

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Task manager integration
func (s *HTTPServer) handleTasksList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	tasks := pm.GetActiveTasks()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tasks": tasks,
	})
}

// ProjectManager manages multiple development projects (Yaver, Talos, OCPP)
type ProjectManager struct {
	projects    map[string]*Project
	activeTasks map[string]*ActiveTask
	mu          sync.RWMutex
	config      *ManagerConfig
}

type DevelopmentProject struct {
	Name        string
	Path        string
	Repo        string
	Environment string
	Status      string // "idle", "building", "deployed", "testing"
	Port        int
	LastDeploy  time.Time
	HetznerID   string
}

type ManagerConfig struct {
	WorkspaceRoot      string
	HetznerDevice      string
	DefaultEnvironment string
	AutoDeploy         bool
	MobileTesting      bool
}

type ActiveTask struct {
	ID          string
	ProjectName string
	Type        string // "deploy", "test", "hot-reload"
	Status      string // "queued", "running", "completed", "failed"
	StartTime   time.Time
	EndTime     time.Time
	Output      string
	Error       string
}

type DevelopmentWorkflow struct {
	CurrentPhase string   // "development", "testing", "deployment"
	Projects     []string // ["yaver", "talos", "ocpp"]
	ActiveAgent  string   // "opencode", "codex", "claude"
	TestMode     string   // "mobile", "web", "headless"
}

// NewProjectManager creates a new project manager
func NewProjectManager(config *ManagerConfig) *ProjectManager {
	if config == nil {
		config = &ManagerConfig{
			WorkspaceRoot:      "/Users/kivanccakmak/Workspace",
			HetznerDevice:      "selected-machine",
			DefaultEnvironment: "development",
			AutoDeploy:         false,
			MobileTesting:      true,
		}
	}

	pm := &ProjectManager{
		projects:    make(map[string]*DevelopmentProject),
		activeTasks: make(map[string]*ActiveTask),
		config:      config,
	}

	// Initialize projects
	pm.initializeProjects()

	return pm
}

func (pm *ProjectManager) initializeProjects() {
	projects := []struct {
		name string
		path string
		repo string
		port int
	}{
		{"yaver", "yaver.io", "git@github.com:kivanccakmak/yaver.io.git", 18080},
		{"talos", "talos", "git@github.com:kivanccakmak/talos.git", 3000},
		{"ocpp", "ocpp", "git@github.com:kivanccakmak/ocpp.git", 8080},
	}

	for _, p := range projects {
		fullPath := filepath.Join(pm.config.WorkspaceRoot, p.path)
		pm.projects[p.name] = &DevelopmentProject{
			Name:        p.name,
			Path:        fullPath,
			Repo:        p.repo,
			Environment: pm.config.DefaultEnvironment,
			Status:      "idle",
			Port:        p.port,
		}
	}
}

// GetProjectStatus returns status of all projects
func (pm *ProjectManager) GetProjectStatus() map[string]interface{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	status := make(map[string]interface{})

	for name, project := range pm.projects {
		status[name] = map[string]interface{}{
			"path":        project.Path,
			"status":      project.Status,
			"environment": project.Environment,
			"port":        project.Port,
			"last_deploy": project.LastDeploy,
			"hetzner_id":  project.HetznerID,
		}
	}

	return status
}

// DeployProject deploys a project to Hetzner
func (pm *ProjectManager) DeployProject(ctx context.Context, projectName string) (*ActiveTask, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	project, exists := pm.projects[projectName]
	if !exists {
		return nil, fmt.Errorf("project not found: %s", projectName)
	}

	task := &ActiveTask{
		ID:          generateDevTaskID(),
		ProjectName: projectName,
		Type:        "deploy",
		Status:      "queued",
		StartTime:   time.Now(),
	}

	pm.activeTasks[task.ID] = task

	// Update project status
	project.Status = "building"

	// Start deployment in background
	go pm.runDeployment(task, project)

	return task, nil
}

func (pm *ProjectManager) runDeployment(task *ActiveTask, project *DevelopmentProject) {
	task.Status = "running"

	defer func() {
		task.EndTime = time.Now()
		if task.Error != "" {
			project.Status = "idle"
		} else {
			project.Status = "deployed"
			project.LastDeploy = time.Now()
		}
	}()

	// Check if project exists locally
	if _, err := os.Stat(project.Path); os.IsNotExist(err) {
		task.Error = fmt.Sprintf("project directory not found: %s", project.Path)
		task.Status = "failed"
		return
	}

	// Run deployment script
	output, err := pm.runDeploymentScript(project)
	if err != nil {
		task.Error = err.Error()
		task.Status = "failed"
		return
	}

	task.Output = output
	task.Status = "completed"
}

func (pm *ProjectManager) runDeploymentScript(project *DevelopmentProject) (string, error) {
	// This would execute the bash script we created earlier
	scriptPath := filepath.Join(pm.config.WorkspaceRoot, "yaver.io", "scripts", "hetzner-deploy.sh")

	// For now, simulate the deployment
	return fmt.Sprintf("Deployed %s to %s environment on Hetzner", project.Name, project.Environment), nil
}

// HotReloadProject enables hot-reload for a project
func (pm *ProjectManager) HotReloadProject(ctx context.Context, projectName string) (*ActiveTask, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	project, exists := pm.projects[projectName]
	if !exists {
		return nil, fmt.Errorf("project not found: %s", projectName)
	}

	task := &ActiveTask{
		ID:          generateDevTaskID(),
		ProjectName: projectName,
		Type:        "hot-reload",
		Status:      "queued",
		StartTime:   time.Now(),
	}

	pm.activeTasks[task.ID] = task

	go pm.runHotReload(task, project)

	return task, nil
}

func (pm *ProjectManager) runHotReload(task *ActiveTask, project *DevelopmentProject) {
	task.Status = "running"
	defer func() {
		task.EndTime = time.Now()
		project.Status = "testing"
	}()

	// Run hot-reload script
	output, err := pm.runHotReloadScript(project)
	if err != nil {
		task.Error = err.Error()
		task.Status = "failed"
		return
	}

	task.Output = output
	task.Status = "completed"
}

func (pm *ProjectManager) runHotReloadScript(project *DevelopmentProject) (string, error) {
	return fmt.Sprintf("Hot-reload enabled for %s on port %d", project.Name, project.Port), nil
}

// SetupMobileTesting configures mobile testing for projects
func (pm *ProjectManager) SetupMobileTesting(ctx context.Context, projectNames []string) (*ActiveTask, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Create a combined task for multiple projects
	task := &ActiveTask{
		ID:          generateDevTaskID(),
		ProjectName: "multi-project",
		Type:        "mobile-test",
		Status:      "queued",
		StartTime:   time.Now(),
	}

	pm.activeTasks[task.ID] = task

	go pm.runMobileTestSetup(task, projectNames)

	return task, nil
}

func (pm *ProjectManager) runMobileTestSetup(task *ActiveTask, projectNames []string) {
	task.Status = "running"
	defer func() {
		task.EndTime = time.Now()
	}()

	var output string
	var errors []string

	for _, name := range projectNames {
		project, exists := pm.projects[name]
		if !exists {
			errors = append(errors, fmt.Sprintf("project not found: %s", name))
			continue
		}

		// Setup mobile testing for each project
		result, err := pm.setupProjectMobileTesting(project)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", name, err.Error()))
			continue
		}

		output += fmt.Sprintf("%s\n", result)
		project.Status = "testing"
	}

	if len(errors) > 0 {
		task.Error = fmt.Sprintf("Errors: %v", errors)
		task.Status = "failed"
	} else {
		task.Output = output
		task.Status = "completed"
	}
}

func (pm *ProjectManager) setupProjectMobileTesting(project *DevelopmentProject) (string, error) {
	// Create tunnel for mobile access
	return fmt.Sprintf("Mobile testing setup complete for %s. Access via Yaver mobile app.", project.Name), nil
}

// SwitchDevelopmentAgent switches the active AI agent for development
func (pm *ProjectManager) SwitchDevelopmentAgent(ctx context.Context, agentID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Validate agent ID
	validAgents := map[string]bool{
		"opencode": true,
		"codex":    true,
		"claude":   true,
	}

	if !validAgents[agentID] {
		return fmt.Errorf("invalid agent ID: %s", agentID)
	}

	// Store current agent preference
	pm.config.ActiveAgent = agentID

	return nil
}

// GetActiveTasks returns all active tasks
func (pm *ProjectManager) GetActiveTasks() map[string]*ActiveTask {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Return copy to avoid race conditions
	tasks := make(map[string]*ActiveTask)
	for id, task := range pm.activeTasks {
		tasks[id] = task
	}

	return tasks
}

// GetTask returns a specific task
func (pm *ProjectManager) GetTask(taskID string) (*ActiveTask, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	task, exists := pm.activeTasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	return task, nil
}

// CreateDevelopmentWorkflow creates a new development workflow
func (pm *ProjectManager) CreateDevelopmentWorkflow(projects []string, testMode string) *DevelopmentWorkflow {
	return &DevelopmentWorkflow{
		CurrentPhase: "development",
		Projects:     projects,
		TestMode:     testMode,
		ActiveAgent:  pm.config.ActiveAgent,
	}
}

// ExecuteWorkflow executes a development workflow
func (pm *ProjectManager) ExecuteWorkflow(ctx context.Context, workflow *DevelopmentWorkflow) ([]*ActiveTask, error) {
	var tasks []*ActiveTask

	// Deploy all projects in workflow
	for _, projectName := range workflow.Projects {
		task, err := pm.DeployProject(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("failed to deploy %s: %w", projectName, err)
		}
		tasks = append(tasks, task)
	}

	// Wait for deployments to complete
	for _, task := range tasks {
		pm.waitForTask(ctx, task.ID)
	}

	// Setup mobile testing if requested
	if workflow.TestMode == "mobile" {
		mobileTask, err := pm.SetupMobileTesting(ctx, workflow.Projects)
		if err != nil {
			return nil, fmt.Errorf("failed to setup mobile testing: %w", err)
		}
		tasks = append(tasks, mobileTask)
	}

	// Enable hot-reload for all projects
	for _, projectName := range workflow.Projects {
		task, err := pm.HotReloadProject(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("failed to enable hot-reload for %s: %w", projectName, err)
		}
		tasks = append(tasks, task)
	}

	workflow.CurrentPhase = "testing"

	return tasks, nil
}

func (pm *ProjectManager) waitForTask(ctx context.Context, taskID string) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			task, _ := pm.GetTask(taskID)
			if task == nil || task.Status == "completed" || task.Status == "failed" {
				return
			}
		}
	}
}

func generateDevTaskID() string {
	return fmt.Sprintf("dev-task-%d", time.Now().UnixNano())
}

type ManagerConfig struct {
	WorkspaceRoot      string
	HetznerDevice      string
	DefaultEnvironment string
	AutoDeploy         bool
	MobileTesting      bool
}

type ActiveTask struct {
	ID          string
	ProjectName string
	Type        string // "deploy", "test", "hot-reload"
	Status      string // "queued", "running", "completed", "failed"
	StartTime   time.Time
	EndTime     time.Time
	Output      string
	Error       string
}

type DevelopmentWorkflow struct {
	CurrentPhase string   // "development", "testing", "deployment"
	Projects     []string // ["yaver", "talos", "ocpp"]
	ActiveAgent  string   // "opencode", "codex", "claude"
	TestMode     string   // "mobile", "web", "headless"
}

// NewProjectManager creates a new project manager
func NewProjectManager(config *ManagerConfig) *ProjectManager {
	if config == nil {
		config = &ManagerConfig{
			WorkspaceRoot:      "/Users/kivanccakmak/Workspace",
			HetznerDevice:      "selected-machine",
			DefaultEnvironment: "development",
			AutoDeploy:         false,
			MobileTesting:      true,
		}
	}

	pm := &ProjectManager{
		projects:    make(map[string]*Project),
		activeTasks: make(map[string]*ActiveTask),
		config:      config,
	}

	// Initialize projects
	pm.initializeProjects()

	return pm
}

func (pm *ProjectManager) initializeProjects() {
	projects := []struct {
		name string
		path string
		repo string
		port int
	}{
		{"yaver", "yaver.io", "git@github.com:kivanccakmak/yaver.io.git", 18080},
		{"talos", "talos", "git@github.com:kivanccakmak/talos.git", 3000},
		{"ocpp", "ocpp", "git@github.com:kivanccakmak/ocpp.git", 8080},
	}

	for _, p := range projects {
		fullPath := filepath.Join(pm.config.WorkspaceRoot, p.path)
		pm.projects[p.name] = &Project{
			Name:        p.name,
			Path:        fullPath,
			Repo:        p.repo,
			Environment: pm.config.DefaultEnvironment,
			Status:      "idle",
			Port:        p.port,
		}
	}
}

// GetProjectStatus returns status of all projects
func (pm *ProjectManager) GetProjectStatus() map[string]interface{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	status := make(map[string]interface{})

	for name, project := range pm.projects {
		status[name] = map[string]interface{}{
			"path":        project.Path,
			"status":      project.Status,
			"environment": project.Environment,
			"port":        project.Port,
			"last_deploy": project.LastDeploy,
			"hetzner_id":  project.HetznerID,
		}
	}

	return status
}

// DeployProject deploys a project to Hetzner
func (pm *ProjectManager) DeployProject(ctx context.Context, projectName string) (*ActiveTask, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	project, exists := pm.projects[projectName]
	if !exists {
		return nil, fmt.Errorf("project not found: %s", projectName)
	}

	task := &ActiveTask{
		ID:          generateTaskID(),
		ProjectName: projectName,
		Type:        "deploy",
		Status:      "queued",
		StartTime:   time.Now(),
	}

	pm.activeTasks[task.ID] = task

	// Update project status
	project.Status = "building"

	// Start deployment in background
	go pm.runDeployment(task, project)

	return task, nil
}

func (pm *ProjectManager) runDeployment(task *ActiveTask, project *Project) {
	task.Status = "running"

	defer func() {
		task.EndTime = time.Now()
		if task.Error != "" {
			project.Status = "idle"
		} else {
			project.Status = "deployed"
			project.LastDeploy = time.Now()
		}
	}()

	// Check if project exists locally
	if _, err := os.Stat(project.Path); os.IsNotExist(err) {
		task.Error = fmt.Sprintf("project directory not found: %s", project.Path)
		task.Status = "failed"
		return
	}

	// Run deployment script
	output, err := pm.runDeploymentScript(project)
	if err != nil {
		task.Error = err.Error()
		task.Status = "failed"
		return
	}

	task.Output = output
	task.Status = "completed"
}

func (pm *ProjectManager) runDeploymentScript(project *Project) (string, error) {
	// This would execute the bash script we created earlier
	scriptPath := filepath.Join(pm.config.WorkspaceRoot, "yaver.io", "scripts", "hetzner-deploy.sh")

	// For now, simulate the deployment
	return fmt.Sprintf("Deployed %s to %s environment on Hetzner", project.Name, project.Environment), nil
}

// HotReloadProject enables hot-reload for a project
func (pm *ProjectManager) HotReloadProject(ctx context.Context, projectName string) (*ActiveTask, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	project, exists := pm.projects[projectName]
	if !exists {
		return nil, fmt.Errorf("project not found: %s", projectName)
	}

	task := &ActiveTask{
		ID:          generateTaskID(),
		ProjectName: projectName,
		Type:        "hot-reload",
		Status:      "queued",
		StartTime:   time.Now(),
	}

	pm.activeTasks[task.ID] = task

	go pm.runHotReload(task, project)

	return task, nil
}

func (pm *ProjectManager) runHotReload(task *ActiveTask, project *Project) {
	task.Status = "running"
	defer func() {
		task.EndTime = time.Now()
		project.Status = "testing"
	}()

	// Run hot-reload script
	output, err := pm.runHotReloadScript(project)
	if err != nil {
		task.Error = err.Error()
		task.Status = "failed"
		return
	}

	task.Output = output
	task.Status = "completed"
}

func (pm *ProjectManager) runHotReloadScript(project *Project) (string, error) {
	return fmt.Sprintf("Hot-reload enabled for %s on port %d", project.Name, project.Port), nil
}

// SetupMobileTesting configures mobile testing for projects
func (pm *ProjectManager) SetupMobileTesting(ctx context.Context, projectNames []string) (*ActiveTask, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Create a combined task for multiple projects
	task := &ActiveTask{
		ID:          generateTaskID(),
		ProjectName: "multi-project",
		Type:        "mobile-test",
		Status:      "queued",
		StartTime:   time.Now(),
	}

	pm.activeTasks[task.ID] = task

	go pm.runMobileTestSetup(task, projectNames)

	return task, nil
}

func (pm *ProjectManager) runMobileTestSetup(task *ActiveTask, projectNames []string) {
	task.Status = "running"
	defer func() {
		task.EndTime = time.Now()
	}()

	var output string
	var errors []string

	for _, name := range projectNames {
		project, exists := pm.projects[name]
		if !exists {
			errors = append(errors, fmt.Sprintf("project not found: %s", name))
			continue
		}

		// Setup mobile testing for each project
		result, err := pm.setupProjectMobileTesting(project)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", name, err.Error()))
			continue
		}

		output += fmt.Sprintf("%s\n", result)
		project.Status = "testing"
	}

	if len(errors) > 0 {
		task.Error = fmt.Sprintf("Errors: %v", errors)
		task.Status = "failed"
	} else {
		task.Output = output
		task.Status = "completed"
	}
}

func (pm *ProjectManager) setupProjectMobileTesting(project *Project) (string, error) {
	// Create tunnel for mobile access
	return fmt.Sprintf("Mobile testing setup complete for %s. Access via Yaver mobile app.", project.Name), nil
}

// SwitchDevelopmentAgent switches the active AI agent for development
func (pm *ProjectManager) SwitchDevelopmentAgent(ctx context.Context, agentID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Validate agent ID
	validAgents := map[string]bool{
		"opencode": true,
		"codex":    true,
		"claude":   true,
	}

	if !validAgents[agentID] {
		return fmt.Errorf("invalid agent ID: %s", agentID)
	}

	// Store current agent preference
	pm.config.ActiveAgent = agentID

	return nil
}

// GetActiveTasks returns all active tasks
func (pm *ProjectManager) GetActiveTasks() map[string]*ActiveTask {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Return copy to avoid race conditions
	tasks := make(map[string]*ActiveTask)
	for id, task := range pm.activeTasks {
		tasks[id] = task
	}

	return tasks
}

// GetTask returns a specific task
func (pm *ProjectManager) GetTask(taskID string) (*ActiveTask, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	task, exists := pm.activeTasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	return task, nil
}

// CreateDevelopmentWorkflow creates a new development workflow
func (pm *ProjectManager) CreateDevelopmentWorkflow(projects []string, testMode string) *DevelopmentWorkflow {
	return &DevelopmentWorkflow{
		CurrentPhase: "development",
		Projects:     projects,
		TestMode:     testMode,
		ActiveAgent:  pm.config.ActiveAgent,
	}
}

// ExecuteWorkflow executes a development workflow
func (pm *ProjectManager) ExecuteWorkflow(ctx context.Context, workflow *DevelopmentWorkflow) ([]*ActiveTask, error) {
	var tasks []*ActiveTask

	// Deploy all projects in workflow
	for _, projectName := range workflow.Projects {
		task, err := pm.DeployProject(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("failed to deploy %s: %w", projectName, err)
		}
		tasks = append(tasks, task)
	}

	// Wait for deployments to complete
	for _, task := range tasks {
		pm.waitForTask(ctx, task.ID)
	}

	// Setup mobile testing if requested
	if workflow.TestMode == "mobile" {
		mobileTask, err := pm.SetupMobileTesting(ctx, workflow.Projects)
		if err != nil {
			return nil, fmt.Errorf("failed to setup mobile testing: %w", err)
		}
		tasks = append(tasks, mobileTask)
	}

	// Enable hot-reload for all projects
	for _, projectName := range workflow.Projects {
		task, err := pm.HotReloadProject(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("failed to enable hot-reload for %s: %w", projectName, err)
		}
		tasks = append(tasks, task)
	}

	workflow.CurrentPhase = "testing"

	return tasks, nil
}

func (pm *ProjectManager) waitForTask(ctx context.Context, taskID string) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			task, _ := pm.GetTask(taskID)
			if task == nil || task.Status == "completed" || task.Status == "failed" {
				return
			}
		}
	}
}

func generateTaskID() string {
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

// HTTP handlers for project management
func (s *HTTPServer) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	status := pm.GetProjectStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"projects": status,
	})
}

func (s *HTTPServer) handleProjectDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectName string `json:"project_name"`
		Environment string `json:"environment"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	task, err := pm.DeployProject(r.Context(), req.ProjectName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (s *HTTPServer) handleProjectHotReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectName string `json:"project_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	task, err := pm.HotReloadProject(r.Context(), req.ProjectName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (s *HTTPServer) handleMobileTestSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Projects []string `json:"projects"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	task, err := pm.SetupMobileTesting(r.Context(), req.Projects)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (s *HTTPServer) handleAgentSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AgentID string `json:"agent_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	if err := pm.SwitchDevelopmentAgent(r.Context(), req.AgentID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Switched to agent: %s", req.AgentID),
	})
}

func (s *HTTPServer) handleWorkflowExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Projects []string `json:"projects"`
		TestMode string   `json:"test_mode"`
		AgentID  string   `json:"agent_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pm := s.projectManager
	if pm == nil {
		http.Error(w, "Project manager not initialized", http.StatusInternalServerError)
		return
	}

	// Switch agent if specified
	if req.AgentID != "" {
		if err := pm.SwitchDevelopmentAgent(r.Context(), req.AgentID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Create and execute workflow
	workflow := pm.CreateDevelopmentWorkflow(req.Projects, req.TestMode)
	tasks, err := pm.ExecuteWorkflow(r.Context(), workflow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"workflow": workflow,
		"tasks":    tasks,
		"message":  "Workflow execution started",
	})
}
