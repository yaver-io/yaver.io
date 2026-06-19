package automation

import (
	"context"
	"time"
)

// ActionType represents the type of action to perform on an element
type ActionType string

const (
	ActionClick    ActionType = "click"
	ActionFill     ActionType = "fill"
	ActionSelect   ActionType = "select"
	ActionHover    ActionType = "hover"
	ActionScroll   ActionType = "scroll"
	ActionWait     ActionType = "wait"
	ActionNavigate ActionType = "navigate"
	ActionBack     ActionType = "back"
	ActionHome     ActionType = "home"
	ActionSwipe    ActionType = "swipe"
	ActionKey      ActionType = "key"
)

// HealthStatus represents the health status of a surface
type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthUnknown   HealthStatus = "unknown"
)

// Element represents an interactable element on a surface
type Element struct {
	Selector string   `json:"selector"`
	Role     string   `json:"role"`
	Text     string   `json:"text"`
	Bounds   Rect     `json:"bounds"`
	Visible  bool     `json:"visible"`
	Enabled  bool     `json:"enabled"`
	Attrs    AttrsMap `json:"attrs,omitempty"`
}

// Rect represents a rectangle (for bounds)
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// AttrsMap is a map of element attributes
type AttrsMap map[string]string

// SurfaceState represents the complete state of a surface for capture
type SurfaceState struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Elements []Element `json:"elements"`
	Meta     StateMeta `json:"meta"`
}

// StateMeta provides metadata about the surface state
type StateMeta struct {
	Platform   string    `json:"platform"`
	AppPackage string    `json:"appPackage,omitempty"`
	AppVersion string    `json:"appVersion,omitempty"`
	Viewport   Rect      `json:"viewport"`
	CapturedAt time.Time `json:"capturedAt"`
	SessionID  string    `json:"sessionId"`
}

// NetworkEvent represents a captured network event
type NetworkEvent struct {
	URL     string    `json:"url"`
	Method  string    `json:"method"`
	Status  int       `json:"status"`
	At      time.Time `json:"at"`
	Headers AttrsMap  `json:"headers,omitempty"`
}

// ConsoleMsg represents a captured console message
type ConsoleMsg struct {
	Level string    `json:"level"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
	Stack string    `json:"stack,omitempty"`
}

// HealthReport represents the health status of a surface
type HealthReport struct {
	Overall   HealthStatus           `json:"overall"`
	Checks    map[string]HealthCheck `json:"checks"`
	Suggested []HealingAction        `json:"suggested"`
	LastCheck time.Time              `json:"lastCheck"`
}

// HealthCheck represents a single health check
type HealthCheck struct {
	Name     string        `json:"name"`
	Status   HealthStatus  `json:"status"`
	Message  string        `json:"message"`
	Duration time.Duration `json:"duration"`
	Details  interface{}   `json:"details,omitempty"`
}

// HealingAction represents a suggested healing action
type HealingAction struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Priority    int           `json:"priority"`
	Description string        `json:"description"`
	Estimated   time.Duration `json:"estimated,omitempty"`
}

// SurfaceMetrics represents performance metrics for a surface
type SurfaceMetrics struct {
	TasksTotal      int           `json:"tasksTotal"`
	TasksSuccess    int           `json:"tasksSuccess"`
	TasksFailed     int           `json:"tasksFailed"`
	AvgLatency      time.Duration `json:"avgLatency"`
	P50Latency      time.Duration `json:"p50Latency"`
	P95Latency      time.Duration `json:"p95Latency"`
	P99Latency      time.Duration `json:"p99Latency"`
	SessionsCreated int           `json:"sessionsCreated"`
	SessionsReused  int           `json:"sessionsReused"`
	Uptime          float64       `json:"uptime"` // percentage
	LastError       string        `json:"lastError,omitempty"`
	LastUsedAt      time.Time     `json:"lastUsedAt"`
}

// SessionConfig configures how a session should behave
type SessionConfig struct {
	// Identification
	TargetID string // "misli.com" or "com.garanti.ceza"
	Platform string // "web" or "mobile"

	// Authentication
	AuthMethod  string   // "oauth", "password", "totp", "none"
	Permissions []string // "read", "write", "admin"

	// Lifecycle
	KeepWarm      bool          // keep session alive for reuse
	MaxIdleTime   time.Duration // max idle time before termination
	MaxReuseCount int           // max number of times session can be reused

	// Security
	NetworkJail   string // "strict", "permissive"
	AuditActions  bool   // audit all actions
	EncryptAtRest bool   // encrypt session data
}

// AutomationSurface is the unified abstraction for both web and mobile automation.
// Personal assistant code uses this interface without knowing whether the target
// is a web browser (chromedp/playwright) or a mobile device (redroid/real device).
type AutomationSurface interface {
	// --- Lifecycle ---

	// EnsureReady provisions the surface if needed and brings it to a ready state.
	// For warm-kept surfaces, this is a quick health check. For cold surfaces,
	// this provisions and boots.
	EnsureReady(ctx context.Context) error

	// WarmKeep keeps the surface in a warm state for faster subsequent tasks.
	// Returns when the surface is confirmed warm (or an error if warm-keep fails).
	WarmKeep(ctx context.Context) error

	// Close releases all resources and cleans up the surface.
	Close()

	// --- Universal Actions ---

	// Navigate to a target (URL for web, app package/activity for mobile)
	Navigate(ctx context.Context, target string) error

	// Element returns an element by selector, or an error if not found
	Element(ctx context.Context, selector string) (Element, error)

	// Action performs a generic action on an element
	Action(ctx context.Context, selector string, action ActionType, payload any) error

	// Screenshot captures the current visible state
	Screenshot(ctx context.Context) ([]byte, error)

	// --- State Capture ---

	// CaptureState returns the complete surface state (elements, url, metadata)
	CaptureState(ctx context.Context) (SurfaceState, error)

	// CaptureNetwork returns recent network events
	CaptureNetwork(ctx context.Context) ([]NetworkEvent, error)

	// CaptureConsole returns recent console messages
	CaptureConsole(ctx context.Context) ([]ConsoleMsg, error)

	// --- Mobile-Specific Actions (no-op on web surfaces) ---

	// TapAt taps at specific coordinates
	TapAt(ctx context.Context, x, y int) error

	// Swipe performs a swipe gesture from (x1, y1) to (x2, y2) over durationMs
	Swipe(ctx context.Context, x1, y1, x2, y2 int, durationMs int) error

	// Back performs the platform "back" action
	Back(ctx context.Context) error

	// Home performs the platform "home" action
	Home(ctx context.Context) error

	// --- Diagnostics ---

	// HealthCheck runs diagnostics and returns a health report
	HealthCheck(ctx context.Context) HealthReport

	// Metrics returns performance metrics for this surface
	Metrics() SurfaceMetrics

	// --- Session Info ---

	// SessionID returns the unique identifier for this surface session
	SessionID() string

	// IsWarm reports whether the surface is already warm (ready to use)
	IsWarm() bool

	// LastUsed returns when this surface was last used
	LastUsed() time.Time

	// IdleTime returns how long this surface has been idle
	IdleTime() time.Duration
}

// SurfaceFactory creates automation surfaces based on target type
type SurfaceFactory interface {
	// Create creates a new surface with the given configuration
	Create(ctx context.Context, config SessionConfig) (AutomationSurface, error)

	// ForType returns the factory for a specific platform type
	ForType(platform string) (SurfaceFactory, error)

	// AvailablePlatforms returns the list of platforms this factory supports
	AvailablePlatforms() []string
}

// SurfacePool manages a pool of automation surfaces
type SurfacePool interface {
	// Acquire gets a warm surface for the given target, creating one if needed
	Acquire(ctx context.Context, config SessionConfig) (AutomationSurface, error)

	// Release returns a surface to the pool, potentially for reuse
	Release(ctx context.Context, surface AutomationSurface) error

	// Prune removes idle/expired surfaces from the pool
	Prune(ctx context.Context) error

	// Status returns pool status (count, metrics)
	Status(ctx context.Context) PoolStatus
}

// PoolStatus represents the status of a surface pool
type PoolStatus struct {
	Total      int                `json:"total"`
	Warm       int                `json:"warm"`
	Cold       int                `json:"cold"`
	Acquired   int                `json:"acquired"`
	Released   int                `json:"released"`
	Pruned     int                `json:"pruned"`
	ByPlatform map[string]int     `json:"byPlatform"`
	Metrics    map[string]float64 `json:"metrics"`
}

// SurfaceEvent represents a lifecycle event for a surface
type SurfaceEvent struct {
	SessionID string    `json:"sessionId"`
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Details   any       `json:"details,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// EventSink receives surface lifecycle events
type EventSink interface {
	// OnEvent is called when a surface event occurs
	OnEvent(ctx context.Context, event SurfaceEvent)
}

// EventType constants for SurfaceEvent
const (
	EventCreated     string = "created"
	EventAcquired    string = "acquired"
	EventReleased    string = "released"
	EventWarmed      string = "warmed"
	EventFailed      string = "failed"
	EventPruned      string = "pruned"
	EventHealthCheck string = "health_check"
)
