package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// MockRoute defines a single mock HTTP endpoint.
type MockRoute struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
	Delay      time.Duration     `json:"delay,omitempty"`
	Dynamic    bool              `json:"dynamic,omitempty"` // interpret Body as Go template
}

// MockPreset is a named collection of routes for common third-party APIs.
type MockPreset struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Routes      []MockRoute `json:"routes"`
}

// MockRecording captures a request/response pair during record mode.
type MockRecording struct {
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	ResponseStatus  int               `json:"response_status"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	Timestamp       time.Time         `json:"timestamp"`
}

// MockServerConfig is persisted to ~/.yaver/mock.json.
type MockServerConfig struct {
	Port       int             `json:"port"`
	Routes     []MockRoute     `json:"routes"`
	Recordings []MockRecording `json:"recordings,omitempty"`
}

// MockServer manages a configurable HTTP mock server.
type MockServer struct {
	mu         sync.Mutex
	server     *http.Server
	port       int
	routes     []MockRoute
	recordings []MockRecording
	recording  bool   // record mode enabled
	configPath string
}

// NewMockServer creates a MockServer with default port 9999 and config at
// ~/.yaver/mock.json.
func NewMockServer() *MockServer {
	configPath := ""
	if dir, err := ConfigDir(); err == nil {
		configPath = filepath.Join(dir, "mock.json")
	}
	ms := &MockServer{
		port:       9999,
		configPath: configPath,
	}
	// Best-effort load; ignore errors (fresh start is fine).
	_ = ms.loadConfig()
	return ms
}

// Start launches the mock HTTP server on the given port. It registers all
// configured routes and returns the base URL.
func (ms *MockServer) Start(port int) (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if ms.server != nil {
		return "", fmt.Errorf("mock server already running on port %d", ms.port)
	}

	ms.port = port
	mux := http.NewServeMux()
	// Catch-all handler; routing is done inside handleMockRequest.
	mux.HandleFunc("/", ms.handleMockRequest)

	ms.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := ms.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[mock] server error: %v", err)
		}
	}()

	// Persist updated port.
	_ = ms.saveConfig()

	url := fmt.Sprintf("http://localhost:%d", port)
	return fmt.Sprintf("Mock server started at %s (%d routes)", url, len(ms.routes)), nil
}

// Stop shuts down the mock HTTP server gracefully.
func (ms *MockServer) Stop() (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if ms.server == nil {
		return "", fmt.Errorf("mock server is not running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := ms.server.Shutdown(ctx); err != nil {
		return "", fmt.Errorf("shutdown error: %w", err)
	}
	ms.server = nil
	return "Mock server stopped", nil
}

// AddRoute registers a new mock route. The server is updated live if running,
// and the config is persisted.
func (ms *MockServer) AddRoute(method, path string, status int, body string, headers map[string]string) (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	method = strings.ToUpper(method)
	if method == "" {
		method = "GET"
	}
	if status == 0 {
		status = 200
	}
	if headers == nil {
		headers = map[string]string{}
	}

	// Replace any existing route with the same method+path.
	replaced := false
	for i, r := range ms.routes {
		if strings.EqualFold(r.Method, method) && r.Path == path {
			ms.routes[i] = MockRoute{
				Method:     method,
				Path:       path,
				StatusCode: status,
				Headers:    headers,
				Body:       body,
			}
			replaced = true
			break
		}
	}
	if !replaced {
		ms.routes = append(ms.routes, MockRoute{
			Method:     method,
			Path:       path,
			StatusCode: status,
			Headers:    headers,
			Body:       body,
		})
	}

	if err := ms.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}

	action := "added"
	if replaced {
		action = "updated"
	}
	return fmt.Sprintf("Route %s %s %s (status %d)", action, method, path, status), nil
}

// RemoveRoute deletes a route identified by method and path.
func (ms *MockServer) RemoveRoute(method, path string) (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	method = strings.ToUpper(method)
	original := len(ms.routes)
	kept := ms.routes[:0]
	for _, r := range ms.routes {
		if strings.EqualFold(r.Method, method) && r.Path == path {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == original {
		return "", fmt.Errorf("route not found: %s %s", method, path)
	}
	ms.routes = kept

	if err := ms.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return fmt.Sprintf("Route removed: %s %s", method, path), nil
}

// ListRoutes returns all configured routes.
func (ms *MockServer) ListRoutes() ([]MockRoute, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	out := make([]MockRoute, len(ms.routes))
	copy(out, ms.routes)
	return out, nil
}

// Reset clears all routes and recordings and persists the empty config.
func (ms *MockServer) Reset() (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.routes = nil
	ms.recordings = nil
	ms.recording = false

	if err := ms.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return "Mock server reset: all routes and recordings cleared", nil
}

// StartRecording enables record mode. Unmatched requests are recorded and
// returned as 404 to the caller so the real client is aware that the route
// was missing.
func (ms *MockServer) StartRecording() (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if ms.recording {
		return "", fmt.Errorf("already in recording mode")
	}
	ms.recording = true
	return "Recording mode enabled. Unmatched requests will be captured.", nil
}

// StopRecording disables record mode and returns all captured recordings.
func (ms *MockServer) StopRecording() ([]MockRecording, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if !ms.recording {
		return nil, fmt.Errorf("not in recording mode")
	}
	ms.recording = false

	out := make([]MockRecording, len(ms.recordings))
	copy(out, ms.recordings)
	return out, nil
}

// LoadPreset loads a named preset, replacing any existing routes that conflict.
func (ms *MockServer) LoadPreset(name string) (string, error) {
	preset, err := ms.getPreset(name)
	if err != nil {
		return "", err
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	added := 0
	updated := 0
	for _, route := range preset.Routes {
		replaced := false
		for i, r := range ms.routes {
			if strings.EqualFold(r.Method, route.Method) && r.Path == route.Path {
				ms.routes[i] = route
				replaced = true
				updated++
				break
			}
		}
		if !replaced {
			ms.routes = append(ms.routes, route)
			added++
		}
	}

	if err := ms.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return fmt.Sprintf("Preset '%s' loaded: %d routes added, %d updated", name, added, updated), nil
}

// LoadOpenAPI parses an OpenAPI 3.0 YAML or JSON spec file and generates mock
// routes from path definitions and example/schema responses.
func (ms *MockServer) LoadOpenAPI(specPath string) (string, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return "", fmt.Errorf("read spec file: %w", err)
	}

	// Unmarshal into a generic map; handles both YAML and JSON.
	var spec map[string]interface{}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return "", fmt.Errorf("parse spec: %w", err)
	}

	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("no 'paths' found in OpenAPI spec")
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	count := 0
	for rawPath, rawMethods := range paths {
		methods, ok := rawMethods.(map[string]interface{})
		if !ok {
			continue
		}
		for method, rawOp := range methods {
			method = strings.ToUpper(method)
			op, ok := rawOp.(map[string]interface{})
			if !ok {
				continue
			}

			status, body := extractOpenAPIResponse(op)
			route := MockRoute{
				Method:     method,
				Path:       rawPath,
				StatusCode: status,
				Headers:    map[string]string{"Content-Type": "application/json"},
				Body:       body,
			}

			replaced := false
			for i, r := range ms.routes {
				if strings.EqualFold(r.Method, route.Method) && r.Path == route.Path {
					ms.routes[i] = route
					replaced = true
					break
				}
			}
			if !replaced {
				ms.routes = append(ms.routes, route)
			}
			count++
		}
	}

	if err := ms.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return fmt.Sprintf("OpenAPI spec loaded from %s: %d routes generated", filepath.Base(specPath), count), nil
}

// ImportFromRecordings converts all captured recordings into permanent routes.
func (ms *MockServer) ImportFromRecordings() (string, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if len(ms.recordings) == 0 {
		return "No recordings to import", nil
	}

	imported := 0
	for _, rec := range ms.recordings {
		route := MockRoute{
			Method:     rec.Method,
			Path:       rec.Path,
			StatusCode: rec.ResponseStatus,
			Headers:    rec.ResponseHeaders,
			Body:       rec.ResponseBody,
		}
		replaced := false
		for i, r := range ms.routes {
			if strings.EqualFold(r.Method, route.Method) && r.Path == route.Path {
				ms.routes[i] = route
				replaced = true
				break
			}
		}
		if !replaced {
			ms.routes = append(ms.routes, route)
		}
		imported++
	}
	ms.recordings = nil

	if err := ms.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return fmt.Sprintf("Imported %d recordings as permanent routes", imported), nil
}

// ---------------------------------------------------------------------------
// Internal — HTTP handler
// ---------------------------------------------------------------------------

// handleMockRequest is the single catch-all handler for all incoming requests.
func (ms *MockServer) handleMockRequest(w http.ResponseWriter, r *http.Request) {
	ms.mu.Lock()
	route := ms.matchRoute(r.Method, r.URL.Path)
	recording := ms.recording
	ms.mu.Unlock()

	if route == nil {
		if recording {
			ms.recordRequest(r, 404, nil, "")
		}
		http.NotFound(w, r)
		return
	}

	// Simulate network latency.
	if route.Delay > 0 {
		time.Sleep(route.Delay)
	}

	// Write custom headers first.
	for k, v := range route.Headers {
		w.Header().Set(k, v)
	}
	// Default Content-Type if not set by the route.
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}

	body := route.Body
	if route.Dynamic {
		rendered, err := renderTemplate(body, r)
		if err != nil {
			log.Printf("[mock] template error for %s %s: %v", route.Method, route.Path, err)
		} else {
			body = rendered
		}
	}

	w.WriteHeader(route.StatusCode)
	if _, err := w.Write([]byte(body)); err != nil {
		log.Printf("[mock] write error: %v", err)
	}
}

// matchRoute returns the first route whose method and path pattern match the
// request. Path params (:id, :name, …) act as wildcards for a single segment.
// A literal "*" at the end of a pattern matches any remaining path.
func (ms *MockServer) matchRoute(method, path string) *MockRoute {
	method = strings.ToUpper(method)
	for i := range ms.routes {
		r := &ms.routes[i]
		if !strings.EqualFold(r.Method, method) {
			continue
		}
		if pathMatches(r.Path, path) {
			return r
		}
	}
	return nil
}

// pathMatches returns true when pattern matches the concrete path.
// Segments that start with ':' match any single non-empty segment.
// A trailing "/*" matches anything.
func pathMatches(pattern, path string) bool {
	if pattern == path {
		return true
	}

	// Strip trailing slashes for comparison, but keep at least "/".
	cleanPat := strings.TrimRight(pattern, "/")
	cleanPath := strings.TrimRight(path, "/")
	if cleanPat == "" {
		cleanPat = "/"
	}
	if cleanPath == "" {
		cleanPath = "/"
	}

	patParts := strings.Split(cleanPat, "/")
	pathParts := strings.Split(cleanPath, "/")

	// Handle trailing wildcard: pattern ends with * segment.
	if len(patParts) > 0 && patParts[len(patParts)-1] == "*" {
		patParts = patParts[:len(patParts)-1]
		if len(pathParts) < len(patParts) {
			return false
		}
		pathParts = pathParts[:len(patParts)]
	}

	if len(patParts) != len(pathParts) {
		return false
	}
	for i, seg := range patParts {
		if seg == pathParts[i] {
			continue
		}
		if strings.HasPrefix(seg, ":") && pathParts[i] != "" {
			continue // path param wildcard
		}
		return false
	}
	return true
}

// recordRequest appends a recording entry (called while NOT holding ms.mu).
func (ms *MockServer) recordRequest(r *http.Request, respStatus int, respHeaders map[string]string, respBody string) {
	reqHeaders := make(map[string]string, len(r.Header))
	for k, vv := range r.Header {
		reqHeaders[k] = strings.Join(vv, ", ")
	}

	var reqBody string
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // cap at 1 MB
		reqBody = string(b)
	}

	rec := MockRecording{
		Method:          strings.ToUpper(r.Method),
		Path:            r.URL.Path,
		RequestHeaders:  reqHeaders,
		RequestBody:     reqBody,
		ResponseStatus:  respStatus,
		ResponseHeaders: respHeaders,
		ResponseBody:    respBody,
		Timestamp:       time.Now().UTC(),
	}

	ms.mu.Lock()
	ms.recordings = append(ms.recordings, rec)
	ms.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Internal — persistence
// ---------------------------------------------------------------------------

func (ms *MockServer) loadConfig() error {
	if ms.configPath == "" {
		return nil
	}
	data, err := os.ReadFile(ms.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read mock config: %w", err)
	}
	var cfg MockServerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse mock config: %w", err)
	}
	ms.port = cfg.Port
	ms.routes = cfg.Routes
	ms.recordings = cfg.Recordings
	return nil
}

func (ms *MockServer) saveConfig() error {
	if ms.configPath == "" {
		return nil
	}
	cfg := MockServerConfig{
		Port:       ms.port,
		Routes:     ms.routes,
		Recordings: ms.recordings,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mock config: %w", err)
	}
	if err := os.WriteFile(ms.configPath, data, 0600); err != nil {
		return fmt.Errorf("write mock config: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal — presets
// ---------------------------------------------------------------------------

func (ms *MockServer) getPreset(name string) (*MockPreset, error) {
	switch strings.ToLower(name) {
	case "stripe":
		return stripePreset(), nil
	case "openai":
		return openaiPreset(), nil
	case "twilio":
		return twilioPreset(), nil
	case "github":
		return githubPreset(), nil
	case "supabase-auth":
		return supabaseAuthPreset(), nil
	default:
		return nil, fmt.Errorf("unknown preset %q; available: stripe, openai, twilio, github, supabase-auth", name)
	}
}

func jsonHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

func stripePreset() *MockPreset {
	return &MockPreset{
		Name:        "stripe",
		Description: "Common Stripe API v1 endpoints",
		Routes: []MockRoute{
			{
				Method:     "POST",
				Path:       "/v1/charges",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"ch_mock_123","object":"charge","amount":2000,"currency":"usd","status":"succeeded","created":1712000000,"livemode":false,"paid":true,"refunded":false,"description":"Mock charge","failure_code":null,"failure_message":null}`,
			},
			{
				Method:     "POST",
				Path:       "/v1/customers",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"cus_mock_456","object":"customer","email":"mock@example.com","created":1712000000,"livemode":false,"description":"Mock customer","currency":"usd","delinquent":false}`,
			},
			{
				Method:     "GET",
				Path:       "/v1/balance",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"object":"balance","available":[{"amount":99500,"currency":"usd","source_types":{"card":99500}}],"pending":[{"amount":500,"currency":"usd","source_types":{"card":500}}],"livemode":false}`,
			},
			{
				Method:     "POST",
				Path:       "/v1/payment_intents",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"pi_mock_789","object":"payment_intent","amount":2000,"currency":"usd","status":"requires_payment_method","created":1712000000,"livemode":false,"client_secret":"pi_mock_789_secret_mock","confirmation_method":"automatic"}`,
			},
			{
				Method:     "GET",
				Path:       "/v1/customers/:id",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"cus_mock_456","object":"customer","email":"mock@example.com","created":1712000000,"livemode":false}`,
			},
			{
				Method:     "POST",
				Path:       "/v1/refunds",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"re_mock_111","object":"refund","amount":2000,"currency":"usd","status":"succeeded","charge":"ch_mock_123","created":1712000000}`,
			},
		},
	}
}

func openaiPreset() *MockPreset {
	return &MockPreset{
		Name:        "openai",
		Description: "OpenAI v1 API endpoints",
		Routes: []MockRoute{
			{
				Method:     "GET",
				Path:       "/v1/models",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"object":"list","data":[{"id":"gpt-4o","object":"model","created":1712000000,"owned_by":"openai"},{"id":"gpt-4o-mini","object":"model","created":1712000000,"owned_by":"openai"},{"id":"gpt-3.5-turbo","object":"model","created":1712000000,"owned_by":"openai"}]}`,
			},
			{
				Method:     "POST",
				Path:       "/v1/chat/completions",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"chatcmpl-mock123","object":"chat.completion","created":1712000000,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"This is a mock response from the OpenAI API."},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":14,"total_tokens":26}}`,
			},
			{
				Method:     "POST",
				Path:       "/v1/embeddings",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.0023064255,-0.009327292,-0.0028842222]}],"model":"text-embedding-ada-002","usage":{"prompt_tokens":8,"total_tokens":8}}`,
			},
			{
				Method:     "POST",
				Path:       "/v1/completions",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"cmpl-mock456","object":"text_completion","created":1712000000,"model":"gpt-3.5-turbo-instruct","choices":[{"text":"\n\nThis is a mock completion.","index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`,
			},
		},
	}
}

func twilioPreset() *MockPreset {
	return &MockPreset{
		Name:        "twilio",
		Description: "Twilio REST API endpoints",
		Routes: []MockRoute{
			{
				Method:     "POST",
				Path:       "/2010-04-01/Accounts/:accountSid/Messages.json",
				StatusCode: 201,
				Headers:    jsonHeaders(),
				Body:       `{"sid":"SM_mock_abc123","account_sid":"AC_mock","to":"+15551234567","from":"+15559876543","body":"Mock SMS message","status":"queued","direction":"outbound-api","date_created":"Sat, 13 Apr 2024 00:00:00 +0000","date_updated":"Sat, 13 Apr 2024 00:00:00 +0000","price":null,"error_code":null,"error_message":null,"uri":"/2010-04-01/Accounts/AC_mock/Messages/SM_mock_abc123.json"}`,
			},
			{
				Method:     "GET",
				Path:       "/2010-04-01/Accounts/:accountSid/Messages/:messageSid.json",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"sid":"SM_mock_abc123","account_sid":"AC_mock","to":"+15551234567","from":"+15559876543","body":"Mock SMS message","status":"delivered","direction":"outbound-api","date_created":"Sat, 13 Apr 2024 00:00:00 +0000","date_updated":"Sat, 13 Apr 2024 00:00:01 +0000"}`,
			},
			{
				Method:     "GET",
				Path:       "/2010-04-01/Accounts/:accountSid.json",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"sid":"AC_mock","friendly_name":"Mock Account","status":"active","type":"Trial","date_created":"Mon, 01 Jan 2024 00:00:00 +0000"}`,
			},
		},
	}
}

func githubPreset() *MockPreset {
	return &MockPreset{
		Name:        "github",
		Description: "GitHub REST API v3 endpoints",
		Routes: []MockRoute{
			{
				Method:     "GET",
				Path:       "/repos/:owner/:repo",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":123456789,"name":"mock-repo","full_name":"mock-owner/mock-repo","private":false,"owner":{"login":"mock-owner","id":1,"type":"User"},"description":"A mock repository","fork":false,"html_url":"https://github.com/mock-owner/mock-repo","clone_url":"https://github.com/mock-owner/mock-repo.git","stargazers_count":42,"watchers_count":42,"forks_count":7,"open_issues_count":3,"default_branch":"main","visibility":"public"}`,
			},
			{
				Method:     "GET",
				Path:       "/user",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"login":"mock-user","id":1,"name":"Mock User","email":"mock@example.com","public_repos":10,"followers":5,"following":3,"created_at":"2020-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`,
			},
			{
				Method:     "GET",
				Path:       "/repos/:owner/:repo/issues",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `[{"id":1,"number":1,"title":"Mock issue","state":"open","user":{"login":"mock-user"},"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","body":"This is a mock issue."}]`,
			},
			{
				Method:     "POST",
				Path:       "/repos/:owner/:repo/issues",
				StatusCode: 201,
				Headers:    jsonHeaders(),
				Body:       `{"id":2,"number":2,"title":"New mock issue","state":"open","user":{"login":"mock-user"},"created_at":"2024-04-13T00:00:00Z","updated_at":"2024-04-13T00:00:00Z"}`,
			},
			{
				Method:     "GET",
				Path:       "/repos/:owner/:repo/pulls",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `[{"id":1,"number":1,"title":"Mock PR","state":"open","user":{"login":"mock-user"},"head":{"ref":"feature-branch","sha":"abc123"},"base":{"ref":"main","sha":"def456"},"created_at":"2024-01-01T00:00:00Z"}]`,
			},
		},
	}
}

func supabaseAuthPreset() *MockPreset {
	return &MockPreset{
		Name:        "supabase-auth",
		Description: "Supabase Auth API endpoints",
		Routes: []MockRoute{
			{
				Method:     "POST",
				Path:       "/auth/v1/signup",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"access_token":"mock_access_token_eyJhbGciOiJIUzI1NiJ9.mock","token_type":"bearer","expires_in":3600,"refresh_token":"mock_refresh_token_abc123","user":{"id":"mock-user-uuid-1234","aud":"authenticated","role":"authenticated","email":"mock@example.com","created_at":"2024-04-13T00:00:00Z","updated_at":"2024-04-13T00:00:00Z","confirmation_sent_at":"2024-04-13T00:00:00Z"}}`,
			},
			{
				Method:     "POST",
				Path:       "/auth/v1/token",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"access_token":"mock_access_token_eyJhbGciOiJIUzI1NiJ9.mock","token_type":"bearer","expires_in":3600,"refresh_token":"mock_refresh_token_xyz789","user":{"id":"mock-user-uuid-1234","aud":"authenticated","role":"authenticated","email":"mock@example.com","created_at":"2024-04-13T00:00:00Z","updated_at":"2024-04-13T00:00:00Z"}}`,
			},
			{
				Method:     "POST",
				Path:       "/auth/v1/logout",
				StatusCode: 204,
				Headers:    jsonHeaders(),
				Body:       "",
			},
			{
				Method:     "GET",
				Path:       "/auth/v1/user",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{"id":"mock-user-uuid-1234","aud":"authenticated","role":"authenticated","email":"mock@example.com","created_at":"2024-04-13T00:00:00Z","updated_at":"2024-04-13T00:00:00Z"}`,
			},
			{
				Method:     "POST",
				Path:       "/auth/v1/recover",
				StatusCode: 200,
				Headers:    jsonHeaders(),
				Body:       `{}`,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Internal — OpenAPI helper
// ---------------------------------------------------------------------------

// extractOpenAPIResponse walks the responses section of an OpenAPI operation
// and returns the first available HTTP status code and a JSON body string.
// It checks, in order: response examples → schema example → schema default → "{}".
func extractOpenAPIResponse(op map[string]interface{}) (int, string) {
	responses, ok := op["responses"].(map[string]interface{})
	if !ok {
		return 200, "{}"
	}

	// Prefer 200, then 201, then the first key we find.
	preferredCodes := []string{"200", "201", "204", "400", "404", "500"}
	var bestCode string
	var bestResp map[string]interface{}

	for _, code := range preferredCodes {
		if v, ok := responses[code]; ok {
			if m, ok := v.(map[string]interface{}); ok {
				bestCode = code
				bestResp = m
				break
			}
		}
	}
	if bestResp == nil {
		for code, v := range responses {
			if m, ok := v.(map[string]interface{}); ok {
				bestCode = code
				bestResp = m
				break
			}
		}
	}

	status := 200
	if bestCode != "" {
		fmt.Sscanf(bestCode, "%d", &status)
	}
	if status == 204 {
		return status, ""
	}

	body := extractResponseBody(bestResp)
	return status, body
}

// extractResponseBody tries to get a JSON body from an OpenAPI response object.
func extractResponseBody(resp map[string]interface{}) string {
	if resp == nil {
		return "{}"
	}

	// Check content → application/json → example or examples or schema.
	content, ok := resp["content"].(map[string]interface{})
	if !ok {
		return "{}"
	}
	mediaType, ok := content["application/json"].(map[string]interface{})
	if !ok {
		// Fall back to any media type.
		for _, v := range content {
			if m, ok := v.(map[string]interface{}); ok {
				mediaType = m
				break
			}
		}
	}
	if mediaType == nil {
		return "{}"
	}

	// Inline example.
	if ex := mediaType["example"]; ex != nil {
		if b, err := json.Marshal(ex); err == nil {
			return string(b)
		}
	}

	// Named examples — take first.
	if examples, ok := mediaType["examples"].(map[string]interface{}); ok {
		for _, v := range examples {
			if m, ok := v.(map[string]interface{}); ok {
				if val := m["value"]; val != nil {
					if b, err := json.Marshal(val); err == nil {
						return string(b)
					}
				}
			}
		}
	}

	// Schema with example.
	if schema, ok := mediaType["schema"].(map[string]interface{}); ok {
		if ex := schema["example"]; ex != nil {
			if b, err := json.Marshal(ex); err == nil {
				return string(b)
			}
		}
		// Generate a minimal skeleton from the schema properties.
		if generated := generateFromSchema(schema); generated != "" {
			return generated
		}
	}

	return "{}"
}

// generateFromSchema creates a minimal JSON object from an OpenAPI schema.
// It only handles the common case of object schemas with named properties.
func generateFromSchema(schema map[string]interface{}) string {
	schemaType, _ := schema["type"].(string)
	if schemaType == "array" {
		return "[]"
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok || len(props) == 0 {
		return "{}"
	}

	obj := make(map[string]interface{}, len(props))
	for name, rawProp := range props {
		prop, ok := rawProp.(map[string]interface{})
		if !ok {
			obj[name] = nil
			continue
		}
		if ex := prop["example"]; ex != nil {
			obj[name] = ex
			continue
		}
		switch prop["type"] {
		case "string":
			obj[name] = ""
		case "integer", "number":
			obj[name] = 0
		case "boolean":
			obj[name] = false
		case "array":
			obj[name] = []interface{}{}
		default:
			obj[name] = nil
		}
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Internal — template rendering
// ---------------------------------------------------------------------------

// templateData is passed to dynamic route templates.
type templateData struct {
	Method  string
	Path    string
	Query   map[string]string
	Headers map[string]string
}

// renderTemplate executes a Go text/template body against the incoming request.
func renderTemplate(body string, r *http.Request) (string, error) {
	tmpl, err := template.New("mock").Parse(body)
	if err != nil {
		return body, fmt.Errorf("parse template: %w", err)
	}

	query := make(map[string]string, len(r.URL.Query()))
	for k, vv := range r.URL.Query() {
		if len(vv) > 0 {
			query[k] = vv[0]
		}
	}
	headers := make(map[string]string, len(r.Header))
	for k, vv := range r.Header {
		headers[k] = strings.Join(vv, ", ")
	}

	data := templateData{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   query,
		Headers: headers,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return body, fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}
