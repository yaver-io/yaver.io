package framework

import (
	"fmt"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

// PlaywrightManager manages Playwright instances and browser lifecycle
type PlaywrightManager struct {
	pw        *playwright.Playwright
	instances map[string]*BrowserInstance
	mu        sync.RWMutex
	config    PlaywrightConfig
}

// BrowserInstance represents a running browser instance
type BrowserInstance struct {
	ID        string
	Browser   playwright.Browser
	Context   playwright.BrowserContext
	Page      playwright.Page
	StartTime time.Time
	Metadata  BrowserMetadata
}

// BrowserMetadata contains metadata about a browser instance
type BrowserMetadata struct {
	DeviceInfo   DeviceInfo
	SessionInfo  SessionInfo
	NetworkInfo  NetworkInfo
	CustomFields map[string]interface{}
}

// DeviceInfo describes the device being emulated
type DeviceInfo struct {
	Name      string
	Platform  string // "desktop", "mobile", "tablet"
	Viewport  ViewportDimensions
	UserAgent string
	IsMobile  bool
}

// SessionInfo tracks session state
type SessionInfo struct {
	SessionID       string
	TestExecutionID string
	CreatedAt       time.Time
	LastActivity    time.Time
}

// NetworkInfo tracks network state
type NetworkInfo struct {
	InterceptionEnabled bool
	Mocks               []NetworkMock
	RecordedRequests    []RequestRecord
	RecordedResponses   []ResponseRecord
}

// ViewportDimensions defines viewport size
type ViewportDimensions struct {
	Width  int
	Height int
}

// NetworkMock represents a network request/response mock
type NetworkMock struct {
	Pattern      string
	Method       string
	Response     *MockResponse
	ResponseTime time.Duration
}

// MockResponse defines a mock response
type MockResponse struct {
	StatusCode int
	Body       string
	Headers    map[string]string
}

// RequestRecord tracks a network request
type RequestRecord struct {
	Timestamp    time.Time
	URL          string
	Method       string
	Headers      map[string]string
	Body         string
	ResponseTime time.Duration
}

// ResponseRecord tracks a network response
type ResponseRecord struct {
	Timestamp  time.Time
	StatusCode int
	Headers    map[string]string
	Body       string
	BodySize   int64
}

// NewPlaywrightManager creates a new Playwright manager
func NewPlaywrightManager(config PlaywrightConfig) (*PlaywrightManager, error) {
	pm := &PlaywrightManager{
		instances: make(map[string]*BrowserInstance),
		config:    config,
	}

	// Initialize Playwright
	pw, err := playwright.Run(&playwright.RunOptions{
		Headless:    config.Headless,
		Browsers:    "chromium,firefox,webkit",
		Timeout:     config.Timeout,
		InstallDeps: config.InstallDeps,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Playwright: %w", err)
	}

	pm.pw = pw
	return pm, nil
}

// LaunchBrowser launches a new browser instance
func (pm *PlaywrightManager) LaunchBrowser(browserType string, metadata BrowserMetadata) (*BrowserInstance, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Create instance ID
	instanceID := fmt.Sprintf("%s-%s-%d", browserType, metadata.SessionInfo.SessionID, time.Now().UnixNano())

	// Get browser launcher
	var browserTypeInstance playwright.BrowserType
	switch browserType {
	case "chromium":
		browserTypeInstance = pm.pw.Chromium
	case "firefox":
		browserTypeInstance = pm.pw.Firefox
	case "webkit":
		browserTypeInstance = pm.pw.Webkit
	default:
		return nil, fmt.Errorf("unsupported browser type: %s", browserType)
	}

	// Launch browser
	browser, err := browserTypeInstance.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(pm.config.Headless),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}

	// Create context with device emulation
	contextOptions := playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{
			Width:  playwright.Int(metadata.DeviceInfo.Viewport.Width),
			Height: playwright.Int(metadata.DeviceInfo.Viewport.Height),
		},
		UserAgent:         playwright.String(metadata.DeviceInfo.UserAgent),
		IsMobile:          playwright.Bool(metadata.DeviceInfo.IsMobile),
		DeviceScaleFactor: playwright.Float(1.0),
	}

	// Enable network interception if configured
	if metadata.NetworkInfo.InterceptionEnabled {
		// Network interception will be enabled after context creation
	}

	context, err := browser.NewContext(contextOptions)
	if err != nil {
		browser.Close()
		return nil, fmt.Errorf("failed to create browser context: %w", err)
	}

	// Create page
	page, err := context.NewPage()
	if err != nil {
		context.Close()
		browser.Close()
		return nil, fmt.Errorf("failed to create page: %w", err)
	}

	// Set up network interception if enabled
	if metadata.NetworkInfo.InterceptionEnabled {
		if err := pm.setupNetworkInterception(page, metadata); err != nil {
			page.Close()
			context.Close()
			browser.Close()
			return nil, fmt.Errorf("failed to setup network interception: %w", err)
		}
	}

	// Create browser instance
	instance := &BrowserInstance{
		ID:        instanceID,
		Browser:   browser,
		Context:   context,
		Page:      page,
		StartTime: time.Now(),
		Metadata:  metadata,
	}

	pm.instances[instanceID] = instance
	return instance, nil
}

// setupNetworkInterception configures network request interception
func (pm *PlaywrightManager) setupNetworkInterception(page playwright.Page, metadata BrowserMetadata) error {
	// Enable route interception for all requests
	if err := page.Route("**/*", func(route playwright.Route, request playwright.Request) {
		// Check for mock matches
		for _, mock := range metadata.NetworkInfo.Mocks {
			if pm.requestMatchesPattern(request.URL(), mock.Pattern) {
				// Apply mock response
				route.Fulfill(playwright.RouteFulfillOptions{
					Status:  playwright.Int(mock.Response.StatusCode),
					Body:    playwright.String(mock.Response.Body),
					Headers: mock.Response.Headers,
				})

				// Record request
				metadata.NetworkInfo.RecordedRequests = append(metadata.NetworkInfo.RecordedRequests, RequestRecord{
					Timestamp: time.Now(),
					URL:       request.URL(),
					Method:    request.Method(),
				})
				return
			}
		}

		// Continue with normal request
		route.Continue()

		// Record request
		metadata.NetworkInfo.RecordedRequests = append(metadata.NetworkInfo.RecordedRequests, RequestRecord{
			Timestamp: time.Now(),
			URL:       request.URL(),
			Method:    request.Method(),
		})
	}); err != nil {
		return fmt.Errorf("failed to set up route interception: %w", err)
	}

	// Listen for responses
	page.On("response", func(response playwright.Response) {
		metadata.NetworkInfo.RecordedResponses = append(metadata.NetworkInfo.RecordedResponses, ResponseRecord{
			Timestamp:  time.Now(),
			StatusCode: response.Status(),
			BodySize:   int64(len(response.Text())),
		})
	})

	return nil
}

// requestMatchesPattern checks if a request URL matches a mock pattern
func (pm *PlaywrightManager) requestMatchesPattern(url, pattern string) bool {
	// Simple implementation - can be enhanced with regex support
	return url == pattern
}

// GetInstance retrieves a browser instance by ID
func (pm *PlaywrightManager) GetInstance(instanceID string) (*BrowserInstance, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	instance, exists := pm.instances[instanceID]
	return instance, exists
}

// ListInstances returns all active browser instances
func (pm *PlaywrightManager) ListInstances() []BrowserInstance {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	instances := make([]BrowserInstance, 0, len(pm.instances))
	for _, instance := range pm.instances {
		instances = append(instances, *instance)
	}
	return instances
}

// CloseInstance closes a specific browser instance
func (pm *PlaywrightManager) CloseInstance(instanceID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	instance, exists := pm.instances[instanceID]
	if !exists {
		return fmt.Errorf("browser instance not found: %s", instanceID)
	}

	if err := instance.Page.Close(); err != nil {
		return fmt.Errorf("failed to close page: %w", err)
	}

	if err := instance.Context.Close(); err != nil {
		return fmt.Errorf("failed to close context: %w", err)
	}

	if err := instance.Browser.Close(); err != nil {
		return fmt.Errorf("failed to close browser: %w", err)
	}

	delete(pm.instances, instanceID)
	return nil
}

// CloseAll closes all browser instances
func (pm *PlaywrightManager) CloseAll() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var errors []error
	for instanceID := range pm.instances {
		if err := pm.CloseInstance(instanceID); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to close some instances: %v", errors)
	}

	return nil
}

// Cleanup shuts down the Playwright manager
func (pm *PlaywrightManager) Cleanup() error {
	if err := pm.CloseAll(); err != nil {
		return err
	}

	if pm.pw != nil {
		if err := pm.pw.Stop(); err != nil {
			return fmt.Errorf("failed to stop Playwright: %w", err)
		}
	}

	return nil
}
