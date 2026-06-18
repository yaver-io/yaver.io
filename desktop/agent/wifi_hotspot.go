package main

import (
	"encoding/json"
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

// runWifiCmd executes a command and returns its output
func runWifiCmd(name string, args ...string) (string, error) {
	cmd := osexec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// WiFiHotspotConfig represents the configuration for a WiFi hotspot
type WiFiHotspotConfig struct {
	SSID         string `json:"ssid"`                   // Network name
	Password     string `json:"password"`               // WPA2 password (8-63 chars)
	Channel      int    `json:"channel"`                // WiFi channel (1-14)
	Mode         string `json:"mode"`                   // "ap" or "apsta" (AP+STA repeater)
	Interface    string `json:"interface"`              // wlan0, wlp2s0, etc.
	APInterface  string `json:"apInterface,omitempty"`  // optional AP virtual interface in AP+STA mode
	UpstreamIF   string `json:"upstreamIf,omitempty"`   // optional uplink interface for NAT
	UpstreamSSID string `json:"upstreamSsid,omitempty"` // For AP+STA mode
	UpstreamPass string `json:"upstreamPass,omitempty"` // For AP+STA mode
	Frequency    string `json:"frequency"`              // "2.4GHz" or "5GHz"
	BridgeName   string `json:"bridgeName"`             // Bridge interface name
	IPAddress    string `json:"ipAddress,omitempty"`    // Static IP (optional)
	EnableDHCP   bool   `json:"enableDhcp"`             // Run dnsmasq DHCP
	DHCPRange    string `json:"dhcpRange,omitempty"`    // DHCP range (e.g., "192.168.4.100,192.168.4.200")
	MACAddress   string `json:"macAddress,omitempty"`   // Custom MAC (optional)
	EnableNAT    bool   `json:"enableNat"`              // Enable NAT forwarding
	CountryCode  string `json:"countryCode,omitempty"`  // ISO country code
	EnableWMM    bool   `json:"enableWmm"`              // WMM for QoS
	EnableHT40   bool   `json:"enableHt40"`             // HT40 support
	EnableVHT    bool   `json:"enableVht"`              // VHT support
	EnableHE80   bool   `json:"enableHe80"`             // HE80 support
}

// WiFiHotspotStatus represents the current status of a WiFi hotspot
type WiFiHotspotStatus struct {
	Running          bool     `json:"running"`
	Mode             string   `json:"mode"` // "ap" or "apsta"
	SSID             string   `json:"ssid"`
	Interface        string   `json:"interface"`
	BridgeName       string   `json:"bridgeName,omitempty"`
	IPAddress        string   `json:"ipAddress,omitempty"`
	ConnectedClients int      `json:"connectedClients"`
	UpstreamStatus   string   `json:"upstreamStatus,omitempty"` // "connected", "disconnected", "connecting"
	Uptime           string   `json:"uptime,omitempty"`
	LastError        string   `json:"lastError,omitempty"`
	SupportedModes   []string `json:"supportedModes"`  // ["ap"], ["ap", "apsta"], or []
	HardwareSupport  string   `json:"hardwareSupport"` // "full", "ap-only", "apsta-only", or "none"
}

// WiFiHardwareCapabilities represents the detected hardware capabilities
type WiFiHardwareCapabilities struct {
	Interface       string   `json:"interface"`
	Driver          string   `json:"driver"`
	ChannelCount    int      `json:"channelCount"`
	SupportedBands  []string `json:"supportedBands"` // ["2.4GHz", "5GHz"]
	SupportedModes  []string `json:"supportedModes"` // ["ap"], ["ap", "apsta"]
	MACAddress      string   `json:"macAddress"`
	SupportsAP      bool     `json:"supportsAp"`
	SupportsAPSTA   bool     `json:"supportsApsta"`
	Supports5GHz    bool     `json:"supports5GHz"`
	SupportsHT40    bool     `json:"supportsHt40"`
	SupportsVHT     bool     `json:"supportsVht"`
	SupportsHE80    bool     `json:"supportsHe80"`
	SupportsWMM     bool     `json:"supportsWmm"`
	IsUSBDevice     bool     `json:"isUsbDevice"`
	HardwareSupport string   `json:"hardwareSupport"` // "full", "ap-only", "apsta-only", or "none"
}

// WiFiHotspotManager manages the WiFi hotspot lifecycle
type WiFiHotspotManager struct {
	mu                sync.Mutex
	config            *WiFiHotspotConfig
	status            *WiFiHotspotStatus
	pid               int
	dnsmasqPID        int
	startedAt         time.Time
	configPath        string
	pidPath           string
	dnsmasqPIDPath    string
	logPath           string
	hostapdConfigPath string
	dnsmasqConfigPath string
	wpaConfigPath     string
	wpaPIDPath        string
	wpaCtrlPath       string
	wifiInterface     string
	workDir           string
	stopChan          chan struct{}
	bannedClients     map[string]time.Time
}

// NewWiFiHotspotManager creates a new WiFi hotspot manager
func NewWiFiHotspotManager(workDir string) *WiFiHotspotManager {
	yaverDir := filepath.Join(workDir, ".yaver")
	return &WiFiHotspotManager{
		configPath:        filepath.Join(yaverDir, "wifi-hotspot.yaml"),
		pidPath:           filepath.Join(yaverDir, "wifi-hotspot.pid"),
		dnsmasqPIDPath:    filepath.Join(yaverDir, "wifi-dnsmasq.pid"),
		logPath:           filepath.Join(yaverDir, "wifi-hotspot.log"),
		hostapdConfigPath: filepath.Join(yaverDir, "hostapd.conf"),
		dnsmasqConfigPath: filepath.Join(yaverDir, "dnsmasq.conf"),
		wpaConfigPath:     filepath.Join(yaverDir, "wpa_supplicant-apsta.conf"),
		wpaPIDPath:        filepath.Join(yaverDir, "wpa_supplicant-apsta.pid"),
		wpaCtrlPath:       filepath.Join(yaverDir, "wpa-apsta-ctrl"),
		workDir:           workDir,
		stopChan:          make(chan struct{}),
		bannedClients:     make(map[string]time.Time),
		status: &WiFiHotspotStatus{
			SupportedModes:  []string{"ap"},
			HardwareSupport: "unknown",
		},
	}
}

// DetectHardwareCapabilities probes the WiFi hardware to determine capabilities
func (wm *WiFiHotspotManager) DetectHardwareCapabilities() (*WiFiHardwareCapabilities, error) {
	caps := &WiFiHardwareCapabilities{
		SupportedModes: []string{"ap"},
	}

	// Find WiFi interfaces
	interfaces, err := wm.findWiFiInterfaces()
	if err != nil {
		return nil, fmt.Errorf("find wifi interfaces: %w", err)
	}

	if len(interfaces) == 0 {
		return nil, fmt.Errorf("no wifi interfaces found")
	}

	// Use first available interface for detailed probing
	caps.Interface = interfaces[0]
	caps.MACAddress = wm.getMACAddress(caps.Interface)

	// Detect driver
	caps.Driver = wm.detectWiFiDriver(caps.Interface)

	// Probe capabilities
	caps.SupportedBands = wm.detectSupportedBands(caps.Interface)
	caps.SupportedModes = wm.detectSupportedModes(caps.Interface)
	caps.ChannelCount = wm.detectChannelCount(caps.Interface)

	// Parse capabilities into high-level support flags
	caps.SupportsAP = stringSliceContains(caps.SupportedModes, "ap")
	caps.SupportsAPSTA = stringSliceContains(caps.SupportedModes, "apsta")
	caps.Supports5GHz = stringSliceContains(caps.SupportedBands, "5GHz")
	caps.SupportsHT40 = wm.checkHT40Support(caps.Interface)
	caps.SupportsVHT = wm.checkVHTSupport(caps.Interface)
	caps.SupportsHE80 = wm.checkHE80Support(caps.Interface)
	caps.SupportsWMM = wm.checkWMMSupport(caps.Interface)
	caps.IsUSBDevice = wm.checkUSBDevice(caps.Interface)

	// Determine overall hardware support classification
	caps.HardwareSupport = wm.classifyHardwareSupport(caps)

	// Update status
	wm.mu.Lock()
	wm.status.HardwareSupport = caps.HardwareSupport
	wm.status.SupportedModes = caps.SupportedModes
	wm.status.Interface = caps.Interface
	wm.mu.Unlock()

	return caps, nil
}

// findWiFiInterfaces finds all WiFi interfaces on the system
func (wm *WiFiHotspotManager) findWiFiInterfaces() ([]string, error) {
	var interfaces []string

	switch runtime.GOOS {
	case "linux":
		// Use iw and nmcli to find wireless interfaces
		if out, err := runWifiCmd("iw", "dev"); err == nil {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(line, "Interface") {
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						if iface := strings.TrimSuffix(parts[1], ":"); iface != "" {
							interfaces = append(interfaces, iface)
						}
					}
				}
			}
		}
		if len(interfaces) == 0 {
			// Fallback: check /sys/class/net for wireless interfaces
			if dirs, err := filepath.Glob("/sys/class/net/*/wireless"); err == nil && len(dirs) > 0 {
				for _, dir := range dirs {
					iface := filepath.Base(filepath.Dir(dir))
					if iface != "" {
						interfaces = append(interfaces, iface)
					}
				}
			}
		}

	case "darwin":
		// macOS: use networksetup to find WiFi interfaces
		if out, err := runWifiCmd("networksetup", "-listallhardwareports"); err == nil {
			lines := strings.Split(out, "\n")
			inWiFiPort := false
			for _, raw := range lines {
				line := strings.TrimSpace(raw)
				lower := strings.ToLower(line)
				if strings.HasPrefix(lower, "hardware port:") {
					inWiFiPort = strings.Contains(lower, "wi-fi") || strings.Contains(lower, "airport")
					continue
				}
				if inWiFiPort && strings.HasPrefix(lower, "device:") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						if iface := strings.TrimSpace(parts[1]); iface != "" {
							interfaces = append(interfaces, iface)
						}
					}
					inWiFiPort = false
				}
			}
		}

	case "windows":
		// Windows: use netsh to find wireless interfaces
		if out, err := runWifiCmd("netsh", "wlan", "show", "interfaces"); err == nil {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(strings.ToLower(line), "wi-fi") {
					parts := strings.Fields(line)
					for _, part := range parts {
						if strings.Contains(strings.ToLower(part), "wi-fi") && len(part) > 6 {
							interfaces = append(interfaces, part)
							break
						}
					}
				}
			}
		}
	}

	if len(interfaces) == 0 {
		return nil, fmt.Errorf("no wifi interfaces detected")
	}

	return interfaces, nil
}

// detectWiFiDriver detects the WiFi driver being used
func (wm *WiFiHotspotManager) detectWiFiDriver(iface string) string {
	switch runtime.GOOS {
	case "linux":
		// Check for driver via dmesg
		if out, err := runWifiCmd("sh", "-c", "dmesg | grep -i '"+iface+"' | grep -i 'firmware\\|driver' | head -5"); err == nil {
			if strings.Contains(strings.ToLower(out), "iwlwifi") {
				return "intel-iwlwifi"
			}
			if strings.Contains(strings.ToLower(out), "ath10k") {
				return "qualcomm-ath10k"
			}
			if strings.Contains(strings.ToLower(out), "realtek") {
				return "realtek-rtl88xx"
			}
			if strings.Contains(strings.ToLower(out), "broadcom") {
				return "broadcom-b43"
			}
		}

	case "darwin":
		// macOS drivers are typically internal
		return "airport"

	case "windows":
		// Windows drivers are loaded via NDIS
		if out, err := runWifiCmd("sh", "-c", "driverquery | findstr /i 'wifi\\|wireless'"); err == nil {
			if strings.Contains(strings.ToLower(out), "intel") {
				return "intel-proset"
			}
			if strings.Contains(strings.ToLower(out), "realtek") {
				return "realtek"
			}
		}
	}

	return "unknown"
}

// detectSupportedBands detects which frequency bands are supported
func (wm *WiFiHotspotManager) detectSupportedBands(iface string) []string {
	var bands []string

	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				parsed := parseIWPhyCapabilities(out)
				if parsed.Supports5GHz {
					bands = append(bands, "5GHz")
				}
				if parsed.Supports24GHz {
					bands = append(bands, "2.4GHz")
				}
			}
		}

	case "darwin":
		// macOS typically supports both bands on modern hardware
		if out, err := runWifiCmd("/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport", "-I"); err == nil {
			if strings.Contains(out, "5 GHz") || strings.Contains(out, "5Ghz") {
				bands = append(bands, "5GHz")
			}
			if strings.Contains(out, "2.4 GHz") || strings.Contains(out, "2.4Ghz") {
				bands = append(bands, "2.4GHz")
			}
		}

	case "windows":
		// Check via netsh
		if out, err := runWifiCmd("netsh", "wlan", "show", "drivers"); err == nil {
			if strings.Contains(out, "5 GHz") || strings.Contains(out, "5Ghz") {
				bands = append(bands, "5GHz")
			}
			if strings.Contains(out, "2.4 GHz") || strings.Contains(out, "2.4Ghz") {
				bands = append(bands, "2.4GHz")
			}
		}
	}

	if len(bands) == 0 {
		bands = append(bands, "2.4GHz") // Default fallback
	}

	return bands
}

// detectSupportedModes detects whether AP and AP+STA modes are supported.
func (wm *WiFiHotspotManager) detectSupportedModes(iface string) []string {
	var modes []string

	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				parsed := parseIWPhyCapabilities(out)
				if parsed.SupportsAP {
					modes = append(modes, "ap")
				}
				if parsed.SupportsAPSTA {
					modes = append(modes, "apsta")
				}
			}
		}

	case "darwin":
		// macOS supports AP mode via Internet Sharing.
		modes = append(modes, "ap")

	case "windows":
		// Windows supports mobile hotspot (AP mode).
		modes = append(modes, "ap")
		if wm.checkWindowsAPSTA(iface) {
			modes = append(modes, "apsta")
		}
	}

	return modes
}

type iwPhyCapabilities struct {
	Supports24GHz bool
	Supports5GHz  bool
	SupportsAP    bool
	SupportsSTA   bool
	SupportsAPSTA bool
	ChannelCount  int
	SupportsHT40  bool
	SupportsVHT   bool
	SupportsHE80  bool
	SupportsWMM   bool
}

func parseIWPhyCapabilities(out string) iwPhyCapabilities {
	var caps iwPhyCapabilities
	lines := strings.Split(out, "\n")
	inModes := false
	inCombinations := false
	var combo strings.Builder

	flushCombo := func() {
		line := strings.ToLower(combo.String())
		combo.Reset()
		if line == "" {
			return
		}
		hasAP := strings.Contains(line, "{ ap") || strings.Contains(line, ", ap") || strings.Contains(line, " ap,") || strings.Contains(line, " ap }")
		hasSTA := strings.Contains(line, "managed") || strings.Contains(line, "station")
		if hasAP && hasSTA && comboTotalAtLeastTwo(line) {
			caps.SupportsAPSTA = true
		}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if line == "" {
			inModes = false
			continue
		}
		if strings.Contains(lower, "supported interface modes:") {
			inModes = true
			inCombinations = false
			continue
		}
		if strings.Contains(lower, "valid interface combinations:") {
			inCombinations = true
			inModes = false
			continue
		}
		if inModes {
			if !strings.HasPrefix(line, "*") {
				inModes = false
			} else {
				mode := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "*")))
				if mode == "ap" {
					caps.SupportsAP = true
				}
				if mode == "managed" || mode == "station" {
					caps.SupportsSTA = true
				}
			}
		}
		if inCombinations {
			if strings.HasPrefix(line, "*") {
				flushCombo()
			}
			if strings.HasPrefix(line, "*") || strings.HasPrefix(line, "total") || strings.HasPrefix(line, "#channels") || strings.Contains(line, "#{") {
				if combo.Len() > 0 {
					combo.WriteByte(' ')
				}
				combo.WriteString(line)
			} else if !strings.HasPrefix(line, "[") {
				flushCombo()
				inCombinations = false
			}
		}
		if strings.Contains(lower, "mhz") {
			if strings.Contains(lower, "2412") || strings.Contains(lower, "2437") || strings.Contains(lower, "2462") || strings.Contains(lower, "2.4") {
				caps.Supports24GHz = true
			}
			if strings.Contains(lower, "5180") || strings.Contains(lower, "5200") || strings.Contains(lower, "5805") || strings.Contains(lower, "5 ghz") {
				caps.Supports5GHz = true
			}
			caps.ChannelCount++
		}
		if strings.Contains(line, "HT40") {
			caps.SupportsHT40 = true
		}
		if strings.Contains(line, "VHT") {
			caps.SupportsVHT = true
		}
		if strings.Contains(line, "HE80") {
			caps.SupportsHE80 = true
		}
		if strings.Contains(lower, "wmm") {
			caps.SupportsWMM = true
		}
	}
	flushCombo()
	if caps.SupportsAPSTA {
		caps.SupportsAP = true
		caps.SupportsSTA = true
	}
	return caps
}

func comboTotalAtLeastTwo(line string) bool {
	idx := strings.Index(line, "total <=")
	if idx < 0 {
		return true
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line[idx:], "total <="))
	fields := strings.FieldsFunc(rest, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if len(fields) == 0 {
		return true
	}
	n, err := strconv.Atoi(fields[0])
	return err != nil || n >= 2
}

// checkAPSTASupport checks if the hardware supports simultaneous AP+STA mode
func (wm *WiFiHotspotManager) checkAPSTASupport(iface, driver string) bool {
	if phyName, err := wm.getPhyName(iface); err == nil {
		if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
			return parseIWPhyCapabilities(out).SupportsAPSTA
		}
	}

	// Intel WiFi cards typically support AP+STA mode
	if driver == "intel-iwlwifi" {
		return wm.checkIntelAPSTA(iface)
	}

	// Qualcomm Atheros cards often support AP+STA
	if strings.Contains(driver, "ath") {
		return wm.checkAtherosAPSTA(iface)
	}

	// Realtek cards vary by model
	if strings.Contains(driver, "realtek") {
		return wm.checkRealtekAPSTA(iface)
	}

	return false
}

// checkIntelAPSTA checks Intel WiFi card AP+STA support
func (wm *WiFiHotspotManager) checkIntelAPSTA(iface string) bool {
	if phyName, err := wm.getPhyName(iface); err == nil {
		if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
			return parseIWPhyCapabilities(out).SupportsAPSTA
		}
	}
	return false
}

// checkAtherosAPSTA checks Atheros WiFi card AP+STA support
func (wm *WiFiHotspotManager) checkAtherosAPSTA(iface string) bool {
	// Atheros cards often need specific firmware for AP+STA
	if out, err := runWifiCmd("sh", "-c", "dmesg | grep -i ath10k | grep -i 'ap\\|sta\\|combined' | head -10"); err == nil {
		return strings.Contains(strings.ToLower(out), "ap") && strings.Contains(strings.ToLower(out), "sta")
	}
	return false
}

// checkRealtekAPSTA checks Realtek WiFi card AP+STA support
func (wm *WiFiHotspotManager) checkRealtekAPSTA(iface string) bool {
	if out, err := runWifiCmd("sh", "-c", "dmesg | grep -i rtl | grep -i 'ap\\|sta\\|combined' | head -10"); err == nil {
		return strings.Contains(strings.ToLower(out), "ap") && strings.Contains(strings.ToLower(out), "sta")
	}
	return false
}

// checkWindowsAPSTA checks Windows AP+STA support
func (wm *WiFiHotspotManager) checkWindowsAPSTA(iface string) bool {
	if out, err := runWifiCmd("netsh", "wlan", "show", "profiles"); err == nil {
		// Check if there are multiple profiles which suggests AP+STA support
		profiles := strings.Count(strings.ToLower(out), "profile")
		return profiles > 1
	}
	return false
}

// detectChannelCount detects the number of supported channels
func (wm *WiFiHotspotManager) detectChannelCount(iface string) int {
	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyNameName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				if n := parseIWPhyCapabilities(out).ChannelCount; n > 0 {
					return n
				}
			}
		}

	case "darwin":
		// macOS typically supports 2.4GHz (channels 1-13) and 5GHz
		return 13

	case "windows":
		// Windows typically supports standard channels
		return 13
	}

	return 13 // Default
}

// checkHT40Support checks HT40 support
func (wm *WiFiHotspotManager) checkHT40Support(iface string) bool {
	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyNameName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				return parseIWPhyCapabilities(out).SupportsHT40
			}
		}

	case "darwin":
		// macOS modern hardware supports HT40
		return true

	case "windows":
		if out, err := runWifiCmd("netsh", "wlan", "show", "profiles"); err == nil {
			return strings.Contains(strings.ToLower(out), "ht40")
		}
	}

	return false
}

// checkVHTSupport checks VHT support
func (wm *WiFiHotspotManager) checkVHTSupport(iface string) bool {
	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyNameName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				return parseIWPhyCapabilities(out).SupportsVHT
			}
		}

	case "darwin":
		// macOS modern hardware supports VHT
		return true

	case "windows":
		if out, err := runWifiCmd("netsh", "wlan", "show", "profiles"); err == nil {
			return strings.Contains(strings.ToLower(out), "vht") || strings.Contains(strings.ToLower(out), "802.11ac")
		}
	}

	return false
}

// checkHE80Support checks HE80 support
func (wm *WiFiHotspotManager) checkHE80Support(iface string) bool {
	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyNameName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				return parseIWPhyCapabilities(out).SupportsHE80
			}
		}

	case "darwin":
		// Check macOS version for HE80 support
		if out, err := runWifiCmd("sw_vers", "-productVersion"); err == nil {
			version, _ := strconv.ParseFloat(strings.TrimSpace(out), 64)
			if version >= 14.0 {
				return true
			}
		}

	case "windows":
		if out, err := runWifiCmd("netsh", "wlan", "show", "profiles"); err == nil {
			return strings.Contains(strings.ToLower(out), "he80") || strings.Contains(strings.ToLower(out), "wifi6e")
		}
	}

	return false
}

// checkWMMSupport checks WMM support
func (wm *WiFiHotspotManager) checkWMMSupport(iface string) bool {
	switch runtime.GOOS {
	case "linux":
		if phyName, err := wm.getPhyNameName(iface); err == nil {
			if out, err := runWifiCmd("iw", "phy", phyName, "info"); err == nil {
				return parseIWPhyCapabilities(out).SupportsWMM
			}
		}

	case "darwin":
		// macOS typically supports WMM
		return true

	case "windows":
		if out, err := runWifiCmd("netsh", "wlan", "show", "profiles"); err == nil {
			return strings.Contains(strings.ToLower(out), "wmm")
		}
	}

	return false
}

// checkUSBDevice checks if the WiFi interface is a USB device
func (wm *WiFiHotspotManager) checkUSBDevice(iface string) bool {
	switch runtime.GOOS {
	case "linux":
		// Check USB buses for WiFi devices
		if out, err := runWifiCmd("lsusb", "-t"); err == nil {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(strings.ToLower(line), "wireless") ||
					strings.Contains(strings.ToLower(line), "wifi") ||
					strings.Contains(strings.ToLower(line), "802.11") ||
					strings.Contains(strings.ToLower(line), "wlan") {
					return true
				}
			}
		}
		// Also check /sys/bus/usb
		if dirs, err := filepath.Glob("/sys/bus/usb/*/usb*/wireless"); err == nil && len(dirs) > 0 {
			return true
		}

	case "darwin":
		// macOS USB WiFi adapters show up in system info
		if out, err := runWifiCmd("system_profiler", "SPUSBDataType"); err == nil {
			return strings.Contains(strings.ToLower(out), "wireless") || strings.Contains(strings.ToLower(out), "802.11")
		}

	case "windows":
		// Check Device Manager for USB WiFi adapters
		if out, err := runWifiCmd("wmic", "path", "usb", "get", "name", ".*,description"); err == nil {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(strings.ToLower(line), "wireless") || strings.Contains(strings.ToLower(line), "802.11") {
					return true
				}
			}
		}
	}

	return false
}

// classifyHardwareSupport classifies the overall hardware support level
func (wm *WiFiHotspotManager) classifyHardwareSupport(caps *WiFiHardwareCapabilities) string {
	if caps.SupportsAP && caps.SupportsAPSTA && caps.Supports5GHz {
		return "full"
	}
	if caps.SupportsAP && caps.SupportsAPSTA {
		return "apsta-only"
	}
	if caps.SupportsAP {
		return "ap-only"
	}
	if caps.SupportsAPSTA {
		return "apsta-only"
	}
	return "none"
}

// getPhyName gets the phy name for an interface
func (wm *WiFiHotspotManager) getPhyNameName(iface string) (string, error) {
	if out, err := runWifiCmd("sh", "-c", "iw dev "+iface+" info | grep 'wiphy'"); err == nil {
		parts := strings.Fields(out)
		if len(parts) > 1 {
			return parts[len(parts)-1], nil
		}
	}
	return "", fmt.Errorf("could not get phy name for %s", iface)
}

// getPhyName is the original function name (keep for compatibility)
func (wm *WiFiHotspotManager) getPhyName(iface string) (string, error) {
	return wm.getPhyNameName(iface)
}

// getMACAddress gets the MAC address of an interface
func (wm *WiFiHotspotManager) getMACAddress(iface string) string {
	switch runtime.GOOS {
	case "linux":
		if out, err := runWifiCmd("ip", "link", "show", iface); err == nil {
			parts := strings.Split(out, "\n")
			for _, line := range parts {
				if strings.Contains(line, "link/ether") {
					parts := strings.Fields(line)
					if len(parts) >= 3 {
						return parts[2]
					}
				}
			}
		}

	case "darwin":
		if out, err := runWifiCmd("ifconfig", iface); err == nil {
			for _, line := range strings.Split(out, "\n") {
				parts := strings.Fields(strings.TrimSpace(line))
				if len(parts) >= 2 && parts[0] == "ether" {
					return parts[1]
				}
			}
		}

	case "windows":
		if out, err := runWifiCmd("getmac", iface, "/v", "/fo", "csv"); err == nil {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(line, iface) && !strings.Contains(line, "Media") {
					parts := strings.Split(line, ",")
					if len(parts) >= 1 {
						// Remove quotes and spaces
						mac := strings.Trim(strings.Trim(parts[0], "\""), " ")
						return mac
					}
				}
			}
		}
	}

	return "00:00:00:00:00:00"
}

// ValidateConfig validates a WiFi hotspot configuration
func (wm *WiFiHotspotManager) ValidateConfig(cfg *WiFiHotspotConfig) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Check SSID
	if cfg.SSID == "" {
		return fmt.Errorf("SSID cannot be empty")
	}
	if len(cfg.SSID) > 32 {
		return fmt.Errorf("SSID too long (max 32 characters)")
	}

	// Check password
	if cfg.Password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	if len(cfg.Password) < 8 {
		return fmt.Errorf("password too short (min 8 characters)")
	}
	if len(cfg.Password) > 63 {
		return fmt.Errorf("password too long (max 63 characters)")
	}

	// Check channel
	if cfg.Channel < 1 || cfg.Channel > 165 {
		return fmt.Errorf("invalid channel %d (must be 1-165)", cfg.Channel)
	}

	// Check mode
	if cfg.Mode != "ap" && cfg.Mode != "apsta" {
		return fmt.Errorf("invalid mode %s (must be 'ap' or 'apsta')", cfg.Mode)
	}

	// Check interface
	if cfg.Interface == "" {
		return fmt.Errorf("interface cannot be empty")
	}

	// Validate AP+STA mode requirements
	if cfg.Mode == "apsta" {
		if cfg.UpstreamSSID == "" {
			return fmt.Errorf("upstream SSID required for AP+STA mode")
		}
		if cfg.UpstreamPass == "" {
			return fmt.Errorf("upstream password required for AP+STA mode")
		}

		// Check hardware support
		caps, err := wm.DetectHardwareCapabilities()
		if err != nil {
			return fmt.Errorf("hardware detection failed: %w", err)
		}
		if !caps.SupportsAPSTA {
			return fmt.Errorf("hardware does not support AP+STA mode (detected: %s)", caps.HardwareSupport)
		}
	} else {
		// Check AP mode support
		caps, err := wm.DetectHardwareCapabilities()
		if err != nil {
			return fmt.Errorf("hardware detection failed: %w", err)
		}
		if !caps.SupportsAP {
			return fmt.Errorf("hardware does not support AP mode (detected: %s)", caps.HardwareSupport)
		}
	}

	// Check frequency support
	if cfg.Frequency == "5GHz" && !wm.checkFrequencySupport(cfg.Frequency) {
		return fmt.Errorf("hardware does not support 5GHz mode")
	}

	// Check advanced feature support
	if cfg.EnableHT40 && !wm.checkHT40Support(cfg.Interface) {
		return fmt.Errorf("hardware does not support HT40")
	}
	if cfg.EnableVHT && !wm.checkVHTSupport(cfg.Interface) {
		return fmt.Errorf("hardware does not support VHT")
	}
	if cfg.EnableHE80 && !wm.checkHE80Support(cfg.Interface) {
		return fmt.Errorf("hardware does not support HE80")
	}
	if cfg.EnableWMM && !wm.checkWMMSupport(cfg.Interface) {
		return fmt.Errorf("hardware does not support WMM")
	}

	return nil
}

// checkFrequencySupport checks if a frequency band is supported
func (wm *WiFiHotspotManager) checkFrequencySupport(frequency string) bool {
	caps, err := wm.DetectHardwareCapabilities()
	if err != nil {
		return false
	}
	return stringSliceContains(caps.SupportedBands, frequency)
}

// checkInterfaceExists checks if a network interface exists
func (wm *WiFiHotspotManager) checkInterfaceExists(iface string) bool {
	switch runtime.GOOS {
	case "linux":
		if out, err := runWifiCmd("ip", "link", "show", iface); err == nil {
			return strings.Contains(out, "state")
		}

	case "darwin":
		if _, err := runWifiCmd("ifconfig", iface); err == nil {
			return true
		}

	case "windows":
		if _, err := runWifiCmd("netsh", "interface", "show", "interface", iface); err == nil {
			return true
		}
	}

	return false
}

// StartHotspot starts the platform AP implementation.
// Linux uses hostapd/dnsmasq. macOS uses the system Internet Sharing service.
func (wm *WiFiHotspotManager) StartHotspot(cfg *WiFiHotspotConfig) error {
	cfg = normalizeWiFiHotspotConfig(cfg)
	if cfg.Interface == "" {
		ifaces, err := wm.findWiFiInterfaces()
		if err != nil {
			return fmt.Errorf("detect wifi interface: %w", err)
		}
		cfg.Interface = ifaces[0]
	}
	if runtime.GOOS == "darwin" {
		if os.Geteuid() != 0 {
			return fmt.Errorf("macOS hotspot start requires root privileges to configure Internet Sharing")
		}
		if err := wm.ValidateConfig(cfg); err != nil {
			return err
		}
		wm.mu.Lock()
		defer wm.mu.Unlock()
		return wm.startMacOSHotspotLocked(cfg)
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("wifi hotspot lifecycle is implemented on Linux and macOS only")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("wifi hotspot start requires root privileges for hostapd, interface IP, DHCP, and NAT setup")
	}
	if err := wm.ValidateConfig(cfg); err != nil {
		return err
	}
	if cfg.Mode == "apsta" && !toolExists("wpa_supplicant") {
		return fmt.Errorf("AP+STA mode requires wpa_supplicant in PATH")
	}
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(wm.configPath), 0o700); err != nil {
		return fmt.Errorf("create hotspot state dir: %w", err)
	}
	if wm.status != nil && wm.status.Running {
		return fmt.Errorf("wifi hotspot already running on %s", wm.status.Interface)
	}

	apIface := cfg.Interface
	if cfg.Mode == "apsta" {
		apIface = firstNonEmptyStr(cfg.APInterface, cfg.Interface+"ap")
		if cfg.UpstreamIF == "" {
			cfg.UpstreamIF = cfg.Interface
		}
		if apIface != cfg.Interface && !wm.checkInterfaceExists(apIface) {
			if _, err := runWifiCmd("iw", "dev", cfg.Interface, "interface", "add", apIface, "type", "__ap"); err != nil {
				return fmt.Errorf("create AP virtual interface %s from %s: %w", apIface, cfg.Interface, err)
			}
		}
		cfg.APInterface = apIface
	} else if cfg.APInterface != "" {
		apIface = cfg.APInterface
	}

	if cfg.Mode == "apsta" {
		if err := wm.GenerateAPSTAWpaSupplicantConfig(cfg); err != nil {
			return err
		}
		if err := os.MkdirAll(wm.wpaCtrlPath, 0o700); err != nil {
			return fmt.Errorf("create wpa_supplicant control dir: %w", err)
		}
		if err := wm.startAPSTAWpaSupplicant(cfg.Interface); err != nil {
			return err
		}
	}
	if err := wm.GenerateHostapdConfig(cfg); err != nil {
		_ = wm.stopAPSTAWpaSupplicant()
		return err
	}
	if cfg.EnableDHCP {
		if err := wm.GenerateDnsmasqConfig(cfg); err != nil {
			_ = wm.stopAPSTAWpaSupplicant()
			return err
		}
	}
	if err := wm.configureAPInterface(apIface, cfg.IPAddress); err != nil {
		_ = wm.stopAPSTAWpaSupplicant()
		return err
	}
	if cfg.EnableNAT {
		if err := wm.enableNAT(apIface, firstNonEmptyStr(cfg.UpstreamIF, wm.defaultRouteInterface())); err != nil {
			_ = wm.stopAPSTAWpaSupplicant()
			return err
		}
	}
	if cfg.EnableDHCP {
		if err := wm.startDnsmasq(); err != nil {
			_ = wm.disableNAT(apIface, firstNonEmptyStr(cfg.UpstreamIF, wm.defaultRouteInterface()))
			_ = wm.stopAPSTAWpaSupplicant()
			return err
		}
	}
	if err := wm.startHostapd(); err != nil {
		_ = wm.stopDnsmasq()
		_ = wm.disableNAT(apIface, firstNonEmptyStr(cfg.UpstreamIF, wm.defaultRouteInterface()))
		_ = wm.stopAPSTAWpaSupplicant()
		return err
	}

	wm.config = cfg
	wm.wifiInterface = apIface
	wm.startedAt = time.Now()
	wm.status = &WiFiHotspotStatus{
		Running:         true,
		Mode:            cfg.Mode,
		SSID:            cfg.SSID,
		Interface:       apIface,
		BridgeName:      cfg.BridgeName,
		IPAddress:       cfg.IPAddress,
		UpstreamStatus:  wm.upstreamStatus(cfg),
		SupportedModes:  wm.detectSupportedModes(cfg.Interface),
		HardwareSupport: "unknown",
	}
	if caps, err := wm.DetectHardwareCapabilities(); err == nil {
		wm.status.HardwareSupport = caps.HardwareSupport
		wm.status.SupportedModes = caps.SupportedModes
	}
	return nil
}

// StopHotspot stops the platform AP implementation and removes Yaver-owned plumbing.
func (wm *WiFiHotspotManager) StopHotspot() error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if runtime.GOOS == "darwin" {
		return wm.stopMacOSHotspotLocked()
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("wifi hotspot lifecycle is implemented on Linux and macOS only")
	}
	cfg := wm.config
	apIface := wm.wifiInterface
	if cfg != nil && cfg.APInterface != "" {
		apIface = cfg.APInterface
	}
	if apIface == "" && cfg != nil {
		apIface = cfg.Interface
	}
	_ = wm.stopHostapd()
	_ = wm.stopDnsmasq()
	_ = wm.stopAPSTAWpaSupplicant()
	if cfg != nil && cfg.EnableNAT {
		_ = wm.disableNAT(apIface, firstNonEmptyStr(cfg.UpstreamIF, wm.defaultRouteInterface()))
	}
	if cfg != nil && cfg.Mode == "apsta" && cfg.APInterface != "" && cfg.APInterface != cfg.Interface && wm.checkInterfaceExists(cfg.APInterface) {
		_, _ = runWifiCmd("ip", "link", "delete", cfg.APInterface)
	}
	_ = os.Remove(wm.pidPath)
	_ = os.Remove(wm.dnsmasqPIDPath)
	_ = os.Remove(wm.wpaPIDPath)

	wm.pid = 0
	wm.dnsmasqPID = 0
	wm.startedAt = time.Time{}
	wm.status = &WiFiHotspotStatus{
		Running:         false,
		SupportedModes:  []string{"ap"},
		HardwareSupport: "unknown",
	}
	wm.config = nil
	wm.wifiInterface = ""
	return nil
}

// GetStatus returns current hotspot status and refreshes process/client details.
func (wm *WiFiHotspotManager) GetStatus() (*WiFiHotspotStatus, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	st := *wm.status
	if st.Interface == "" && wm.config != nil {
		st.Interface = firstNonEmptyStr(wm.config.APInterface, wm.config.Interface)
	}
	if runtime.GOOS == "linux" {
		st.Running = processRunningFromPIDFile(wm.pidPath)
		if st.Running && !wm.startedAt.IsZero() {
			st.Uptime = time.Since(wm.startedAt).Round(time.Second).String()
		}
		if st.Interface != "" {
			st.ConnectedClients = wm.connectedClientCount(st.Interface)
		}
		if wm.config != nil {
			st.UpstreamStatus = wm.upstreamStatus(wm.config)
		}
	} else if runtime.GOOS == "darwin" {
		st.Running = wm.macOSInternetSharingRunning()
		if st.Running && !wm.startedAt.IsZero() {
			st.Uptime = time.Since(wm.startedAt).Round(time.Second).String()
		}
	}
	return &st, nil
}

// GenerateHostapdConfig writes a minimal WPA2 hostapd config.
func (wm *WiFiHotspotManager) GenerateHostapdConfig(cfg *WiFiHotspotConfig) error {
	cfg = normalizeWiFiHotspotConfig(cfg)
	apIface := firstNonEmptyStr(cfg.APInterface, cfg.Interface)
	hwMode := "g"
	if cfg.Frequency == "5GHz" {
		hwMode = "a"
	}
	lines := []string{
		"interface=" + apIface,
		"driver=nl80211",
		"ssid=" + cfg.SSID,
		"hw_mode=" + hwMode,
		"channel=" + strconv.Itoa(cfg.Channel),
		"auth_algs=1",
		"wpa=2",
		"wpa_passphrase=" + cfg.Password,
		"wpa_key_mgmt=WPA-PSK",
		"rsn_pairwise=CCMP",
	}
	if cfg.CountryCode != "" {
		lines = append(lines, "country_code="+strings.ToUpper(cfg.CountryCode), "ieee80211d=1")
	}
	if cfg.EnableWMM {
		lines = append(lines, "wmm_enabled=1")
	}
	if cfg.EnableHT40 {
		lines = append(lines, "ieee80211n=1", "ht_capab=[HT40+][SHORT-GI-20][SHORT-GI-40]")
	}
	if cfg.EnableVHT {
		lines = append(lines, "ieee80211ac=1")
	}
	if cfg.BridgeName != "" {
		lines = append(lines, "bridge="+cfg.BridgeName)
	}
	return os.WriteFile(wm.hostapdConfigPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// GenerateDnsmasqConfig writes DHCP/DNS config for the AP interface.
func (wm *WiFiHotspotManager) GenerateDnsmasqConfig(cfg *WiFiHotspotConfig) error {
	cfg = normalizeWiFiHotspotConfig(cfg)
	apIface := firstNonEmptyStr(cfg.APInterface, cfg.Interface)
	gateway := cfg.IPAddress
	if slash := strings.Index(gateway, "/"); slash >= 0 {
		gateway = gateway[:slash]
	}
	lines := []string{
		"interface=" + apIface,
		"bind-interfaces",
		"dhcp-range=" + cfg.DHCPRange + ",12h",
		"dhcp-option=3," + gateway,
		"dhcp-option=6," + gateway,
		"server=1.1.1.1",
		"server=8.8.8.8",
		"cache-size=150",
	}
	return os.WriteFile(wm.dnsmasqConfigPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// GenerateAPSTAWpaSupplicantConfig writes the STA uplink config for AP+STA mode.
func (wm *WiFiHotspotManager) GenerateAPSTAWpaSupplicantConfig(cfg *WiFiHotspotConfig) error {
	cfg = normalizeWiFiHotspotConfig(cfg)
	if err := os.MkdirAll(filepath.Dir(wm.wpaConfigPath), 0o700); err != nil {
		return err
	}
	lines := []string{
		"ctrl_interface=" + wm.wpaCtrlPath,
		"update_config=0",
	}
	if cfg.CountryCode != "" {
		lines = append(lines, "country="+strings.ToUpper(cfg.CountryCode))
	}
	network := []string{
		"network={",
		"\tssid=\"" + escapeWPAString(cfg.UpstreamSSID) + "\"",
		"\tkey_mgmt=WPA-PSK",
		"\tpsk=\"" + escapeWPAString(cfg.UpstreamPass) + "\"",
		"}",
	}
	lines = append(lines, network...)
	return os.WriteFile(wm.wpaConfigPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func normalizeWiFiHotspotConfig(cfg *WiFiHotspotConfig) *WiFiHotspotConfig {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	if cp.Mode == "" {
		cp.Mode = "ap"
	}
	if cp.Channel == 0 {
		cp.Channel = 6
	}
	if cp.Frequency == "" {
		cp.Frequency = "2.4GHz"
	}
	if cp.IPAddress == "" {
		cp.IPAddress = "192.168.47.1/24"
	}
	if cp.DHCPRange == "" {
		cp.DHCPRange = "192.168.47.100,192.168.47.200"
	}
	if cp.EnableNAT && cp.UpstreamIF == "" && cp.Mode == "apsta" {
		cp.UpstreamIF = cp.Interface
	}
	return &cp
}

func (wm *WiFiHotspotManager) configureAPInterface(iface, cidr string) error {
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

func (wm *WiFiHotspotManager) startHostapd() error {
	cmd := osexec.Command("hostapd", "-B", "-P", wm.pidPath, "-f", wm.logPath, wm.hostapdConfigPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start hostapd: %w: %s", err, clipString(strings.TrimSpace(string(out)), 500))
	}
	wm.pid = readPIDFile(wm.pidPath)
	return nil
}

func (wm *WiFiHotspotManager) stopHostapd() error {
	return stopPIDFile(wm.pidPath)
}

func (wm *WiFiHotspotManager) startDnsmasq() error {
	cmd := osexec.Command("dnsmasq", "--conf-file="+wm.dnsmasqConfigPath, "--pid-file="+wm.dnsmasqPIDPath, "--log-facility="+wm.logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start dnsmasq: %w: %s", err, clipString(strings.TrimSpace(string(out)), 500))
	}
	wm.dnsmasqPID = readPIDFile(wm.dnsmasqPIDPath)
	return nil
}

func (wm *WiFiHotspotManager) stopDnsmasq() error {
	return stopPIDFile(wm.dnsmasqPIDPath)
}

func (wm *WiFiHotspotManager) startAPSTAWpaSupplicant(iface string) error {
	cmd := osexec.Command("wpa_supplicant", "-B", "-i", iface, "-c", wm.wpaConfigPath, "-P", wm.wpaPIDPath, "-f", wm.logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start AP+STA wpa_supplicant: %w: %s", err, clipString(strings.TrimSpace(string(out)), 500))
	}
	return nil
}

func (wm *WiFiHotspotManager) stopAPSTAWpaSupplicant() error {
	return stopPIDFile(wm.wpaPIDPath)
}

func (wm *WiFiHotspotManager) enableNAT(apIface, upstreamIface string) error {
	if upstreamIface == "" {
		return fmt.Errorf("upstream interface required for NAT")
	}
	if _, err := runWifiCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable ip forwarding: %w", err)
	}
	_ = iptablesEnsure("-t", "nat", "-A", "POSTROUTING", "-o", upstreamIface, "-j", "MASQUERADE")
	_ = iptablesEnsure("-A", "FORWARD", "-i", upstreamIface, "-o", apIface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	_ = iptablesEnsure("-A", "FORWARD", "-i", apIface, "-o", upstreamIface, "-j", "ACCEPT")
	return nil
}

func (wm *WiFiHotspotManager) disableNAT(apIface, upstreamIface string) error {
	if upstreamIface == "" || apIface == "" {
		return nil
	}
	_, _ = runWifiCmd("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", upstreamIface, "-j", "MASQUERADE")
	_, _ = runWifiCmd("iptables", "-D", "FORWARD", "-i", upstreamIface, "-o", apIface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	_, _ = runWifiCmd("iptables", "-D", "FORWARD", "-i", apIface, "-o", upstreamIface, "-j", "ACCEPT")
	return nil
}

func iptablesEnsure(args ...string) error {
	check := append([]string{}, args...)
	for i, arg := range check {
		if arg == "-A" {
			check[i] = "-C"
			break
		}
	}
	if _, err := runWifiCmd("iptables", check...); err == nil {
		return nil
	}
	_, err := runWifiCmd("iptables", args...)
	return err
}

func (wm *WiFiHotspotManager) defaultRouteInterface() string {
	if runtime.GOOS == "darwin" {
		out, err := runWifiCmd("route", "-n", "get", "default")
		if err != nil {
			return ""
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) == 2 && fields[0] == "interface:" {
				return fields[1]
			}
		}
		return ""
	}
	out, err := runWifiCmd("sh", "-c", "ip route show default | awk 'NR==1 {for (i=1;i<=NF;i++) if ($i==\"dev\") print $(i+1)}'")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (wm *WiFiHotspotManager) connectedClientCount(iface string) int {
	out, err := runWifiCmd("hostapd_cli", "-i", iface, "all_sta")
	if err != nil || strings.TrimSpace(out) == "" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Count(line, ":") == 5 && !strings.Contains(line, "=") {
			count++
		}
	}
	return count
}

func (wm *WiFiHotspotManager) startMacOSHotspotLocked(cfg *WiFiHotspotConfig) error {
	if cfg.Mode != "ap" {
		return fmt.Errorf("macOS supports Yaver hotspot mode through Internet Sharing only; AP+STA requires Linux hostapd/nl80211 support")
	}
	if err := os.MkdirAll(filepath.Dir(wm.configPath), 0o700); err != nil {
		return fmt.Errorf("create hotspot state dir: %w", err)
	}
	if wm.status != nil && wm.status.Running {
		return fmt.Errorf("wifi hotspot already running on %s", wm.status.Interface)
	}

	upstream := firstNonEmptyStr(cfg.UpstreamIF, wm.defaultRouteInterface())
	if upstream == "" {
		return fmt.Errorf("upstream interface required for macOS Internet Sharing")
	}
	if upstream == cfg.Interface {
		return fmt.Errorf("macOS Internet Sharing needs distinct upstream and Wi-Fi AP interfaces")
	}

	if _, err := runWifiCmd("scutil", "--set", "ComputerName", cfg.SSID); err != nil {
		return fmt.Errorf("set macOS hotspot name from ComputerName: %w", err)
	}
	if localName := macOSLocalHostName(cfg.SSID); localName != "" {
		if _, err := runWifiCmd("scutil", "--set", "LocalHostName", localName); err != nil {
			return fmt.Errorf("set macOS hotspot name from LocalHostName: %w", err)
		}
	}
	if _, err := runWifiCmd("defaults", "write", "/Library/Preferences/SystemConfiguration/com.apple.nat", "NAT", "-dict", "Enabled", "-int", "1", "PrimaryInterface", upstream, "SharingDevices", "-array", cfg.Interface); err != nil {
		return fmt.Errorf("configure macOS Internet Sharing NAT: %w", err)
	}
	_, _ = runWifiCmd("launchctl", "bootout", "system", "/System/Library/LaunchDaemons/com.apple.InternetSharing.plist")
	if _, err := runWifiCmd("launchctl", "bootstrap", "system", "/System/Library/LaunchDaemons/com.apple.InternetSharing.plist"); err != nil {
		_, _ = runWifiCmd("launchctl", "kickstart", "-k", "system/com.apple.InternetSharing")
		if !wm.macOSInternetSharingRunning() {
			return fmt.Errorf("start macOS Internet Sharing: %w", err)
		}
	}

	wm.config = cfg
	wm.wifiInterface = cfg.Interface
	wm.startedAt = time.Now()
	wm.status = &WiFiHotspotStatus{
		Running:         wm.macOSInternetSharingRunning(),
		Mode:            "ap",
		SSID:            cfg.SSID,
		Interface:       cfg.Interface,
		UpstreamStatus:  "shared:" + upstream,
		SupportedModes:  []string{"ap"},
		HardwareSupport: "ap-only",
	}
	return nil
}

func (wm *WiFiHotspotManager) stopMacOSHotspotLocked() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("macOS hotspot stop requires root privileges to configure Internet Sharing")
	}
	_, _ = runWifiCmd("launchctl", "bootout", "system", "/System/Library/LaunchDaemons/com.apple.InternetSharing.plist")
	_, _ = runWifiCmd("defaults", "write", "/Library/Preferences/SystemConfiguration/com.apple.nat", "NAT", "-dict", "Enabled", "-int", "0")
	wm.startedAt = time.Time{}
	wm.config = nil
	wm.wifiInterface = ""
	wm.status = &WiFiHotspotStatus{
		Running:         false,
		SupportedModes:  []string{"ap"},
		HardwareSupport: "ap-only",
	}
	return nil
}

func (wm *WiFiHotspotManager) macOSInternetSharingRunning() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if _, err := runWifiCmd("pgrep", "-x", "InternetSharing"); err == nil {
		return true
	}
	out, err := runWifiCmd("defaults", "read", "/Library/Preferences/SystemConfiguration/com.apple.nat", "NAT")
	if err != nil {
		return false
	}
	return strings.Contains(out, "Enabled = 1") || strings.Contains(out, "Enabled = true")
}

func macOSLocalHostName(ssid string) string {
	var b strings.Builder
	for _, r := range ssid {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' && b.Len() > 0:
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '.':
			if b.Len() > 0 {
				b.WriteRune('-')
			}
		}
		if b.Len() >= 63 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func (wm *WiFiHotspotManager) ListClients() []map[string]interface{} {
	wm.mu.Lock()
	iface := wm.wifiInterface
	if iface == "" && wm.config != nil {
		iface = firstNonEmptyStr(wm.config.APInterface, wm.config.Interface)
	}
	wm.mu.Unlock()
	if iface == "" {
		return nil
	}

	out, err := runWifiCmd("hostapd_cli", "-i", iface, "all_sta")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var clients []map[string]interface{}
	var current map[string]interface{}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Count(line, ":") == 5 && !strings.Contains(line, "=") {
			if current != nil {
				clients = append(clients, current)
			}
			current = map[string]interface{}{"mac": line}
			continue
		}
		if current == nil || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		current[parts[0]] = parts[1]
	}
	if current != nil {
		clients = append(clients, current)
	}
	return clients
}

func (wm *WiFiHotspotManager) KickClient(mac string) error {
	wm.mu.Lock()
	iface := wm.wifiInterface
	if iface == "" && wm.config != nil {
		iface = firstNonEmptyStr(wm.config.APInterface, wm.config.Interface)
	}
	wm.mu.Unlock()
	if strings.TrimSpace(mac) == "" {
		return fmt.Errorf("mac address required")
	}
	if iface == "" {
		return fmt.Errorf("wifi hotspot is not running")
	}
	if _, err := runWifiCmd("hostapd_cli", "-i", iface, "deauthenticate", mac); err != nil {
		return fmt.Errorf("deauthenticate %s: %w", mac, err)
	}
	return nil
}

func (wm *WiFiHotspotManager) BanClient(mac string, durationHours int) error {
	mac = strings.TrimSpace(strings.ToLower(mac))
	if mac == "" {
		return fmt.Errorf("mac address required")
	}
	var expires time.Time
	if durationHours > 0 {
		expires = time.Now().Add(time.Duration(durationHours) * time.Hour)
	}
	wm.mu.Lock()
	if wm.bannedClients == nil {
		wm.bannedClients = make(map[string]time.Time)
	}
	wm.bannedClients[mac] = expires
	wm.mu.Unlock()
	_ = wm.KickClient(mac)
	return nil
}

func (wm *WiFiHotspotManager) UnbanClient(mac string) error {
	mac = strings.TrimSpace(strings.ToLower(mac))
	if mac == "" {
		return fmt.Errorf("mac address required")
	}
	wm.mu.Lock()
	delete(wm.bannedClients, mac)
	wm.mu.Unlock()
	return nil
}

func (wm *WiFiHotspotManager) GetBannedClients() map[string]time.Time {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	out := make(map[string]time.Time, len(wm.bannedClients))
	now := time.Now()
	for mac, expires := range wm.bannedClients {
		if !expires.IsZero() && now.After(expires) {
			delete(wm.bannedClients, mac)
			continue
		}
		out[mac] = expires
	}
	return out
}

func (wm *WiFiHotspotManager) SetAPSTAConfig(cfg *WiFiHotspotConfig) error {
	cfg = normalizeWiFiHotspotConfig(cfg)
	if err := os.MkdirAll(filepath.Dir(wm.configPath), 0o700); err != nil {
		return fmt.Errorf("create hotspot config dir: %w", err)
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(wm.configPath, append(body, '\n'), 0o600)
}

func (wm *WiFiHotspotManager) GetAPSTAConfig() (*WiFiHotspotConfig, error) {
	body, err := os.ReadFile(wm.configPath)
	if err != nil {
		return nil, err
	}
	var cfg WiFiHotspotConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	return normalizeWiFiHotspotConfig(&cfg), nil
}

func (wm *WiFiHotspotManager) upstreamStatus(cfg *WiFiHotspotConfig) string {
	if cfg == nil || cfg.Mode != "apsta" {
		return ""
	}
	if runtime.GOOS != "linux" {
		return "unsupported"
	}
	iface := firstNonEmptyStr(cfg.UpstreamIF, cfg.Interface)
	out, err := runWifiCmd("iw", "dev", iface, "link")
	if err != nil {
		return "unknown"
	}
	if strings.Contains(strings.ToLower(out), "connected to") {
		return "connected"
	}
	return "disconnected"
}

func processRunningFromPIDFile(path string) bool {
	pid := readPIDFile(path)
	if pid <= 0 {
		return false
	}
	err := osexec.Command("kill", "-0", strconv.Itoa(pid)).Run()
	return err == nil
}

func readPIDFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

func stopPIDFile(path string) error {
	pid := readPIDFile(path)
	if pid <= 0 {
		return nil
	}
	_, _ = runWifiCmd("kill", strconv.Itoa(pid))
	for i := 0; i < 20; i++ {
		if !processRunningFromPIDFile(path) {
			_ = os.Remove(path)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, _ = runWifiCmd("kill", "-9", strconv.Itoa(pid))
	_ = os.Remove(path)
	return nil
}

// Helper functions

func stringSliceContains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func clipString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
