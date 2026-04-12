package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StorageStatus holds the current state of the MinIO storage service.
type StorageStatus struct {
	Running    bool   `json:"running"`
	Endpoint   string `json:"endpoint"`
	ConsoleURL string `json:"console_url"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	Buckets    int    `json:"buckets"`
	TotalSize  string `json:"total_size"`
}

// BucketInfo describes a single MinIO bucket.
type BucketInfo struct {
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	ObjectCount int       `json:"object_count"`
	Size        string    `json:"size"`
}

// ObjectInfo describes a single object stored in a bucket.
type ObjectInfo struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
	ContentType  string    `json:"content_type"`
}

// StorageManager manages a local MinIO instance (Docker-first, binary fallback).
type StorageManager struct {
	mu          sync.Mutex
	cmd         *exec.Cmd // non-nil when running as a binary process
	port        int
	consolePort int
	dataDir     string
	accessKey   string
	secretKey   string
	useDocker   bool
}

// NewStorageManager returns a StorageManager with sensible defaults.
func NewStorageManager() *StorageManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return &StorageManager{
		port:        9000,
		consolePort: 9001,
		dataDir:     filepath.Join(home, ".yaver", "minio-data"),
		accessKey:   "minioadmin",
		secretKey:   "minioadmin",
	}
}

// Start launches MinIO. Docker is tried first; if unavailable, falls back to
// the minio binary on PATH. Returns the endpoint URL and informational text.
func (s *StorageManager) Start(port int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if port > 0 {
		s.port = port
	}

	if s.isRunning() {
		return fmt.Sprintf("MinIO already running at http://localhost:%d", s.port), nil
	}

	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}

	// Try Docker first.
	if dockerAvailable() {
		if err := s.startDocker(); err == nil {
			s.useDocker = true
			return fmt.Sprintf("MinIO started via Docker → http://localhost:%d (console http://localhost:%d)", s.port, s.consolePort), nil
		}
	}

	// Fall back to minio binary.
	if err := s.startBinary(); err != nil {
		return "", fmt.Errorf("start MinIO (tried Docker and binary): %w", err)
	}
	s.useDocker = false
	return fmt.Sprintf("MinIO started via binary → http://localhost:%d (console http://localhost:%d)", s.port, s.consolePort), nil
}

// Stop terminates the MinIO container or process.
func (s *StorageManager) Stop() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning() {
		return "MinIO is not running", nil
	}

	if s.useDocker {
		out, err := storageRunCmd("docker", "rm", "-f", "yaver-minio")
		if err != nil {
			return "", fmt.Errorf("docker rm yaver-minio: %w — %s", err, out)
		}
		return "MinIO container stopped and removed", nil
	}

	if s.cmd != nil && s.cmd.Process != nil {
		if err := s.cmd.Process.Kill(); err != nil {
			return "", fmt.Errorf("kill minio process: %w", err)
		}
		_ = s.cmd.Wait()
		s.cmd = nil
		return "MinIO process stopped", nil
	}

	return "MinIO stopped", nil
}

// Status returns the current operational status of MinIO.
func (s *StorageManager) Status() (*StorageStatus, error) {
	running := s.isRunning()
	st := &StorageStatus{
		Running:    running,
		Endpoint:   fmt.Sprintf("http://localhost:%d", s.port),
		ConsoleURL: fmt.Sprintf("http://localhost:%d", s.consolePort),
		AccessKey:  s.accessKey,
		SecretKey:  s.secretKey,
	}

	if !running {
		return st, nil
	}

	buckets, err := s.ListBuckets()
	if err == nil {
		st.Buckets = len(buckets)
		var totalBytes int64
		for _, b := range buckets {
			_ = b // size aggregation via mc is done below
		}
		_ = totalBytes
		// Attempt aggregate size via mc.
		if s.mcAvailable() {
			if err2 := s.ensureAlias(); err2 == nil {
				out, err3 := storageRunCmd("mc", "du", "--quiet", "yaver")
				if err3 == nil {
					parts := strings.Fields(out)
					if len(parts) > 0 {
						st.TotalSize = parts[0]
					}
				}
			}
		}
	}

	return st, nil
}

// CreateBucket creates a new bucket. The name must be a valid S3 bucket name.
func (s *StorageManager) CreateBucket(name string) (string, error) {
	if err := validateBucketName(name); err != nil {
		return "", err
	}

	if s.mcAvailable() {
		if err := s.ensureAlias(); err != nil {
			return "", err
		}
		out, err := storageRunCmd("mc", "mb", "--ignore-existing", "yaver/"+name)
		if err != nil {
			return "", fmt.Errorf("mc mb: %w — %s", err, out)
		}
		return fmt.Sprintf("Bucket %q created (or already exists)", name), nil
	}

	// Fall back to plain HTTP PUT (S3-compatible API, no auth for local MinIO by default).
	url := fmt.Sprintf("http://localhost:%d/%s", s.port, name)
	req, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		return "", fmt.Errorf("build PUT request: %w", err)
	}
	addBasicAuth(req, s.accessKey, s.secretKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT bucket: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("PUT bucket returned %d: %s", resp.StatusCode, body)
	}
	return fmt.Sprintf("Bucket %q created", name), nil
}

// ListBuckets returns metadata for all buckets.
func (s *StorageManager) ListBuckets() ([]BucketInfo, error) {
	if s.mcAvailable() {
		if err := s.ensureAlias(); err != nil {
			return nil, err
		}
		out, err := storageRunCmd("mc", "ls", "--json", "yaver")
		if err != nil {
			return nil, fmt.Errorf("mc ls: %w — %s", err, out)
		}
		return parseMcLsBuckets(out), nil
	}

	// Fall back to S3 ListBuckets API.
	url := fmt.Sprintf("http://localhost:%d/", s.port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build GET request: %w", err)
	}
	addBasicAuth(req, s.accessKey, s.secretKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET buckets: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	_ = body
	// Minimal parse: return empty list rather than a broken XML parser dependency.
	return []BucketInfo{}, nil
}

// Upload copies a local file into the specified bucket under the given key.
func (s *StorageManager) Upload(bucket, key, filePath string) (string, error) {
	if bucket == "" || key == "" || filePath == "" {
		return "", fmt.Errorf("bucket, key, and filePath must all be non-empty")
	}
	if _, err := os.Stat(filePath); err != nil {
		return "", fmt.Errorf("source file: %w", err)
	}

	if s.mcAvailable() {
		if err := s.ensureAlias(); err != nil {
			return "", err
		}
		dest := fmt.Sprintf("yaver/%s/%s", bucket, key)
		out, err := storageRunCmd("mc", "cp", filePath, dest)
		if err != nil {
			return "", fmt.Errorf("mc cp: %w — %s", err, out)
		}
		return fmt.Sprintf("Uploaded %s → %s/%s", filePath, bucket, key), nil
	}

	// Fall back to HTTP PUT.
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d/%s/%s", s.port, bucket, key)
	req, err := http.NewRequest(http.MethodPut, url, f)
	if err != nil {
		return "", fmt.Errorf("build PUT request: %w", err)
	}
	req.ContentLength = fi.Size()
	addBasicAuth(req, s.accessKey, s.secretKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT object: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("PUT object returned %d: %s", resp.StatusCode, body)
	}
	return fmt.Sprintf("Uploaded %s → %s/%s", filePath, bucket, key), nil
}

// ListObjects returns the objects in bucket that match the optional prefix.
func (s *StorageManager) ListObjects(bucket, prefix string) ([]ObjectInfo, error) {
	if bucket == "" {
		return nil, fmt.Errorf("bucket must not be empty")
	}

	if s.mcAvailable() {
		if err := s.ensureAlias(); err != nil {
			return nil, err
		}
		target := fmt.Sprintf("yaver/%s", bucket)
		if prefix != "" {
			target += "/" + prefix
		}
		out, err := storageRunCmd("mc", "ls", "--json", "--recursive", target)
		if err != nil {
			return nil, fmt.Errorf("mc ls objects: %w — %s", err, out)
		}
		return parseMcLsObjects(out), nil
	}

	// Minimal HTTP fallback — returns empty list without XML parser dependency.
	return []ObjectInfo{}, nil
}

// Presign generates a pre-signed download URL for the object, valid for expiry.
func (s *StorageManager) Presign(bucket, key string, expiry time.Duration) (string, error) {
	if bucket == "" || key == "" {
		return "", fmt.Errorf("bucket and key must not be empty")
	}
	if expiry <= 0 {
		expiry = 7 * 24 * time.Hour // default: 7 days
	}

	if s.mcAvailable() {
		if err := s.ensureAlias(); err != nil {
			return "", err
		}
		expiryStr := fmt.Sprintf("%ds", int(expiry.Seconds()))
		out, err := storageRunCmd("mc", "share", "download", "--expire", expiryStr,
			fmt.Sprintf("yaver/%s/%s", bucket, key))
		if err != nil {
			return "", fmt.Errorf("mc share download: %w — %s", err, out)
		}
		// mc prints the URL on a line beginning with "Share:"
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Share:") {
				return strings.TrimSpace(strings.TrimPrefix(line, "Share:")), nil
			}
			// Newer mc versions print the URL directly.
			if strings.HasPrefix(line, "http") {
				return line, nil
			}
		}
		return strings.TrimSpace(out), nil
	}

	// Without mc, return a plain (non-signed) URL as best effort.
	return fmt.Sprintf("http://localhost:%d/%s/%s", s.port, bucket, key), nil
}

// Config returns an S3-compatible configuration map suitable for direct SDK use.
func (s *StorageManager) Config() map[string]string {
	return map[string]string{
		"endpoint":       fmt.Sprintf("http://localhost:%d", s.port),
		"accessKey":      s.accessKey,
		"secretKey":      s.secretKey,
		"region":         "us-east-1",
		"forcePathStyle": "true",
	}
}

// Delete removes a single object from a bucket.
func (s *StorageManager) Delete(bucket, key string) (string, error) {
	if bucket == "" || key == "" {
		return "", fmt.Errorf("bucket and key must not be empty")
	}

	if s.mcAvailable() {
		if err := s.ensureAlias(); err != nil {
			return "", err
		}
		out, err := storageRunCmd("mc", "rm", fmt.Sprintf("yaver/%s/%s", bucket, key))
		if err != nil {
			return "", fmt.Errorf("mc rm: %w — %s", err, out)
		}
		return fmt.Sprintf("Deleted %s/%s", bucket, key), nil
	}

	url := fmt.Sprintf("http://localhost:%d/%s/%s", s.port, bucket, key)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return "", fmt.Errorf("build DELETE request: %w", err)
	}
	addBasicAuth(req, s.accessKey, s.secretKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("DELETE object: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("DELETE object returned %d: %s", resp.StatusCode, body)
	}
	return fmt.Sprintf("Deleted %s/%s", bucket, key), nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// mcAvailable reports whether the mc (MinIO Client) binary is on PATH.
func (s *StorageManager) mcAvailable() bool {
	_, err := exec.LookPath("mc")
	return err == nil
}

// ensureAlias configures an mc alias named "yaver" pointing at the local instance.
func (s *StorageManager) ensureAlias() error {
	endpoint := fmt.Sprintf("http://localhost:%d", s.port)
	out, err := storageRunCmd("mc", "alias", "set", "yaver", endpoint, s.accessKey, s.secretKey)
	if err != nil {
		return fmt.Errorf("mc alias set: %w — %s", err, out)
	}
	return nil
}

// isRunning checks whether MinIO is currently listening on its configured port.
// It first checks the Docker container name, then falls back to a TCP dial.
func (s *StorageManager) isRunning() bool {
	// Check Docker container.
	if dockerAvailable() {
		out, err := storageRunCmd("docker", "ps", "--filter", "name=yaver-minio", "--format", "{{.Names}}")
		if err == nil && strings.Contains(out, "yaver-minio") {
			return true
		}
	}
	// Check TCP port.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", s.port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// startDocker starts MinIO as a Docker container.
func (s *StorageManager) startDocker() error {
	args := []string{
		"run", "-d", "--name", "yaver-minio",
		"-p", fmt.Sprintf("%d:9000", s.port),
		"-p", fmt.Sprintf("%d:9001", s.consolePort),
		"-v", "yaver-minio-data:/data",
		"-e", fmt.Sprintf("MINIO_ROOT_USER=%s", s.accessKey),
		"-e", fmt.Sprintf("MINIO_ROOT_PASSWORD=%s", s.secretKey),
		"minio/minio",
		"server", "/data",
		"--console-address", ":9001",
	}
	out, err := storageRunCmd("docker", args...)
	if err != nil {
		return fmt.Errorf("docker run: %w — %s", err, out)
	}
	return s.waitReady(15 * time.Second)
}

// startBinary starts MinIO using the minio binary on PATH.
func (s *StorageManager) startBinary() error {
	minioPath, err := exec.LookPath("minio")
	if err != nil {
		return fmt.Errorf("minio binary not found on PATH (install MinIO or Docker): %w", err)
	}

	cmd := exec.Command(minioPath, "server", s.dataDir,
		"--address", fmt.Sprintf(":%d", s.port),
		"--console-address", fmt.Sprintf(":%d", s.consolePort),
	)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("MINIO_ROOT_USER=%s", s.accessKey),
		fmt.Sprintf("MINIO_ROOT_PASSWORD=%s", s.secretKey),
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start minio binary: %w", err)
	}
	s.cmd = cmd

	return s.waitReady(15 * time.Second)
}

// waitReady polls the MinIO health endpoint until it responds or timeout expires.
func (s *StorageManager) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	healthURL := fmt.Sprintf("http://localhost:%d/minio/health/live", s.port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL) //nolint:noctx
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("MinIO did not become healthy within %s", timeout)
}

// ---------------------------------------------------------------------------
// Package-level helpers
// ---------------------------------------------------------------------------

// dockerAvailable reports whether the docker binary is available.
func dockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// storageRunCmd runs a command and returns its combined stdout+stderr output.
func storageRunCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

// addBasicAuth sets HTTP Basic Auth headers on the request.
func addBasicAuth(req *http.Request, user, pass string) {
	req.SetBasicAuth(user, pass)
}

// validateBucketName enforces basic S3 bucket naming rules.
func validateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("bucket name must be 3–63 characters long")
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '.') {
			return fmt.Errorf("bucket name %q contains invalid character %q (use lowercase letters, numbers, hyphens, dots)", name, ch)
		}
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("bucket name must not start or end with a hyphen")
	}
	return nil
}

// ---------------------------------------------------------------------------
// mc JSON output parsers
// ---------------------------------------------------------------------------

// mcLsEntry is the JSON structure emitted by `mc ls --json`.
type mcLsEntry struct {
	Status       string    `json:"status"`
	Type         string    `json:"type"`   // "folder" for buckets
	Key          string    `json:"key"`
	LastModified time.Time `json:"lastModified"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag"`
	ContentType  string    `json:"contentType"`
}

// parseMcLsBuckets parses newline-delimited JSON from `mc ls --json <alias>`.
func parseMcLsBuckets(raw string) []BucketInfo {
	var result []BucketInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry mcLsEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "folder" {
			continue
		}
		result = append(result, BucketInfo{
			Name:      strings.TrimSuffix(entry.Key, "/"),
			CreatedAt: entry.LastModified,
		})
	}
	return result
}

// parseMcLsObjects parses newline-delimited JSON from `mc ls --json --recursive`.
func parseMcLsObjects(raw string) []ObjectInfo {
	var result []ObjectInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry mcLsEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "folder" {
			continue
		}
		result = append(result, ObjectInfo{
			Key:          entry.Key,
			Size:         entry.Size,
			LastModified: entry.LastModified,
			ContentType:  entry.ContentType,
		})
	}
	return result
}
