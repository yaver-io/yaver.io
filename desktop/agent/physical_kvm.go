package main

// physical_kvm.go is the local-first control plane for the v0 physical-PC kit:
// Raspberry Pi + UVC HDMI capture card + M5Stack AtomS3U USB HID bridge.
//
// No device credential, binding, lease, frame, or action crosses Convex. The
// agent persists one owner-only local binding, probes the actual M5 HTTP
// operation, and exposes a typed API shared by HTTP, ops/MCP, CLI and the
// remote-runtime target. A managed tunnel is transport convenience, never an
// authorization boundary; the target bridge still checks its own random
// challenge-HMAC credential, local arm state, USB mount state, and short lease.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	physicalKVMTargetID   = "physical-pc-kvm"
	physicalKVMProtocol   = "yaver-physical-kvm-v1"
	physicalKVMBeaconPort = 19838
	physicalKVMMaxBody    = 32 << 10
)

type physicalKVMBinding struct {
	DeviceID      string `json:"deviceId"`
	ControllerID  string `json:"controllerId"`
	CaptureDevice string `json:"captureDevice,omitempty"`
	URL           string `json:"url"`
	Token         string `json:"token"`
}

func validatePhysicalKVMCaptureDevice(raw string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(raw))
	if clean == "." || !filepath.IsAbs(clean) || !strings.HasPrefix(clean, "/dev/") {
		return "", fmt.Errorf("KVM_CAPTURE_DEVICE_INVALID: select an advertised absolute path below /dev")
	}
	for _, device := range captureDevices() {
		if path, _ := device["path"].(string); path == clean {
			return clean, nil
		}
	}
	return "", fmt.Errorf("KVM_CAPTURE_DEVICE_NOT_FOUND: %s is not an advertised video input", clean)
}

func (m *physicalKVMManager) selectedCaptureDevice() (string, error) {
	if err := m.load(); err != nil {
		return "", err
	}
	m.mu.Lock()
	configured := ""
	if m.binding != nil {
		configured = m.binding.CaptureDevice
	}
	m.mu.Unlock()
	if configured != "" {
		return validatePhysicalKVMCaptureDevice(configured)
	}
	return automaticCaptureDevice()
}

func (m *physicalKVMManager) selectCaptureDevice(raw string) error {
	selected, err := validatePhysicalKVMCaptureDevice(raw)
	if err != nil {
		return err
	}
	if err := m.load(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.binding == nil {
		m.mu.Unlock()
		return fmt.Errorf("KVM_NOT_PAIRED: pair the input bridge before selecting its capture card")
	}
	binding := *m.binding
	m.mu.Unlock()
	binding.CaptureDevice = selected
	if err := m.saveBinding(binding); err != nil {
		return err
	}
	captureStream.stop()
	return nil
}

type physicalKVMStatus struct {
	OK               bool   `json:"ok"`
	Protocol         string `json:"protocol"`
	Version          string `json:"version,omitempty"`
	DeviceID         string `json:"deviceId,omitempty"`
	IP               string `json:"ip,omitempty"`
	Mode             string `json:"mode,omitempty"`
	PairingState     string `json:"pairingState,omitempty"`
	KeyboardLayout   string `json:"keyboardLayout,omitempty"`
	USBReady         bool   `json:"usbReady"`
	Armed            bool   `json:"armed"`
	LeaseActive      bool   `json:"leaseActive"`
	LeaseRemainingMS uint32 `json:"leaseRemainingMs,omitempty"`
	Code             string `json:"code,omitempty"`
	Error            string `json:"error,omitempty"`
	Remedy           string `json:"remedy,omitempty"`
}

type physicalKVMDiscovery struct {
	Protocol     string `json:"protocol"`
	DeviceID     string `json:"deviceId"`
	Address      string `json:"address"`
	Port         int    `json:"port"`
	Mode         string `json:"mode"`
	PairingState string `json:"pairingState"`
	USBReady     bool   `json:"usbReady"`
	Armed        bool   `json:"armed"`
}

type physicalKVMSession struct {
	ID      string
	LeaseID string
	Seq     uint32
	Opened  time.Time
}

type physicalKVMManager struct {
	mu         sync.Mutex
	actionMu   sync.Mutex
	binding    *physicalKVMBinding
	loaded     bool
	session    physicalKVMSession
	configPath func() (string, error)
	client     *http.Client
}

var physicalKVM = newPhysicalKVMManager()

func newPhysicalKVMManager() *physicalKVMManager {
	m := &physicalKVMManager{}
	m.configPath = func() (string, error) {
		dir, err := yaverDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "physical-kvm.json"), nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = physicalKVMDialContext
	m.client = &http.Client{
		Timeout:   4 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("M5 bridge redirects are not allowed")
		},
	}
	return m
}

func physicalKVMLocalIP(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// Resolve and enforce the LAN boundary on every connection, not only while
// pairing. This closes the DNS-rebinding gap where a hostname could resolve to
// a private address during validation and a public address on the next dial.
func physicalKVMDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid M5 bridge address %q: %w", address, err)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("resolve M5 bridge host %q: %w", host, err)
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	var lastErr error
	for _, addr := range addrs {
		if !physicalKVMLocalIP(addr.IP) {
			return nil, fmt.Errorf("M5 bridge host resolved to non-local address %s", addr.IP)
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("dial M5 bridge %q: %w", address, lastErr)
}

func randomOpaqueID(prefix string, bytesN int) (string, error) {
	b := make([]byte, bytesN)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func (m *physicalKVMManager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded {
		return nil
	}
	path, err := m.configPath()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		m.loaded = true
		return nil
	}
	if err != nil {
		return err
	}
	var binding physicalKVMBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return fmt.Errorf("decode physical KVM binding: %w", err)
	}
	if binding.URL == "" || binding.DeviceID == "" || binding.ControllerID == "" || binding.Token == "" {
		return fmt.Errorf("physical KVM binding is incomplete; run `yaver kvm pair` again")
	}
	if err := validatePhysicalKVMToken(binding.Token); err != nil {
		return fmt.Errorf("physical KVM binding credential is invalid; run `yaver kvm pair` again: %w", err)
	}
	if len(binding.ControllerID) > 128 {
		return fmt.Errorf("physical KVM controller identity is invalid; run `yaver kvm pair` again")
	}
	m.binding = &binding
	m.loaded = true
	return nil
}

func (m *physicalKVMManager) saveBinding(binding physicalKVMBinding) error {
	path, err := m.configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".physical-kvm-*.tmp")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	defer os.Remove(tmp)
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(append(raw, '\n')); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	m.mu.Lock()
	m.binding = &binding
	m.loaded = true
	m.mu.Unlock()
	return nil
}

func validatePhysicalKVMURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("bridge URL must be a local http://host:port URL")
	}
	if u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("bridge URL must not contain a path, query, or fragment")
	}
	host := u.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("resolve bridge host %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := addr.IP
		if !physicalKVMLocalIP(ip) {
			return "", fmt.Errorf("bridge URL resolved to non-local address %s", ip)
		}
	}
	u.Path = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func (m *physicalKVMManager) request(ctx context.Context, method, path string, payload any, out any) error {
	if err := m.load(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.binding == nil {
		m.mu.Unlock()
		return fmt.Errorf("KVM_NOT_PAIRED: no M5Stack input bridge is paired; run `yaver kvm pair`")
	}
	binding := *m.binding
	m.mu.Unlock()
	err := m.requestWith(ctx, binding, method, path, payload, out)
	if err == nil || binding.DeviceID == "" ||
		(!strings.Contains(err.Error(), "KVM_UNREACHABLE") && !strings.Contains(err.Error(), "KVM_CHALLENGE")) {
		return err
	}
	// DHCP may change the bridge address after a router reboot or when a
	// factory-paired kit moves to its installation network. Re-discover the
	// exact bound identity, authenticate it before accepting the new address,
	// and retry once. Sequence enforcement makes a lost action acknowledgement
	// fail closed instead of executing twice.
	refreshed, refreshErr := rediscoverPhysicalKVMBinding(ctx, binding)
	if refreshErr != nil || refreshed.URL == binding.URL {
		return err
	}
	if retryErr := m.requestWith(ctx, refreshed, method, path, payload, out); retryErr != nil {
		return retryErr
	}
	if saveErr := m.saveBinding(refreshed); saveErr != nil {
		return fmt.Errorf("KVM_ADDRESS_REFRESH_SAVE_FAILED: %w", saveErr)
	}
	return nil
}

func rediscoverPhysicalKVMBinding(ctx context.Context, binding physicalKVMBinding) (physicalKVMBinding, error) {
	discoverCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	devices, err := discoverPhysicalKVM(discoverCtx)
	if err != nil {
		return binding, err
	}
	var match *physicalKVMDiscovery
	for i := range devices {
		if devices[i].DeviceID != binding.DeviceID {
			continue
		}
		if match != nil {
			return binding, fmt.Errorf("KVM_DISCOVERY_AMBIGUOUS: device %s announced more than one address", binding.DeviceID)
		}
		match = &devices[i]
	}
	if match == nil {
		return binding, fmt.Errorf("KVM_DISCOVERY_NOT_FOUND: paired device %s did not announce", binding.DeviceID)
	}
	port := match.Port
	if port == 0 {
		port = 8348
	}
	refreshedURL, err := validatePhysicalKVMURL("http://" + net.JoinHostPort(match.Address, strconv.Itoa(port)))
	if err != nil {
		return binding, err
	}
	binding.URL = refreshedURL
	return binding, nil
}

func (m *physicalKVMManager) requestWith(ctx context.Context, binding physicalKVMBinding, method, path string, payload any, out any) error {
	var raw []byte
	var body io.Reader
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	nonce, err := m.challenge(ctx, binding)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, binding.URL+path, body)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	m.signRequest(req, binding, nonce, hex.EncodeToString(digest[:]))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("KVM_UNREACHABLE: M5Stack did not answer: %w", err)
	}
	defer resp.Body.Close()
	respRaw, err := io.ReadAll(io.LimitReader(resp.Body, physicalKVMMaxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure physicalKVMStatus
		_ = json.Unmarshal(respRaw, &failure)
		message := strings.TrimSpace(failure.Error)
		if message == "" {
			message = strings.TrimSpace(string(respRaw))
		}
		if failure.Code != "" {
			message = failure.Code + ": " + message
		}
		if failure.Remedy != "" {
			message += " Remedy: " + failure.Remedy
		}
		return fmt.Errorf("%s", message)
	}
	if out != nil && len(respRaw) > 0 {
		if err := json.Unmarshal(respRaw, out); err != nil {
			return fmt.Errorf("decode M5Stack reply: %w", err)
		}
	}
	return nil
}

func (m *physicalKVMManager) challenge(ctx context.Context, binding physicalKVMBinding) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binding.URL+"/v1/challenge", nil)
	if err != nil {
		return "", err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("KVM_UNREACHABLE: M5Stack challenge failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("KVM_CHALLENGE_FAILED: bridge returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, physicalKVMMaxBody)).Decode(&result); err != nil || len(result.Nonce) < 32 {
		return "", fmt.Errorf("KVM_CHALLENGE_INVALID: bridge returned no usable nonce")
	}
	return result.Nonce, nil
}

func (m *physicalKVMManager) signRequest(req *http.Request, binding physicalKVMBinding, nonce, bodyHash string) {
	canonical := nonce + "\n" + binding.ControllerID + "\n" + req.Method + "\n" + req.URL.Path + "\n" + bodyHash
	mac := hmac.New(sha256.New, []byte(binding.Token))
	_, _ = mac.Write([]byte(canonical))
	req.Header.Set("X-Yaver-Nonce", nonce)
	req.Header.Set("X-Yaver-Controller-ID", binding.ControllerID)
	req.Header.Set("X-Yaver-Signature", hex.EncodeToString(mac.Sum(nil)))
}

func (m *physicalKVMManager) status(ctx context.Context) (physicalKVMStatus, error) {
	var status physicalKVMStatus
	err := m.request(ctx, http.MethodGet, "/v1/status", nil, &status)
	if err == nil && status.Protocol != physicalKVMProtocol {
		return status, fmt.Errorf("KVM_PROTOCOL_MISMATCH: bridge speaks %q, want %q", status.Protocol, physicalKVMProtocol)
	}
	return status, err
}

func validatePhysicalKVMToken(raw string) error {
	token := strings.TrimSpace(raw)
	if len(token) != 64 {
		return fmt.Errorf("pairing token must be a 32-byte hex credential")
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("pairing token must be a 32-byte hex credential")
	}
	return nil
}

func (m *physicalKVMManager) pair(ctx context.Context, rawURL, token string) (physicalKVMStatus, error) {
	baseURL, err := validatePhysicalKVMURL(rawURL)
	if err != nil {
		return physicalKVMStatus{}, err
	}
	if err := validatePhysicalKVMToken(token); err != nil {
		return physicalKVMStatus{}, err
	}
	binding := physicalKVMBinding{URL: baseURL, Token: strings.TrimSpace(token)}
	binding.ControllerID = strings.TrimSpace(localDeviceID())
	if binding.ControllerID == "" || len(binding.ControllerID) > 128 {
		binding.ControllerID, err = randomOpaqueID("pi-", 16)
		if err != nil {
			return physicalKVMStatus{}, err
		}
	}
	var status physicalKVMStatus
	if err := m.requestWith(ctx, binding, http.MethodPost, "/v1/pair", map[string]any{"controllerId": binding.ControllerID}, &status); err != nil {
		return status, err
	}
	if status.Protocol != physicalKVMProtocol || status.DeviceID == "" || status.PairingState != "paired" {
		return status, fmt.Errorf("the device did not identify as a Yaver physical KVM bridge")
	}
	binding.DeviceID = status.DeviceID
	if err := m.saveBinding(binding); err != nil {
		return status, err
	}
	return status, nil
}

func (m *physicalKVMManager) unpair(ctx context.Context) error {
	// Fail closed on both sides before forgetting the credential locally.
	if err := m.request(ctx, http.MethodPost, "/v1/unpair", map[string]any{}, nil); err != nil {
		return fmt.Errorf("remote bridge did not confirm unpair; press its button and retry so local and device state cannot drift: %w", err)
	}
	path, err := m.configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	m.mu.Lock()
	m.binding = nil
	m.loaded = true
	m.session = physicalKVMSession{}
	m.mu.Unlock()
	return nil
}

func (m *physicalKVMManager) open(ctx context.Context) (string, error) {
	nonce, err := randomOpaqueID("nonce-", 16)
	if err != nil {
		return "", err
	}
	var reply struct {
		OK      bool   `json:"ok"`
		LeaseID string `json:"leaseId"`
		Nonce   string `json:"nonce"`
	}
	if err := m.request(ctx, http.MethodPost, "/v1/session/open", map[string]any{"nonce": nonce}, &reply); err != nil {
		return "", err
	}
	if reply.Nonce != nonce || reply.LeaseID == "" {
		return "", fmt.Errorf("KVM_NONCE_FAILED: input bridge did not prove a fresh session")
	}
	sessionID, err := randomOpaqueID("kvm-", 16)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.session = physicalKVMSession{ID: sessionID, LeaseID: reply.LeaseID, Opened: time.Now()}
	m.mu.Unlock()
	return sessionID, nil
}

func (m *physicalKVMManager) action(ctx context.Context, sessionID string, action map[string]any) (uint32, error) {
	// Serialize assignment and delivery. If two requests were allowed to leave
	// concurrently, sequence 2 could reach the bridge before sequence 1 and
	// make the otherwise-valid first action look like a replay.
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	m.mu.Lock()
	if m.session.ID == "" || sessionID != m.session.ID {
		m.mu.Unlock()
		return 0, fmt.Errorf("KVM_LEASE_REQUIRED: acquire a fresh control lease")
	}
	m.session.Seq++
	seq := m.session.Seq
	action["leaseId"] = m.session.LeaseID
	action["sequence"] = seq
	m.mu.Unlock()
	var reply struct {
		Sequence uint32 `json:"sequence"`
	}
	if err := m.request(ctx, http.MethodPost, "/v1/action", action, &reply); err != nil {
		return 0, err
	}
	if reply.Sequence != seq {
		return 0, fmt.Errorf("KVM_ACK_MISMATCH: bridge acknowledged sequence %d, want %d", reply.Sequence, seq)
	}
	return seq, nil
}

func (m *physicalKVMManager) actionCurrent(ctx context.Context, action map[string]any) error {
	m.mu.Lock()
	sessionID := m.session.ID
	m.mu.Unlock()
	if sessionID == "" {
		var err error
		sessionID, err = m.open(ctx)
		if err != nil {
			return err
		}
	}
	if _, err := m.action(ctx, sessionID, action); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "KVM_LEASE_REQUIRED") {
		return err
	}
	// The firmware lease is deliberately shorter than a human pause. Re-open
	// after expiry and retry exactly once; a stale action is never replayed by
	// the bridge because the new lease starts its own sequence space.
	sessionID, err := m.open(ctx)
	if err != nil {
		return err
	}
	_, err = m.action(ctx, sessionID, action)
	return err
}

func (m *physicalKVMManager) heartbeat(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	if sessionID != m.session.ID || m.session.LeaseID == "" {
		m.mu.Unlock()
		return fmt.Errorf("KVM_LEASE_REQUIRED: acquire a fresh control lease")
	}
	leaseID := m.session.LeaseID
	m.mu.Unlock()
	return m.request(ctx, http.MethodPost, "/v1/session/heartbeat", map[string]any{"leaseId": leaseID}, nil)
}

func (m *physicalKVMManager) close(ctx context.Context) error {
	err := m.request(ctx, http.MethodPost, "/v1/session/close", map[string]any{}, nil)
	m.mu.Lock()
	m.session = physicalKVMSession{}
	m.mu.Unlock()
	return err
}

func (m *physicalKVMManager) releaseAll(ctx context.Context) error {
	err := m.request(ctx, http.MethodPost, "/v1/release-all", map[string]any{}, nil)
	m.mu.Lock()
	m.session = physicalKVMSession{}
	m.mu.Unlock()
	return err
}

func (m *physicalKVMManager) installFirmware(ctx context.Context, image []byte) (string, error) {
	if len(image) < 64<<10 || len(image) > 4<<20 {
		return "", fmt.Errorf("KVM_FIRMWARE_SIZE_INVALID: OTA image must be between 64 KiB and 4 MiB")
	}
	if err := m.load(); err != nil {
		return "", err
	}
	m.mu.Lock()
	if m.binding == nil {
		m.mu.Unlock()
		return "", fmt.Errorf("KVM_NOT_PAIRED: pair the input bridge first")
	}
	binding := *m.binding
	m.mu.Unlock()
	digest := sha256.Sum256(image)
	checksum := hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(binding.Token))
	_, _ = mac.Write([]byte(checksum))
	nonce, err := m.challenge(ctx, binding)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.URL+"/v1/firmware", bytes.NewReader(image))
	if err != nil {
		return "", err
	}
	m.signRequest(req, binding, nonce, checksum)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Yaver-SHA256", checksum)
	req.Header.Set("X-Yaver-HMAC", hex.EncodeToString(mac.Sum(nil)))
	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("KVM_FIRMWARE_UNREACHABLE: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, physicalKVMMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("KVM_FIRMWARE_UPDATE_FAILED: %s", strings.TrimSpace(string(raw)))
	}
	return checksum, nil
}

func discoverPhysicalKVM(ctx context.Context) ([]physicalKVMDiscovery, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: physicalKVMBeaconPort})
	if err != nil {
		return nil, fmt.Errorf("listen for M5Stack discovery beacons: %w", err)
	}
	defer conn.Close()
	seen := map[string]physicalKVMDiscovery{}
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, _, readErr := conn.ReadFromUDP(buf)
		if readErr == nil {
			var item physicalKVMDiscovery
			if json.Unmarshal(buf[:n], &item) == nil && item.Protocol == physicalKVMProtocol && item.DeviceID != "" {
				seen[item.DeviceID] = item
			}
		}
		select {
		case <-ctx.Done():
			out := make([]physicalKVMDiscovery, 0, len(seen))
			for _, item := range seen {
				out = append(out, item)
			}
			return out, nil
		default:
		}
	}
}

func (s *HTTPServer) handlePhysicalKVMStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	status, err := physicalKVMFullStatus(r.Context())
	if err != nil {
		status["ok"] = false
		status["code"] = "KVM_NOT_READY"
		status["error"] = err.Error()
		jsonReply(w, http.StatusServiceUnavailable, status)
		return
	}
	status["ok"] = true
	jsonReply(w, http.StatusOK, status)
}

func (s *HTTPServer) handlePhysicalKVMDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	timeout := 2500 * time.Millisecond
	if raw := r.URL.Query().Get("timeoutMs"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 250 && n <= 10000 {
			timeout = time.Duration(n) * time.Millisecond
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	devices, err := discoverPhysicalKVM(ctx)
	if err != nil {
		jsonError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "devices": devices})
}

func (s *HTTPServer) handlePhysicalKVMPair(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			URL   string `json:"url"`
			Token string `json:"token"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, physicalKVMMaxBody)).Decode(&req) != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		status, err := physicalKVM.pair(r.Context(), req.URL, req.Token)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "deviceId": status.DeviceID, "usbReady": status.USBReady, "armed": status.Armed})
	case http.MethodDelete:
		if err := physicalKVM.unpair(r.Context()); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use POST or DELETE")
	}
}

func (s *HTTPServer) handlePhysicalKVMSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/kvm/session/")
	var req struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, physicalKVMMaxBody)).Decode(&req)
	switch action {
	case "open":
		id, err := physicalKVM.open(r.Context())
		if err != nil {
			jsonReply(w, http.StatusConflict, map[string]any{"ok": false, "code": "KVM_SESSION_REFUSED", "error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "sessionId": id, "expiresInMs": 10000})
	case "heartbeat":
		if err := physicalKVM.heartbeat(r.Context(), req.SessionID); err != nil {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true})
	case "close":
		if err := physicalKVM.close(r.Context()); err != nil {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true})
	default:
		jsonError(w, http.StatusNotFound, "unknown KVM session action")
	}
}

func (s *HTTPServer) handlePhysicalKVMAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var action map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, physicalKVMMaxBody)).Decode(&action); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	sessionID, _ := action["sessionId"].(string)
	delete(action, "sessionId")
	seq, err := physicalKVM.action(r.Context(), sessionID, action)
	if err != nil {
		jsonReply(w, http.StatusConflict, map[string]any{"ok": false, "code": "KVM_ACTION_REFUSED", "error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "sequence": seq})
}

func (s *HTTPServer) handlePhysicalKVMCapture(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		selected, err := physicalKVM.selectedCaptureDevice()
		jsonReply(w, http.StatusOK, map[string]any{"ok": err == nil, "selectedDevice": selected, "devices": captureDevices(), "error": errString(err)})
	case http.MethodPost:
		var req struct {
			Device string `json:"device"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, physicalKVMMaxBody)).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := physicalKVM.selectCaptureDevice(req.Device); err != nil {
			jsonReply(w, http.StatusConflict, map[string]any{"ok": false, "code": "KVM_CAPTURE_SELECT_FAILED", "error": err.Error(), "devices": captureDevices()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "selectedDevice": filepath.Clean(req.Device)})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handlePhysicalKVMReleaseAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if err := physicalKVM.releaseAll(r.Context()); err != nil {
		jsonError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *HTTPServer) handlePhysicalKVMFirmware(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	image, err := io.ReadAll(io.LimitReader(r.Body, (4<<20)+1))
	if err != nil || len(image) > 4<<20 {
		jsonError(w, http.StatusBadRequest, "firmware image exceeds 4 MiB")
		return
	}
	checksum, err := physicalKVM.installFirmware(r.Context(), image)
	if err != nil {
		jsonReply(w, http.StatusConflict, map[string]any{"ok": false, "code": "KVM_FIRMWARE_UPDATE_FAILED", "error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "sha256": checksum, "restarting": true})
}
