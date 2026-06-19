package testing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TestConfig represents the configuration for Playwright testing
type TestConfig struct {
	// Remote machine configuration
	RemoteMachines []RemoteMachineConfig `json:"remoteMachines"`

	// Mobile device configurations
	MobileDevices []MobileDeviceConfig `json:"mobileDevices"`

	// Evidence collection
	Evidence EvidenceConfig `json:"evidence"`

	// Third-party integrations
	ThirdParties []ThirdPartyConfig `json:"thirdParties"`

	// Performance thresholds
	Performance PerformanceThresholds `json:"performance"`

	// Retry configuration
	Retry RetryConfig `json:"retry"`

	// Parallel execution
	Parallel ParallelConfig `json:"parallel"`

	// Dashboard integration
	Dashboard DashboardConfig `json:"dashboard"`
}

type RemoteMachineConfig struct {
	Name               string `json:"name"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	AuthToken          string `json:"authToken"`
	OS                 string `json:"os"`       // linux, darwin, windows
	Platform           string `json:"platform"` // desktop, mobile
	Enabled            bool   `json:"enabled"`
	MaxConcurrentTests int    `json:"maxConcurrentTests"`
}

type MobileDeviceConfig struct {
	Name      string   `json:"name"`
	Viewport  Viewport `json:"viewport"`
	UserAgent string   `json:"userAgent"`
	IsMobile  bool     `json:"isMobile"`
	HasTouch  bool     `json:"hasTouch"`
	Enabled   bool     `json:"enabled"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type EvidenceConfig struct {
	BaseDir             string `json:"baseDir"`
	VideoEnabled        bool   `json:"videoEnabled"`
	ScreenshotOnFailure bool   `json:"screenshotOnFailure"`
	TraceEnabled        bool   `json:"traceEnabled"`
	RetentionPolicy     string `json:"retentionPolicy"` // "on-failure", "always", "never"
	DashboardURL        string `json:"dashboardURL"`
	UploadOnCompletion  bool   `json:"uploadOnCompletion"`
}

type ThirdPartyConfig struct {
	Name       string          `json:"name"`
	BaseURL    string          `json:"baseUrl"`
	AuthConfig AuthConfig      `json:"authConfig"`
	WebhookURL string          `json:"webhookUrl"`
	APITimeout time.Duration   `json:"apiTimeout"`
	RateLimit  RateLimitConfig `json:"rateLimit"`
	Enabled    bool            `json:"enabled"`
}

type AuthConfig struct {
	Type     string `json:"type"` // "basic", "token", "oauth"
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `json:"requestsPerMinute"`
	BurstAllowance    int `json:"burstAllowance"`
}

type PerformanceThresholds struct {
	PageLoadTime    time.Duration `json:"pageLoadTime"`
	DOMContentLoad  time.Duration `json:"domContentLoaded"`
	FirstPaint      time.Duration `json:"firstPaint"`
	InteractionTime time.Duration `json:"interactionTime"`
	MemoryLimitMB   int           `json:"memoryLimitMB"`
}

type RetryConfig struct {
	MaxRetries      int           `json:"maxRetries"`
	RetryDelay      time.Duration `json:"retryDelay"`
	BackoffFactor   float64       `json:"backoffFactor"`
	RetryableErrors []string      `json:"retryableErrors"`
}

type ParallelConfig struct {
	MaxWorkers    int           `json:"maxWorkers"`
	WorkerTimeout time.Duration `json:"workerTimeout"`
	BatchSize     int           `json:"batchSize"`
	QueueSize     int           `json:"queueSize"`
}

type DashboardConfig struct {
	BaseURL        string        `json:"baseUrl"`
	AuthToken      string        `json:"authToken"`
	UploadEnabled  bool          `json:"uploadEnabled"`
	ReportInterval time.Duration `json:"reportInterval"`
}

// TestSpec represents a single test specification
type TestSpec struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	URL         string        `json:"url"`
	Steps       []TestStep    `json:"steps"`
	Viewport    *Viewport     `json:"viewport,omitempty"`
	Devices     []string      `json:"devices,omitempty"` // Device names to test on
	Timeout     time.Duration `json:"timeout"`
	Skip        bool          `json:"skip"`
	Tags        []string      `json:"tags"`
	ThirdParty  string        `json:"thirdParty,omitempty"`
	Parallel    bool          `json:"parallel"`   // Can run in parallel with other tests
	IsCritical  bool          `json:"isCritical"` // Blocks release if failed
}

type TestStep struct {
	Name       string                 `json:"name"`
	Action     string                 `json:"action"` // "navigate", "click", "fill", "drag", "wait", "screenshot", "tap", "swipe", "longPress"
	Selector   string                 `json:"selector"`
	Value      string                 `json:"value,omitempty"`
	Timeout    time.Duration          `json:"timeout,omitempty"`
	Verify     string                 `json:"verify,omitempty"` // CSS selector to verify presence
	Screenshot bool                   `json:"screenshot"`
	Retry      int                    `json:"retry,omitempty"`
	Params     map[string]interface{} `json:"params,omitempty"`
	// For touch gestures
	GestureParams *GestureParams `json:"gestureParams,omitempty"`
}

type GestureParams struct {
	StartX        float64 `json:"startX"`
	StartY        float64 `json:"startY"`
	EndX          float64 `json:"endX"`
	EndY          float64 `json:"endY"`
	Duration      int     `json:"duration"`      // in milliseconds
	PinchScale    float64 `json:"pinchScale"`    // for pinch gestures
	PressDuration int     `json:"pressDuration"` // for long press
}

// TestResult represents the result of a test execution
type TestResult struct {
	TestID     string        `json:"testId"`
	TestName   string        `json:"testName"`
	Status     string        `json:"status"` // "passed", "failed", "skipped", "timeout"
	StartTime  time.Time     `json:"startTime"`
	EndTime    time.Time     `json:"endTime"`
	Duration   time.Duration `json:"duration"`
	Steps      []StepResult  `json:"steps"`
	Evidence   EvidenceData  `json:"evidence"`
	DeviceInfo *DeviceInfo   `json:"deviceInfo,omitempty"`
	Error      string        `json:"error,omitempty"`
	Metrics    TestMetrics   `json:"metrics"`
	ParallelID string        `json:"parallelId,omitempty"` // For tracking parallel test groups
	IsCritical bool          `json:"isCritical"`
}

type StepResult struct {
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Duration   time.Duration     `json:"duration"`
	Screenshot string            `json:"screenshot,omitempty"`
	Error      string            `json:"error,omitempty"`
	RetryCount int               `json:"retryCount"`
	Assertions []AssertionResult `json:"assertions,omitempty"`
}

type AssertionResult struct {
	Type     string `json:"type"` // "text", "visible", "hidden", "attribute", "url"
	Selector string `json:"selector,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Passed   bool   `json:"passed"`
	Error    string `json:"error,omitempty"`
}

type EvidenceData struct {
	Screenshots   []string       `json:"screenshots"`
	Videos        []string       `json:"videos"`
	Traces        []string       `json:"traces"`
	ConsoleLogs   []string       `json:"consoleLogs"`
	NetworkErrors []NetworkError `json:"networkErrors"`
	DOMSnapshot   string         `json:"domSnapshot"`
	APICalls      []APICallLog   `json:"apiCalls"`
	Timestamp     time.Time      `json:"timestamp"`
}

type NetworkError struct {
	URL     string `json:"url"`
	Status  int    `json:"status"`
	Message string `json:"message"`
	Timing  int64  `json:"timing"` // Response time in milliseconds
}

type APICallLog struct {
	Method    string    `json:"method"`
	URL       string    `json:"url"`
	Status    int       `json:"status"`
	Request   string    `json:"request"`
	Response  string    `json:"response"`
	Duration  int64     `json:"duration"`
	Timestamp time.Time `json:"timestamp"`
}

type DeviceInfo struct {
	Name      string   `json:"name"`
	Platform  string   `json:"platform"`
	Viewport  Viewport `json:"viewport"`
	UserAgent string   `json:"userAgent"`
	Device    string   `json:"device"` // "iphone", "ipad", "android", "desktop"
}

type TestMetrics struct {
	Performance PerformanceMetrics `json:"performance"`
	Memory      MemoryMetrics      `json:"memory"`
	Network     NetworkMetrics     `json:"network"`
	Timing      TimingMetrics      `json:"timing"`
	Coverage    CoverageMetrics    `json:"coverage"`
}

type PerformanceMetrics struct {
	PageLoadTime           time.Duration `json:"pageLoadTime"`
	DOMContentLoad         time.Duration `json:"domContentLoaded"`
	FirstPaint             time.Duration `json:"firstPaint"`
	FirstContentfulPaint   time.Duration `json:"firstContentfulPaint"`
	InteractionTime        time.Duration `json:"interactionTime"`
	TotalBlockingTime      time.Duration `json:"totalBlockingTime"`
	CumulativeLayoutShift  float64       `json:"cumulativeLayoutShift"`
	LargestContentfulPaint time.Duration `json:"largestContentfulPaint"`
	TimeToInteractive      time.Duration `json:"timeToInteractive"`
}

type MemoryMetrics struct {
	JSHeapSizeUsed  int64 `json:"jsHeapSizeUsed"`
	JSHeapSizeTotal int64 `json:"jsHeapSizeTotal"`
	JSHeapSizeLimit int64 `json:"jsHeapSizeLimit"`
	UsedJSHeapSize  int64 `json:"usedJSHeapSize"`
	TotalJSHeapSize int64 `json:"totalJSHeapSize"`
}

type NetworkMetrics struct {
	TotalRequests          int   `json:"totalRequests"`
	FailedRequests         int   `json:"failedRequests"`
	AverageLatency         int64 `json:"averageLatency"`
	TotalBytesDownloaded   int64 `json:"totalBytesDownloaded"`
	AverageBytesPerRequest int64 `json:"averageBytesPerRequest"`
	AverageRequestTime     int64 `json:"averageRequestTime"`
	SlowestTTFB            int64 `json:"slowestTTFB"` // Time to First Byte
	HighestTTFB            int64 `json:"highestTTFB"`
}

type TimingMetrics struct {
	TotalTestDuration   time.Duration `json:"totalTestDuration"`
	SetupDuration       time.Duration `json:"setupDuration"`
	TeardownDuration    time.Duration `json:"teardownDuration"`
	FirstStepLatency    time.Duration `json:"firstStepLatency"`
	AverageStepLatency  time.Duration `json:"averageStepLatency"`
	SlowestStepDuration time.Duration `json:"slowestStepDuration"`
}

type CoverageMetrics struct {
	StepsCovered       int     `json:"stepsCovered"`
	TotalSteps         int     `json:"totalSteps"`
	SelectorsUsed      int     `json:"selectorsUsed"`
	UniqueSelectors    int     `json:"uniqueSelectors"`
	CoveragePercentage float64 `json:"coveragePercentage"`
}

// TestSuite represents a collection of test specifications
type TestSuite struct {
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Tests         []TestSpec `json:"tests"`
	Config        TestConfig `json:"config"`
	SetupSteps    []TestStep `json:"setupSteps,omitempty"`
	TeardownSteps []TestStep `json:"teardownSteps,omitempty"`
	Parallel      bool       `json:"parallel"` // Run tests in parallel
	IsCritical    bool       `json:"isCritical"`
}

// TestReport represents the final test execution report
type TestReport struct {
	SuiteName    string        `json:"suiteName"`
	StartTime    time.Time     `json:"startTime"`
	EndTime      time.Time     `json:"endTime"`
	Duration     time.Duration `json:"duration"`
	TotalTests   int           `json:"totalTests"`
	PassedTests  int           `json:"passedTests"`
	FailedTests  int           `json:"failedTests"`
	SkippedTests int           `json:"skippedTests"`
	TimeoutTests int           `json:"timeoutTests"`
	Results      []TestResult  `json:"results"`
	Summary      TestSummary   `json:"summary"`
	Artifacts    ArtifactsInfo `json:"artifacts"`
}

type ArtifactsInfo struct {
	Screenshots []string `json:"screenshots"`
	Videos      []string `json:"videos"`
	Traces      []string `json:"traces"`
	Reports     []string `json:"reports"`
	Logs        []string `json:"logs"`
	BaseURL     string   `json:"baseUrl"` // Base URL for accessing artifacts
}

type TestSummary struct {
	PassRate          float64        `json:"passRate"`
	AverageDuration   time.Duration  `json:"averageDuration"`
	MedianDuration    time.Duration  `json:"medianDuration"`
	CriticalFailures  int            `json:"criticalFailures"`
	PerformanceIssues int            `json:"performanceIssues"`
	TestsByThirdParty map[string]int `json:"testsByThirdParty"`
	TestsByDevice     map[string]int `json:"testsByDevice"`
	TestsByStatus     map[string]int `json:"testsByStatus"`
	TestExecutionTime time.Duration  `json:"testExecutionTime"`
}

// LoadConfig loads test configuration from file
func LoadConfig(configPath string) (*TestConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config TestConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if config.Evidence.BaseDir == "" {
		config.Evidence.BaseDir = "./evidence"
	}
	if config.Retry.MaxRetries == 0 {
		config.Retry.MaxRetries = 3
	}
	if config.Parallel.MaxWorkers == 0 {
		config.Parallel.MaxWorkers = 4
	}
	if config.Performance.PageLoadTime == 0 {
		config.Performance.PageLoadTime = 3 * time.Second
	}
	if config.Performance.InteractionTime == 0 {
		config.Performance.InteractionTime = 500 * time.Millisecond
	}

	return &config, nil
}

// LoadTestSuite loads test suite from JSON file
func LoadTestSuite(suitePath string) (*TestSuite, error) {
	data, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read test suite: %w", err)
	}

	var suite TestSuite
	if err := json.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("failed to parse test suite: %w", err)
	}

	return &suite, nil
}

// SaveTestReport saves test report to file
func SaveTestReport(report *TestReport, reportPath string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(reportPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %w", err)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	if err := os.WriteFile(reportPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	return nil
}

// SaveArtifact saves an artifact (screenshot, video, etc.) and returns its accessible URL
func SaveArtifact(artifactPath string, baseDir string) (string, error) {
	// Convert to relative path from baseDir
	relPath, err := filepath.Rel(baseDir, artifactPath)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path: %w", err)
	}

	// In a real implementation, this would upload to a CDN or storage service
	// For now, return the relative path
	return "/" + relPath, nil
}

// NewTestRunner creates a new test runner
func NewTestRunner(suite *TestSuite) (*TestRunner, error) {
	config := suite.Config

	runner := &TestRunner{
		Config: &config,
		Suite:  suite,
		Report: &TestReport{
			SuiteName:         suite.Name,
			StartTime:         time.Now(),
			Results:           []TestResult{},
			TestsByStatus:     make(map[string]int),
			TestsByDevice:     make(map[string]int),
			TestsByThirdParty: make(map[string]int),
		},
		startTime: time.Now(),
		mu:        sync.RWMutex{},
	}

	// Initialize evidence base directory
	if err := os.MkdirAll(config.Evidence.BaseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create evidence directory: %w", err)
	}

	return runner, nil
}

// TestRunner is the main test execution engine
type TestRunner struct {
	Config    *TestConfig
	Suite     *TestSuite
	Report    *TestReport
	mu        sync.RWMutex
	startTime time.Time
	endTime   time.Time

	// Component managers
	remoteManager     *RemoteBrowserManager
	mobileManager     *MobileTestManager
	evidenceCollector *EvidenceCollector
	networkManager    *NetworkTestFramework
	perfManager       *PerformanceTestSuite
	apiManager        *APITestSuite
	thirdPartyManager *ThirdPartyTestSuite
	gestureFramework  *TouchGestureFramework

	// Test execution state
	activeTests    map[string]*TestExecution
	completedTests []*TestResult
}

type TestExecution struct {
	Spec       TestSpec
	Attempts   int
	LastResult TestResult
	StartTime  time.Time
	EndTime    time.Time
}

// Run executes the complete test suite
func (tr *TestRunner) Run() (*TestReport, error) {
	tr.startTime = time.Now()
	tr.activeTests = make(map[string]*TestExecution)
	tr.completedTests = []*TestResult{}

	// Prepare execution
	if err := tr.prepare(); err != nil {
		return nil, fmt.Errorf("failed to prepare test run: %w", err)
	}

	// Execute tests
	if err := tr.executeTests(); err != nil {
		return nil, fmt.Errorf("failed to execute tests: %w", err)
	}

	// Cleanup
	if err := tr.cleanup(); err != nil {
		return tr.Report, fmt.Errorf("failed to cleanup: %w (partial success: %w)", err)
	}

	tr.endTime = time.Now()
	tr.Report.EndTime = tr.endTime
	tr.Report.Duration = tr.endTime.Sub(tr.startTime)
	tr.Report.Summary = tr.generateSummary()
	tr.Report.Artifacts = tr.collectArtifacts()

	// Save report
	reportPath := filepath.Join(tr.Config.Evidence.BaseDir, "reports",
		fmt.Sprintf("report-%s.json", tr.startTime.Format("20060102-150405")))
	if err := SaveTestReport(tr.Report, reportPath); err != nil {
		return tr.Report, fmt.Errorf("failed to save report: %w (test execution succeeded)", err)
	}

	return tr.Report, nil
}

// prepare prepares the test environment
func (tr *TestRunner) prepare() error {
	// Create evidence subdirectories
	subdirs := []string{
		"screenshots", "videos", "traces", "logs", "reports",
	}

	for _, subdir := range subdirs {
		dirPath := filepath.Join(tr.Config.Evidence.BaseDir, subdir)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("failed to create %s directory: %w", err)
		}
	}

	// Initialize component managers
	tr.evidenceCollector = NewEvidenceCollector(tr.Config.Evidence)

	// Initialize managers
	if err := tr.initializeManagers(); err != nil {
		return fmt.Errorf("failed to initialize managers: %w", err)
	}

	return nil
}

// initializeManagers initializes all test component managers
func (tr *TestRunner) initializeManagers() error {
	// Initialize remote browser manager
	tr.remoteManager = NewRemoteBrowserManager(tr.Config.RemoteMachines)

	// Initialize mobile test manager
	tr.mobileManager = NewMobileTestManager(tr.Config.MobileDevices)

	// Initialize network test framework
	tr.networkManager = NewNetworkTestFramework()

	// Initialize performance test suite
	tr.perfManager = NewPerformanceTestSuite(tr.Config.Performance)

	// Initialize API test suite
	tr.apiManager = NewAPITestSuite()

	// Initialize third-party test suite
	tr.thirdPartyManager = NewThirdPartyTestSuite(tr.Config.ThirdParties)

	// Initialize touch gesture framework
	tr.gestureFramework = NewTouchGestureFramework()

	return nil
}

// executeTests runs all tests in the suite
func (tr *TestRunner) executeTests() error {
	testQueue := tr.buildTestQueue()

	if tr.Suite.Parallel {
		return tr.executeParallelTests(testQueue)
	} else {
		return tr.executeSequentialTests(testQueue)
	}
}

// buildTestQueue builds the execution queue respecting dependencies
func (tr *TestRunner) buildTestQueue() []TestSpec {
	var queue []TestSpec

	// Add setup tests if any
	if tr.Suite.SetupSteps != nil {
		queue = append(queue, TestSpec{
			ID:         fmt.Sprintf("%s-setup", tr.Suite.Name),
			Name:       "Setup",
			Steps:      tr.Suite.SetupSteps,
			URL:        tr.Suite.Tests[0].URL, // Use first test's URL for setup
			IsCritical: true,
		})
	}

	// Add main tests
	for _, test := range tr.Suite.Tests {
		if test.Parallel {
			queue = append(queue, test)
		} else {
			queue = append(queue, test)
		}
	}

	return queue
}

// executeSequentialTests runs tests one by one
func (tr *TestRunner) executeSequentialTests(queue []TestSpec) error {
	for _, testSpec := range queue {
		result := tr.runTestWithRetry(testSpec, "")
		tr.completedTests = append(tr.completedTests, result)

		tr.mu.Lock()
		tr.Report.Results = append(tr.Report.Results, result)
		tr.Report.TestsByStatus[result.Status]++
		tr.mu.Unlock()
	}

	return nil
}

// executeParallelTests runs tests in parallel
func (tr *TestRunner) executeParallelTests(queue []TestSpec) error {
	// Use worker pool pattern
	workerPool := make(chan struct{}, tr.Config.Parallel.MaxWorkers)
	results := make(chan TestResult, len(queue))

	// Start workers
	for i := 0; i < tr.Config.Parallel.MaxWorkers; i++ {
		go tr.parallelWorker(workerPool, results)
	}

	// Submit jobs
	for _, testSpec := range queue {
		workerPool <- struct{}{} // Send signal
		go func(spec TestSpec) {
			result := tr.runTestWithRetry(spec, fmt.Sprintf("parallel-%d", i))
			results <- result
		}(testSpec)
	}

	// Collect results
	for i := 0; i < len(queue); i++ {
		result := <-results
		tr.completedTests = append(tr.completedTests, result)

		tr.mu.Lock()
		tr.Report.Results = append(tr.Report.Results, result)
		tr.Report.TestsByStatus[result.Status]++
		tr.mu.Unlock()
	}

	close(workerPool)
	return nil
}

// parallelWorker processes tests from the work queue
func (tr *TestRunner) parallelWorker(queue chan struct{}, results chan TestResult) {
	for range queue {
		// This is a placeholder - actual implementation would
		// use the test execution logic
	}
}

// runTestWithRetry runs a test with retry logic
func (tr *TestRunner) runTestWithRetry(spec TestSpec, parallelID string) TestResult {
	var lastResult TestResult

	for attempt := 0; attempt <= tr.Config.Retry.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := calculateBackoff(tr.Config.Retry.RetryDelay, tr.Config.Retry.BackoffFactor, attempt)
			time.Sleep(backoff)
		}

		result := tr.runTest(spec, attempt, parallelID)
		lastResult = result

		if result.Status == "passed" {
			return result
		}

		// Check if error is retryable
		if !tr.isRetryableError(result.Error, spec) {
			break
		}
	}

	return lastResult
}

// calculateBackoff calculates exponential backoff delay
func calculateBackoff(baseDelay time.Duration, factor float64, attempt int) time.Duration {
	backoff := float64(baseDelay) * math.Pow(factor, float64(attempt))
	return time.Duration(backoff)
}

// isRetryableError checks if an error should trigger a retry
func (tr *TestRunner) isRetryableError(errorMsg string, spec TestSpec) bool {
	// Check if error is in configured retryable errors
	for _, retryable := range tr.Config.Retry.RetryableErrors {
		if retryable == "*" || contains(errorMsg, retryable) {
			return true
		}
	}

	// Check if test is critical - critical tests may have different retry logic
	if spec.IsCritical {
		return true
	}

	return false
}

// contains is a helper function to check if a string contains another
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// runTest executes a single test specification
func (tr *TestRunner) runTest(spec TestSpec, attempt int, parallelID string) TestResult {
	result := TestResult{
		TestID:    spec.ID,
		TestName:  spec.Name,
		Status:    "failed",
		StartTime: time.Now(),
		Steps:     []StepResult{},
		Evidence:  EvidenceData{Timestamp: time.Now()},
		Metrics: TestMetrics{
			TestsByStatus: make(map[string]int),
		},
		ParallelID: parallelID,
		IsCritical: spec.IsCritical,
	}

	// Skip if marked
	if spec.Skip {
		result.Status = "skipped"
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)
		return result
	}

	// Execute test using appropriate executor
	var executor TestExecutor
	if spec.ThirdParty != "" {
		executor = tr.thirdPartyManager.GetExecutor(spec.ThirdParty)
	} else if len(spec.Devices) > 0 {
		executor = tr.mobileManager.GetExecutor(spec.Devices[0])
	} else {
		executor = tr.remoteManager.GetExecutor()
	}

	// Run test
	result = executor.Execute(&result, spec, attempt)

	// Collect performance metrics
	tr.collectPerformanceMetrics(&result)

	// Collect evidence
	tr.collectTestEvidence(&result, spec)

	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	return result
}

// collectPerformanceMetrics collects performance metrics for a test
func (tr *TestRunner) collectPerformanceMetrics(result *TestResult) {
	// In a real implementation, this would collect metrics from the browser
	// For now, set placeholder values
	result.Metrics.Performance = PerformanceMetrics{
		PageLoadTime:    2 * time.Second,
		DOMContentLoad:  1 * time.Second,
		FirstPaint:      1 * time.Second,
		InteractionTime: 300 * time.Millisecond,
	}

	result.Metrics.Network = NetworkMetrics{
		TotalRequests:  5,
		FailedRequests: 0,
		AverageLatency: 100,
	}
}

// collectTestEvidence collects evidence for a test
func (tr *TestRunner) collectTestEvidence(result *TestResult, spec TestSpec) {
	// Collect console logs
	tr.evidenceCollector.CollectConsoleLogs(result)

	// Collect network errors
	tr.networkManager.CollectNetworkErrors(result)

	// Collect screenshots for failed steps
	for _, step := range result.Steps {
		if step.Status == "failed" && step.Screenshot != "" {
			result.Evidence.Screenshots = append(result.Evidence.Screenshots, step.Screenshot)
		}
	}

	// Collect DOM snapshot if configured
	if tr.Config.Evidence.TraceEnabled {
		// Would collect Playwright trace file
	}
}

// generateSummary generates test summary statistics
func (tr *TestRunner) generateSummary() TestSummary {
	totalTests := len(tr.Report.Results)
	passedTests := tr.Report.PassedTests
	passRate := float64(0)
	if totalTests > 0 {
		passRate = float64(passedTests) / float64(totalTests) * 100
	}

	var totalDuration time.Duration
	for _, result := range tr.Report.Results {
		totalDuration += result.Duration
	}

	// Calculate median duration
	durations := make([]time.Duration, 0, len(tr.Report.Results))
	for i, result := range tr.Report.Results {
		durations[i] = result.Duration
	}
	medianDuration := calculateMedian(durations)

	// Collect third-party stats
	testsByThirdParty := make(map[string]int)
	for _, result := range tr.Report.Results {
		if result.ThirdParty != "" {
			testsByThirdParty[result.ThirdParty]++
		}
	}

	// Collect device stats
	testsByDevice := make(map[string]int)
	for _, result := range tr.Report.Results {
		if result.DeviceInfo != nil {
			testsByDevice[result.DeviceInfo.Name]++
		}
	}

	// Count issues
	criticalFailures := 0
	performanceIssues := 0
	for _, result := range tr.Report.Results {
		if result.Status == "failed" && result.IsCritical {
			criticalFailures++
		}
		if result.Metrics.Performance.InteractionTime > tr.Config.Performance.InteractionTime {
			performanceIssues++
		}
	}

	return TestSummary{
		PassRate:          passRate,
		AverageDuration:   totalDuration / time.Duration(totalTests),
		MedianDuration:    medianDuration,
		CriticalFailures:  criticalFailures,
		PerformanceIssues: performanceIssues,
		TestsByThirdParty: testsByThirdParty,
		TestsByDevice:     testsByDevice,
		TestExecutionTime: tr.endTime.Sub(tr.startTime),
	}
}

// collectArtifacts collects all artifacts from the test run
func (tr *TestRunner) collectArtifacts() ArtifactsInfo {
	var artifacts ArtifactsInfo

	// Collect screenshots
	tr.walkDirectory(filepath.Join(tr.Config.Evidence.BaseDir, "screenshots"), &artifacts.Screenshots)

	// Collect videos
	tr.walkDirectory(filepath.Join(tr.Config.Evidence.BaseDir, "videos"), &artifacts.Videos)

	// Collect traces
	tr.walkDirectory(filepath.Join(tr.Config.Evidence.BaseDir, "traces"), &artifacts.Traces)

	// Collect reports
	tr.walkDirectory(filepath.Join(tr.Config.Evidence.BaseDir, "reports"), &artifacts.Reports)

	// In a real implementation, this would upload artifacts to storage
	// and return accessible URLs

	return artifacts
}

// walkDirectory walks a directory and returns all file paths
func (tr *TestRunner) walkDirectory(dir string, fileSlice *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			*fileSlice = append(*fileSlice, path)
		} else {
			tr.walkDirectory(path, fileSlice)
		}
	}
}

// cleanup cleans up test resources
func (tr *TestRunner) cleanup() error {
	// Disconnect from remote machines
	if tr.remoteManager != nil {
		if err := tr.remoteManager.DisconnectAll(); err != nil {
			return fmt.Errorf("failed to disconnect from remote machines: %w", err)
		}
	}

	// Upload evidence to dashboard if configured
	if tr.Config.Evidence.UploadOnCompletion && tr.Config.Evidence.DashboardURL != "" {
		if err := tr.uploadEvidenceToDashboard(); err != nil {
			return fmt.Errorf("failed to upload evidence: %w (cleanup succeeded)", err)
		}
	}

	return nil
}

// uploadEvidenceToDashboard uploads test evidence to the dashboard
func (tr *TestRunner) uploadEvidenceToDashboard() error {
	// Implement dashboard upload logic
	// This would upload videos, screenshots, and traces to talos.works dashboard
	return nil
}

// Helper function to calculate median
func calculateMedian(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}

	// Sort the durations
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)

	// Find median
	mid := len(sorted) / 2
	return sorted[mid]
}

// Helper functions
func mfgSafeID(ids ...string) string {
	for _, id := range ids {
		if id != "" {
			return id
		}
	}
	return "default"
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Initialize built-in mobile devices
func GetDefaultMobileDevices() []MobileDeviceConfig {
	return []MobileDeviceConfig{
		{
			Name:      "iPhone 12",
			Viewport:  Viewport{Width: 390, Height: 844},
			UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/605.1.15",
			IsMobile:  true,
			HasTouch:  true,
			Enabled:   true,
		},
		{
			Name:      "iPhone 14 Pro",
			Viewport:  Viewport{Width: 393, Height: 852},
			UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/605.1.15",
			IsMobile:  true,
			HasTouch:  true,
			Enabled:   true,
		},
		{
			Name:      "Samsung Galaxy S21",
			Viewport:  Viewport{Width: 360, Height: 800},
			UserAgent: "Mozilla/5.0 (Linux; Android 11; SM-G991B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/96.0.4664.45 Mobile Safari/537.36",
			IsMobile:  true,
			HasTouch:  true,
			Enabled:   true,
		},
		{
			Name:      "iPad Pro",
			Viewport:  Viewport{Width: 1024, Height: 1366},
			UserAgent: "Mozilla/5.0 (iPad; CPU OS 14_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/605.1.15",
			IsMobile:  true,
			HasTouch:  true,
			Enabled:   true,
		},
	}
}

// GetDefaultRemoteMachines returns default remote machine configurations
func GetDefaultRemoteMachines() []RemoteMachineConfig {
	return []RemoteMachineConfig{
		{
			Name:               "hetzner-talos-1",
			Host:               "talos-1.hetzner.yaver.io",
			Port:               9222,
			AuthToken:          "${YAVER_AUTH_TOKEN}",
			OS:                 "linux",
			Platform:           "desktop",
			Enabled:            true,
			MaxConcurrentTests: 5,
		},
	}
}

// GetDefaultPerformanceThresholds returns default performance thresholds
func GetDefaultPerformanceThresholds() PerformanceThresholds {
	return PerformanceThresholds{
		PageLoadTime:    3 * time.Second,
		DOMContentLoad:  2 * time.Second,
		FirstPaint:      1 * time.Second,
		InteractionTime: 500 * time.Millisecond,
		MemoryLimitMB:   512,
	}
}

// GetDefaultRetryConfig returns default retry configuration
func GetDefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,
		RetryDelay:    1 * time.Second,
		BackoffFactor: 2.0,
		RetryableErrors: []string{
			"timeout",
			"network",
			"connection",
			"temporary",
			"flaky",
		},
	}
}

// GetDefaultParallelConfig returns default parallel execution configuration
func GetDefaultParallelConfig() ParallelConfig {
	return ParallelConfig{
		MaxWorkers:    4,
		WorkerTimeout: 10 * time.Minute,
		BatchSize:     2,
		QueueSize:     100,
	}
}

// GetDefaultEvidenceConfig returns default evidence collection configuration
func GetDefaultEvidenceConfig() EvidenceConfig {
	return EvidenceConfig{
		BaseDir:             "./evidence",
		VideoEnabled:        true,
		ScreenshotOnFailure: true,
		TraceEnabled:        false,
		RetentionPolicy:     "on-failure",
		DashboardURL:        "https://talos.works/dashboard/api/evidence",
		UploadOnCompletion:  true,
	}
}

// GetDefaultDashboardConfig returns default dashboard configuration
func GetDefaultDashboardConfig() DashboardConfig {
	return DashboardConfig{
		BaseURL:        "https://talos.works",
		AuthToken:      "${YAVER_API_KEY}",
		UploadEnabled:  true,
		ReportInterval: 1 * time.Minute,
	}
}

// GetDefaultTestConfig returns a complete default test configuration
func GetDefaultTestConfig() *TestConfig {
	return &TestConfig{
		RemoteMachines: GetDefaultRemoteMachines(),
		MobileDevices:  GetDefaultMobileDevices(),
		Evidence:       GetDefaultEvidenceConfig(),
		ThirdParties:   []ThirdPartyConfig{}, // Will be configured per project
		Performance:    GetDefaultPerformanceThresholds(),
		Retry:          GetDefaultRetryConfig(),
		Parallel:       GetDefaultParallelConfig(),
		Dashboard:      GetDefaultDashboardConfig(),
	}
}
