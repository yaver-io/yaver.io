package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type WiFiMeshConfig struct {
	MeshID       string `json:"meshId"`
	Passphrase   string `json:"passphrase,omitempty"`
	Interface    string `json:"interface"`
	MeshIface    string `json:"meshInterface,omitempty"`
	Backend      string `json:"backend"` // "80211s" or "batman"
	IPAddress    string `json:"ipAddress,omitempty"`
	Channel      int    `json:"channel,omitempty"`
	FrequencyMHz int    `json:"frequencyMhz,omitempty"`
	CountryCode  string `json:"countryCode,omitempty"`
	MTU          int    `json:"mtu,omitempty"`
}

type WiFiMeshStatus struct {
	Running       bool     `json:"running"`
	Backend       string   `json:"backend,omitempty"`
	MeshID        string   `json:"meshId,omitempty"`
	Interface     string   `json:"interface,omitempty"`
	MeshInterface string   `json:"meshInterface,omitempty"`
	BatmanIface   string   `json:"batmanInterface,omitempty"`
	IPAddress     string   `json:"ipAddress,omitempty"`
	Peers         []string `json:"peers,omitempty"`
	LastError     string   `json:"lastError,omitempty"`
	Uptime        string   `json:"uptime,omitempty"`
}

type WiFiMeshCapabilities struct {
	Interface          string   `json:"interface,omitempty"`
	SupportsMeshPoint  bool     `json:"supportsMeshPoint"`
	SupportsBATMANAdv  bool     `json:"supportsBatmanAdv"`
	HasWpaSupplicant   bool     `json:"hasWpaSupplicant"`
	HasIW              bool     `json:"hasIw"`
	HasBatctl          bool     `json:"hasBatctl"`
	RecommendedBackend string   `json:"recommendedBackend,omitempty"`
	SupportedBackends  []string `json:"supportedBackends,omitempty"`
}

type WiFiMeshManager struct {
	mu                 sync.Mutex
	workDir            string
	stateDir           string
	config             *WiFiMeshConfig
	status             *WiFiMeshStatus
	startedAt          time.Time
	wpaConfigPath      string
	wpaPIDPath         string
	wpaCtrlPath        string
	logPath            string
	meshInterfaceOwned bool
}

func NewWiFiMeshManager(workDir string) *WiFiMeshManager {
	stateDir := filepath.Join(workDir, ".yaver", "wifi-mesh")
	return &WiFiMeshManager{
		workDir:       workDir,
		stateDir:      stateDir,
		wpaConfigPath: filepath.Join(stateDir, "wpa_supplicant.conf"),
		wpaPIDPath:    filepath.Join(stateDir, "wpa_supplicant.pid"),
		wpaCtrlPath:   filepath.Join(stateDir, "wpa-ctrl"),
		logPath:       filepath.Join(stateDir, "wifi-mesh.log"),
		status:        &WiFiMeshStatus{},
	}
}

func (wm *WiFiMeshManager) DetectCapabilities() (*WiFiMeshCapabilities, error) {
	caps := &WiFiMeshCapabilities{}
	caps.HasIW = toolExists("iw")
	caps.HasWpaSupplicant = toolExists("wpa_supplicant")
	caps.HasBatctl = toolExists("batctl")
	caps.SupportsBATMANAdv = caps.HasBatctl && kernelModuleAvailable("batman_adv")
	if runtime.GOOS != "linux" {
		return caps, nil
	}
	ifaces, err := NewWiFiHotspotManager(wm.workDir).findWiFiInterfaces()
	if err == nil && len(ifaces) > 0 {
		caps.Interface = ifaces[0]
	}
	if caps.Interface != "" && caps.HasIW {
		if phy, err := NewWiFiHotspotManager(wm.workDir).getPhyName(caps.Interface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phy, "info"); err == nil {
				caps.SupportsMeshPoint = iwSupportsInterfaceMode(out, "mesh point")
			}
		}
	}
	if caps.SupportsMeshPoint && caps.HasWpaSupplicant {
		caps.SupportedBackends = append(caps.SupportedBackends, "80211s")
		caps.RecommendedBackend = "80211s"
	}
	if caps.SupportsMeshPoint && caps.SupportsBATMANAdv && caps.HasWpaSupplicant {
		caps.SupportedBackends = append(caps.SupportedBackends, "batman")
		caps.RecommendedBackend = "batman"
	}
	return caps, nil
}

func (wm *WiFiMeshManager) Start(cfg *WiFiMeshConfig) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if runtime.GOOS != "linux" {
		return fmt.Errorf("wifi mesh lifecycle is currently implemented on Linux only")
	}
	if err := wm.ValidateWpaEnvironment(); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("wifi mesh start requires root privileges for iw, wpa_supplicant, IP setup, and optional batman-adv")
	}
	cfg = normalizeWiFiMeshConfig(cfg)
	if err := validateWiFiMeshConfig(cfg); err != nil {
		return err
	}
	if wm.status != nil && wm.status.Running {
		return fmt.Errorf("wifi mesh already running on %s", wm.status.MeshInterface)
	}
	if err := os.MkdirAll(wm.stateDir, 0o700); err != nil {
		return fmt.Errorf("create wifi mesh state dir: %w", err)
	}
	if err := os.MkdirAll(wm.wpaCtrlPath, 0o700); err != nil {
		return fmt.Errorf("create wpa_supplicant control dir: %w", err)
	}
	if err := wm.ensureMeshInterface(cfg); err != nil {
		return err
	}
	if err := wm.GenerateWpaSupplicantConfig(cfg); err != nil {
		return err
	}
	if err := wm.startWpaSupplicant(cfg.MeshIface); err != nil {
		return err
	}
	batIface := ""
	ipIface := cfg.MeshIface
	if cfg.Backend == "batman" {
		batIface = "bat0"
		if err := wm.startBatman(cfg.MeshIface, batIface, cfg.MTU); err != nil {
			_ = wm.stopWpaSupplicant()
			return err
		}
		ipIface = batIface
	}
	if cfg.IPAddress != "" {
		if err := configureInterfaceCIDR(ipIface, cfg.IPAddress); err != nil {
			_ = wm.stopBatman(cfg.MeshIface, batIface)
			_ = wm.stopWpaSupplicant()
			return err
		}
	}
	wm.config = cfg
	wm.startedAt = time.Now()
	wm.status = &WiFiMeshStatus{
		Running:       true,
		Backend:       cfg.Backend,
		MeshID:        cfg.MeshID,
		Interface:     cfg.Interface,
		MeshInterface: cfg.MeshIface,
		BatmanIface:   batIface,
		IPAddress:     cfg.IPAddress,
	}
	return nil
}

func (wm *WiFiMeshManager) Stop() error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	cfg := wm.config
	if cfg != nil && cfg.Backend == "batman" {
		_ = wm.stopBatman(cfg.MeshIface, "bat0")
	}
	_ = wm.stopWpaSupplicant()
	if cfg != nil && wm.meshInterfaceOwned && cfg.MeshIface != "" && cfg.MeshIface != cfg.Interface && NewWiFiHotspotManager(wm.workDir).checkInterfaceExists(cfg.MeshIface) {
		_, _ = runWifiCmd("ip", "link", "delete", cfg.MeshIface)
	}
	_ = os.Remove(wm.wpaPIDPath)
	wm.config = nil
	wm.startedAt = time.Time{}
	wm.meshInterfaceOwned = false
	wm.status = &WiFiMeshStatus{}
	return nil
}

func (wm *WiFiMeshManager) Status() (*WiFiMeshStatus, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	st := *wm.status
	st.Running = processRunningFromPIDFile(wm.wpaPIDPath)
	if st.Running && !wm.startedAt.IsZero() {
		st.Uptime = time.Since(wm.startedAt).Round(time.Second).String()
	}
	if st.MeshInterface != "" {
		st.Peers = wifiMeshPeers(st.MeshInterface, st.Backend)
	}
	return &st, nil
}

func (wm *WiFiMeshManager) GenerateWpaSupplicantConfig(cfg *WiFiMeshConfig) error {
	cfg = normalizeWiFiMeshConfig(cfg)
	if err := os.MkdirAll(wm.stateDir, 0o700); err != nil {
		return err
	}
	lines := []string{
		"ctrl_interface=" + wm.wpaCtrlPath,
		"ap_scan=2",
	}
	if cfg.CountryCode != "" {
		lines = append(lines, "country="+strings.ToUpper(cfg.CountryCode))
	}
	network := []string{
		"network={",
		"\tssid=\"" + escapeWPAString(cfg.MeshID) + "\"",
		"\tmode=5",
		"\tfrequency=" + strconv.Itoa(cfg.FrequencyMHz),
	}
	if cfg.Passphrase != "" {
		network = append(network,
			"\tkey_mgmt=SAE",
			"\tpsk=\""+escapeWPAString(cfg.Passphrase)+"\"",
		)
	} else {
		network = append(network, "\tkey_mgmt=NONE")
	}
	network = append(network, "}")
	lines = append(lines, network...)
	return os.WriteFile(wm.wpaConfigPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func (wm *WiFiMeshManager) ensureMeshInterface(cfg *WiFiMeshConfig) error {
	if cfg.MeshIface == cfg.Interface {
		return nil
	}
	if NewWiFiHotspotManager(wm.workDir).checkInterfaceExists(cfg.MeshIface) {
		return nil
	}
	if _, err := runWifiCmd("iw", "dev", cfg.Interface, "interface", "add", cfg.MeshIface, "type", "mp"); err != nil {
		return fmt.Errorf("create mesh interface %s from %s: %w", cfg.MeshIface, cfg.Interface, err)
	}
	wm.meshInterfaceOwned = true
	return nil
}

func (wm *WiFiMeshManager) startWpaSupplicant(iface string) error {
	cmd := osexec.Command("wpa_supplicant", "-B", "-i", iface, "-c", wm.wpaConfigPath, "-P", wm.wpaPIDPath, "-f", wm.logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start wpa_supplicant: %w: %s", err, clipString(strings.TrimSpace(string(out)), 500))
	}
	return nil
}

func (wm *WiFiMeshManager) stopWpaSupplicant() error {
	return stopPIDFile(wm.wpaPIDPath)
}

func (wm *WiFiMeshManager) startBatman(meshIface, batIface string, mtu int) error {
	_, _ = runWifiCmd("modprobe", "batman-adv")
	if _, err := runWifiCmd("ip", "link", "set", meshIface, "up"); err != nil {
		return fmt.Errorf("bring mesh interface up: %w", err)
	}
	if _, err := runWifiCmd("batctl", "if", "add", meshIface); err != nil {
		return fmt.Errorf("add %s to batman-adv: %w", meshIface, err)
	}
	if mtu > 0 {
		_, _ = runWifiCmd("ip", "link", "set", "dev", batIface, "mtu", strconv.Itoa(mtu))
	}
	if _, err := runWifiCmd("ip", "link", "set", batIface, "up"); err != nil {
		return fmt.Errorf("bring %s up: %w", batIface, err)
	}
	return nil
}

func (wm *WiFiMeshManager) ValidateWpaEnvironment() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("wpa_supplicant mesh support is implemented on Linux only")
	}
	if !toolExists("iw") {
		return fmt.Errorf("iw not found in PATH; install iw before starting Wi-Fi mesh")
	}
	if !toolExists("wpa_supplicant") {
		return fmt.Errorf("wpa_supplicant not found in PATH; install wpa_supplicant before starting Wi-Fi mesh")
	}
	caps, err := wm.DetectCapabilities()
	if err != nil {
		return fmt.Errorf("detect Wi-Fi mesh capabilities: %w", err)
	}
	if caps.Interface == "" {
		return fmt.Errorf("no Wi-Fi interface found for wpa_supplicant mesh")
	}
	if !caps.SupportsMeshPoint {
		return fmt.Errorf("interface %s does not advertise 802.11s mesh point support", caps.Interface)
	}
	return nil
}

func (wm *WiFiMeshManager) stopBatman(meshIface, batIface string) error {
	if meshIface != "" {
		_, _ = runWifiCmd("batctl", "if", "del", meshIface)
	}
	if batIface != "" {
		_, _ = runWifiCmd("ip", "link", "set", batIface, "down")
	}
	return nil
}

func normalizeWiFiMeshConfig(cfg *WiFiMeshConfig) *WiFiMeshConfig {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	if cp.Backend == "" {
		cp.Backend = "80211s"
	}
	cp.Backend = strings.ToLower(cp.Backend)
	if cp.MeshIface == "" && cp.Interface != "" {
		cp.MeshIface = cp.Interface + "mesh"
	}
	if cp.Channel == 0 {
		cp.Channel = 6
	}
	if cp.FrequencyMHz == 0 {
		cp.FrequencyMHz = channelToFrequencyMHz(cp.Channel)
	}
	if cp.MTU == 0 {
		cp.MTU = 1532
	}
	return &cp
}

func validateWiFiMeshConfig(cfg *WiFiMeshConfig) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if strings.TrimSpace(cfg.MeshID) == "" {
		return fmt.Errorf("meshId is required")
	}
	if len(cfg.MeshID) > 32 {
		return fmt.Errorf("meshId too long (max 32 bytes)")
	}
	if cfg.Passphrase != "" && (len(cfg.Passphrase) < 8 || len(cfg.Passphrase) > 63) {
		return fmt.Errorf("passphrase must be 8-63 characters when set")
	}
	if strings.TrimSpace(cfg.Interface) == "" {
		return fmt.Errorf("interface is required")
	}
	if strings.TrimSpace(cfg.MeshIface) == "" {
		return fmt.Errorf("meshInterface is required")
	}
	if cfg.Backend != "80211s" && cfg.Backend != "batman" {
		return fmt.Errorf("backend must be 80211s or batman")
	}
	if cfg.FrequencyMHz <= 0 {
		return fmt.Errorf("frequencyMhz is required")
	}
	return nil
}

func channelToFrequencyMHz(ch int) int {
	if ch >= 1 && ch <= 13 {
		return 2407 + ch*5
	}
	if ch == 14 {
		return 2484
	}
	if ch >= 32 && ch <= 177 {
		return 5000 + ch*5
	}
	return 2437
}

func configureInterfaceCIDR(iface, cidr string) error {
	if _, err := runWifiCmd("ip", "link", "set", iface, "up"); err != nil {
		return fmt.Errorf("bring %s up: %w", iface, err)
	}
	if _, err := runWifiCmd("ip", "addr", "flush", "dev", iface); err != nil {
		return fmt.Errorf("flush %s addresses: %w", iface, err)
	}
	if _, err := runWifiCmd("ip", "addr", "add", cidr, "dev", iface); err != nil {
		return fmt.Errorf("assign %s to %s: %w", cidr, iface, err)
	}
	return nil
}

func wifiMeshPeers(iface, backend string) []string {
	if backend == "batman" && toolExists("batctl") {
		if out, err := runWifiCmd("batctl", "n"); err == nil {
			return wifiMeshNonEmptyLines(out)
		}
	}
	if out, err := runWifiCmd("iw", "dev", iface, "station", "dump"); err == nil {
		var peers []string
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Station ") {
				peers = append(peers, strings.TrimPrefix(line, "Station "))
			}
		}
		return peers
	}
	return nil
}

func iwSupportsInterfaceMode(out, mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	inModes := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "supported interface modes:") {
			inModes = true
			continue
		}
		if inModes {
			if !strings.HasPrefix(line, "*") {
				inModes = false
				continue
			}
			if strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "*"))) == mode {
				return true
			}
		}
	}
	return false
}

func toolExists(name string) bool {
	_, err := osexec.LookPath(name)
	return err == nil
}

func kernelModuleAvailable(name string) bool {
	if _, err := os.Stat(filepath.Join("/sys/module", name)); err == nil {
		return true
	}
	if out, err := runWifiCmd("modinfo", name); err == nil && strings.TrimSpace(out) != "" {
		return true
	}
	return false
}

func escapeWPAString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func wifiMeshNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
