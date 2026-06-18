# Yaver WiFi Hotspot Implementation Guide

## Overview

This document provides deep technical guidance for implementing the WiFi hotspot feature in Yaver that works across mobile apps, PC terminals, and cloud runners with comprehensive troubleshooting capabilities.

## Architecture Design

### Current State

**File**: `desktop/agent/wifi_hotspot.go` (805 lines)

**Completed Components**:
- Hardware detection and capability analysis
- Cross-platform WiFi interface discovery (Linux/macOS/Windows)
- Driver identification for Intel, Realtek, Broadcom, Atheros chipsets
- Configuration validation for AP mode and AP+STA (repeater) mode
- Hardware support classification (full, ap-only, apsta-only, none)

**Implemented Core Functionality**:
- Hotspot lifecycle management (`StartHotspot`, `StopHotspot`, `GetStatus`)
- Linux `hostapd` config generation and process start/stop
- Linux `dnsmasq` DHCP/DNS config generation and process start/stop
- Linux iptables masquerade/forwarding setup for NAT mode
- `/console/wifi/{capabilities,status,start,stop}` owner-authenticated HTTP API
- `/ops` verbs: `wifi_capabilities`, `wifi_status`, `wifi_start`, `wifi_stop`
- Mobile Console Wi-Fi tab for AP/repeater start/stop and status
- PID file management and basic process monitoring

**Still Hardware-Lab Required**:
- Real Linux AP smoke test with `hostapd` + `dnsmasq`
- Real AP+STA repeater smoke test on a radio whose `iw phy` valid interface
  combinations support simultaneous managed+AP mode
- NetworkManager coexistence hardening on desktops where NM owns the Wi-Fi NIC
- nftables backend support for hosts without iptables compatibility

### System Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Yaver WiFi Hotspot                   │
├─────────────────────────────────────────────────────────┤
│                                                          │
│  ┌────────────────────────────────────────────────┐   │
│  │         WiFiHotspotManager                    │   │
│  │  - DetectHardwareCapabilities()              │   │
│  │  - ValidateConfig()                          │   │
│  │  - StartHotspot()                            │   │
│  │  - StopHotspot()                             │   │
│  │  - GetStatus()                               │   │
│  └────────────────────────────────────────────────┘   │
│                         │                              │
│  ┌──────────────────────┼────────────────────────┐   │
│  │                      │                        │   │
│  ▼                      ▼                        ▼   │
│  ┌──────────┐      ┌──────────┐          ┌──────────┐│
│  │ hostapd  │      │ dnsmasq  │          │  NAT/IP  ││
│  │ (AP)     │      │ (DHCP)   │          │ Tables   ││
│  └──────────┘      └──────────┘          └──────────┘│
│       │                 │                      │      │
│       ▼                 ▼                      ▼      │
│  ┌──────────────────────────────────────────────────┐│
│  │              MCP Tools Layer                     ││
│  │  - mcp_wifi_start                               ││
│  │  - mcp_wifi_stop                                ││
│  │  - mcp_wifi_status                              ││
│  │  - mcp_wifi_diagnose                            ││
│  └──────────────────────────────────────────────────┘│
│                          │                             │
│                          ▼                             │
│              ┌───────────────────────┐                 │
│              │   Mobile Integration  │                 │
│              │   - gateway_*_driver.go│                 │
│              │   - status broadcasting│                 │
│              └───────────────────────┘                 │
└─────────────────────────────────────────────────────┘
```

## Implementation Tasks

### Phase 1: Complete Core Hotspot Manager Methods

**Task**: Implement missing lifecycle methods in `desktop/agent/wifi_hotspot.go`

**Required Methods**:

```go
// StartHotspot starts the WiFi hotspot with given configuration
func (wm *WiFiHotspotManager) StartHotspot(cfg *WiFiHotspotConfig) error

// StopHotspot stops the running WiFi hotspot
func (wm *WiFiHotspotManager) StopHotspot() error

// GetStatus returns current hotspot status
func (wm *WiFiHotspotManager) GetStatus() (*WiFiHotspotStatus, error)

// GenerateHostapdConfig creates hostapd configuration file
func (wm *WiFiHotspotManager) GenerateHostapdConfig(cfg *WiFiHotspotConfig) error

// GenerateDnsmasqConfig creates dnsmasq configuration file
func (wm *WiFiHotspotManager) GenerateDnsmasqConfig(cfg *WiFiHotspotConfig) error
```

**Implementation Details**:

1. **StartHotspot()** should:
   - Validate configuration using existing `ValidateConfig()`
   - Detect hardware capabilities
   - Generate hostapd.conf file
   - Generate dnsmasq.conf file
   - Start dnsmasq process (if DHCP enabled)
   - Start hostapd process
   - Configure interface IP address
   - Set up iptables NAT rules for AP+STA mode
   - Write PID file for process tracking
   - Update status with running state

2. **StopHotspot()** should:
   - Check if hotspot is running
   - Kill hostapd process
   - Kill dnsmasq process
   - Clean up iptables rules
   - Remove PID files
   - Reset interface state
   - Update status to stopped

3. **GetStatus()** should:
   - Check if processes are running
   - Get connected client count
   - Check upstream connection status (for AP+STA)
   - Calculate uptime
   - Return comprehensive status

**Hostapd Configuration Template**:

```go
// Example hostapd.conf for AP mode
interface=wlan0
driver=nl80211
ssid=YaverHotspot
hw_mode=g
channel=6
ieee80211n=1
ieee80211ac=1
wmm_enabled=1
auth_algs=1
wpa=2
wpa_passphrase=yourpassword
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
```

**Dnsmasq Configuration Template**:

```go
// Example dnsmasq.conf
interface=wlan0
dhcp-range=192.168.4.100,192.168.4.200,12h
dhcp-option=3,192.168.4.1
dhcp-option=6,192.168.4.1
server=8.8.8.8
server=8.8.4.4
cache-size=150
```

### Phase 2: MCP Integration

**Task**: Add MCP tool handlers in new file `desktop/agent/mcp_wifi.go`

**Required MCP Tools**:

```go
// mcp_wifi_start - starts WiFi hotspot
// mcp_wifi_stop - stops WiFi hotspot
// mcp_wifi_status - gets current status
// mcp_wifi_config - gets/sets configuration
// mcp_wifi_diagnose - runs diagnostics
```

**Example Implementation Pattern** (from existing MCP handlers):

```go
func (h *HTTPServer) mcp_wifi_start(w http.ResponseWriter, r *http.Request) {
    var args struct {
        SSID      string `json:"ssid"`
        Password  string `json:"password"`
        Mode      string `json:"mode"`
        Interface string `json:"interface"`
    }
    if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    cfg := &WiFiHotspotConfig{
        SSID:      args.SSID,
        Password:  args.Password,
        Mode:      args.Mode,
        Interface: args.Interface,
    }

    // Create or get manager instance
    // Start hotspot
    // Return status
}

func (h *HTTPServer) mcp_wifi_stop(w http.ResponseWriter, r *http.Request) {
    // Stop hotspot logic
}

func (h *HTTPServer) mcp_wifi_status(w http.ResponseWriter, r *http.Request) {
    // Get status logic
}

func (h *HTTPServer) mcp_wifi_diagnose(w http.ResponseWriter, r *http.Request) {
    // Run comprehensive diagnostics
    // Return diagnostic results
}
```

**Registration** (in HTTPServer constructor):

```go
// Add to existing MCP handler registrations
m.HandleFunc("/wifi/start", h.mcp_wifi_start)
m.HandleFunc("/wifi/stop", h.mcp_wifi_stop)
m.HandleFunc("/wifi/status", h.mcp_wifi_status)
m.HandleFunc("/wifi/diagnose", h.mcp_wifi_diagnose)
```

### Phase 3: Service Manager Integration

**Task**: Integrate WiFi hotspot with Yaver's service management system

**File**: `desktop/agent/services.go` (reference)

**Integration Points**:

1. **Add WiFi hotspot service type**:
   ```go
   const ServiceTypeWiFiHotspot = "wifi-hotspot"
   ```

2. **Add WiFi service handler**:
   ```go
   func (sm *ServiceManager) manageWiFiHotspot(service *Service, action string) error {
       switch action {
       case "start":
           // Start WiFi hotspot
       case "stop":
           // Stop WiFi hotspot
       case "restart":
           // Restart WiFi hotspot
       default:
           return fmt.Errorf("invalid action: %s", action)
       }
       return nil
   }
   ```

3. **Service configuration format** (`.yaver/services.yaml`):
   ```yaml
   services:
     wifi-hotspot:
       type: wifi-hotspot
       enabled: true
       config:
         ssid: YaverHotspot
         password: securepassword
         mode: ap
         interface: wlan0
         enable_dhcp: true
   ```

### Phase 4: Mobile Integration

**Task**: Create mobile gateway integration for WiFi control

**Files**: `mobile/app/` and `mobile/android/` / `mobile/ios/`

**Integration Components**:

1. **Gateway Driver** (`mobile/app/src/gateway/gateway_wifi_driver.go`):
   ```go
   package gateway

   type WiFiDriver struct {
       baseURL string
   }

   func NewWiFiDriver(baseURL string) *WiFiDriver {
       return &WiFiDriver{baseURL: baseURL}
   }

   func (wd *WiFiDriver) StartHotspot(config WiFiConfig) error {
       // Call MCP endpoint
       url := fmt.Sprintf("%s/mcp/wifi/start", wd.baseURL)
       // Send request
       return nil
   }

   func (wd *WiFiDriver) StopHotspot() error {
       // Call MCP endpoint
       return nil
   }

   func (wd *WiFiDriver) GetStatus() (*WiFiStatus, error) {
       // Call MCP endpoint
       return nil, nil
   }

   func (wd *WiFiDriver) Diagnose() (*WiFiDiagnostics, error) {
       // Call diagnostic endpoint
       return nil, nil
   }
   ```

2. **Mobile UI Components**:

   **Android** (`mobile/android/app/src/main/java/io/yaver/mobile/WiFiHotspotActivity.kt`):
   ```kotlin
   class WiFiHotspotActivity : AppCompatActivity() {
       private val wifiDriver: WiFiDriver by lazy {
           WiFiDriver(getAgentBaseURL())
       }

       override fun onCreate(savedInstanceState: Bundle?) {
           super.onCreate(savedInstanceState)
           setContentView(R.layout.activity_wifi_hotspot)

           setupUI()
           loadStatus()
       }

       private fun startHotspot(ssid: String, password: String, mode: String) {
           lifecycleScope.launch {
               try {
                   val config = WiFiConfig(
                       ssid = ssid,
                       password = password,
                       mode = mode
                   )
                   wifiDriver.startHotspot(config)
                   showSuccess("Hotspot started")
                   loadStatus()
               } catch (e: Exception) {
                   showError(e.message)
               }
           }
       }
   }
   ```

   **iOS** (`mobile/ios/YaverMobile/WiFiHotspotViewController.swift`):
   ```swift
   import UIKit

   class WiFiHotspotViewController: UIViewController {
       private var statusTimer: Timer?

       override func viewDidLoad() {
           super.viewDidLoad()
           setupUI()
           loadStatus()
           startStatusUpdates()
       }

       private func startHotspot(ssid: String, password: String, mode: String) {
           let config = WiFiConfig(
               ssid: ssid,
               password: password,
               mode: mode
           )

           WiFiDriver.shared.startHotspot(config: config) { result in
               DispatchQueue.main.async {
                   if result.success {
                       self.showSuccess("Hotspot started")
                       self.loadStatus()
                   } else {
                       self.showError(result.error)
                   }
               }
           }
       }
   }
   ```

3. **Status Broadcasting** (from existing gateway patterns):

   Use the existing accessibility service pattern for background status updates:
   - Extend `GatewayAccessibilityService`
   - Add WiFi hotspot status announcements
   - Broadcast status to mobile app via existing channels

### Phase 5: Advanced Features

**Task**: Implement advanced WiFi features

1. **AP+STA Mode Implementation**:
   ```go
   func (wm *WiFiHotspotManager) setupAPSTAMode(cfg *WiFiHotspotConfig) error {
       // Create virtual interface for STA mode
       staIface := cfg.Interface + "_sta"

       // Connect to upstream network
       if err := wm.connectToUpstream(staIface, cfg.UpstreamSSID, cfg.UpstreamPass); err != nil {
           return fmt.Errorf("connect to upstream: %w", err)
       }

       // Set up NAT between STA and AP interfaces
       if err := wm.setupNATRules(staIface, cfg.Interface); err != nil {
           return fmt.Errorf("setup NAT rules: %w", err)
       }

       // Enable IP forwarding
       if err := wm.enableIPForwarding(); err != nil {
           return fmt.Errorf("enable IP forwarding: %w", err)
       }

       return nil
   }
   ```

2. **Client Management**:
   ```go
   func (wm *WiFiHotspotManager) GetConnectedClients() ([]WiFiClient, error) {
       if !wm.status.Running {
           return nil, fmt.Errorf("hotspot not running")
       }

       var clients []WiFiClient

       // Get hostapd station list
       if out, err := runCmd("hostapd_cli", "-p", "6666", "all_sta"); err == nil {
           // Parse hostapd output
           stations := strings.Split(out, "\n")
           for _, station := range stations {
               client := wm.parseStationInfo(station)
               if client != nil {
                   clients = append(clients, *client)
               }
           }
       }

       return clients, nil
   }
   ```

3. **Diagnostics System**:
   ```go
   func (wm *WiFiHotspotManager) RunDiagnostics() (*WiFiDiagnostics, error) {
       diagnostics := &WiFiDiagnostics{
           Timestamp: time.Now(),
       }

       // Check hostapd status
       diagnostics.HostapdRunning = wm.isProcessRunning("hostapd")
       diagnostics.HostapdVersion = wm.getHostapdVersion()

       // Check dnsmasq status
       diagnostics.DnsmasqRunning = wm.isProcessRunning("dnsmasq")
       diagnostics.DnsmasqVersion = wm.getDnsmasqVersion()

       // Check interface status
       diagnostics.InterfaceUp = wm.isInterfaceUp(wm.status.Interface)
       diagnostics.InterfaceIP = wm.getInterfaceIP(wm.status.Interface)

       // Check firewall rules
       diagnostics.NATRulesCorrect = wm.checkNATRules()
       diagnostics.IPForwardingEnabled = wm.checkIPForwarding()

       // Check hardware
       hardwareCaps, err := wm.DetectHardwareCapabilities()
       if err == nil {
           diagnostics.HardwareCapabilities = hardwareCaps
       }

       // Get signal information
       diagnostics.SignalStrength = wm.getSignalStrength()
       diagnostics.ChannelUtilization = wm.getChannelUtilization()

       // Check for common issues
       diagnostics.Issues = wm.detectCommonIssues()

       return diagnostics, nil
   }
   ```

## Platform-Specific Considerations

### Linux

**Requirements**:
- `hostapd` package (AP functionality)
- `dnsmasq` package (DHCP/DNS)
- `iw` and `iwconfig` tools
- `iproute2` for network management
- `iptables` for NAT rules

**Common Issues**:
- Driver compatibility check before starting
- Ensure `nl80211` driver support
- Check for regulatory domain restrictions
- Handle multiple WiFi interfaces correctly

### macOS

**Requirements**:
- `airport` command-line tool
- `pf` firewall for NAT rules
- `bootpd` for DHCP server (alternative to dnsmasq)

**Limitations**:
- macOS has limited native AP mode support
- Requires creating network locations
- Often needs third-party tools or mobile hotspot framework

**Workaround**: Use macOS Internet Sharing via system commands:
```go
func (wm *WiFiHotspotManager) enableMacOSSharing() error {
    // Use networksetup to enable internet sharing
    _, err := runCmd("networksetup", "-setsharingwith", "on")
    return err
}
```

### Windows

**Requirements**:
- `netsh` command for WLAN management
- Windows Mobile Hotspot API
- Hosted Network feature (limited support)

**Limitations**:
- Windows Hosted Network is deprecated
- Requires newer Windows versions for proper AP support
- Limited driver support for AP mode

**Approach**: Use Windows Mobile Hotspot API:
```go
func (wm *WiFiHotspotManager) enableWindowsHotspot() error {
    // Use PowerShell to enable mobile hotspot
    psCmd := `Set-NetConnectionProfile -InterfaceAlias "Wi-Fi" -NetworkCategory Private`
    _, err := runCmd("powershell", "-Command", psCmd)
    return err
}
```

## Troubleshooting Guide

### Common Issues and Solutions

**1. Hardware Not Supporting AP Mode**

**Symptoms**:
- `hostapd` fails to start with "driver doesn't support AP mode"
- Hardware detection returns `apsta-only` or `none`

**Solutions**:
```go
// Check driver capabilities
func (wm *WiFiHotspotManager) checkDriverCapabilities(iface string) error {
    out, err := runCmd("iw", "phy", "phy0", "info")
    if err != nil {
        return err
    }

    if !strings.Contains(out, "interface_combinations") {
        return fmt.Errorf("driver does not support required interface modes")
    }

    return nil
}
```

**2. Interface Already in Use**

**Symptoms**:
- Hotspot fails with "interface already in use"
- Network manager conflicts

**Solutions**:
```go
// Release interface from NetworkManager
func (wm *WiFiHotspotManager) releaseInterface(iface string) error {
    // Stop NetworkManager from managing the interface
    _, err := runCmd("nmcli", "dev", "disconnect", iface)
    if err != nil {
        // Try alternative methods
        _, err = runCmd("ip", "link", "set", iface, "down")
    }
    return err
}
```

**3. DHCP Issues**

**Symptoms**:
- Clients can't get IP addresses
- dnsmasq fails to start

**Solutions**:
```go
// Validate DHCP configuration
func (wm *WiFiHotspotManager) validateDHCPConfig(cfg *WiFiHotspotConfig) error {
    if !cfg.EnableDHCP {
        return nil
    }

    // Parse DHCP range
    parts := strings.Split(cfg.DHCPRange, ",")
    if len(parts) != 2 {
        return fmt.Errorf("invalid DHCP range format")
    }

    // Validate IP addresses
    for _, part := range parts {
        if !isValidIP(part) {
            return fmt.Errorf("invalid IP in DHCP range: %s", part)
        }
    }

    return nil
}
```

**4. NAT Not Working in AP+STA Mode**

**Symptoms**:
- Clients can connect to hotspot but no internet access
- iptables rules not applied correctly

**Solutions**:
```go
// Verify NAT rules
func (wm *WiFiHotspotManager) verifyNATRules() error {
    // Check MASQUERADE rule
    _, err := runCmd("iptables", "-t", "nat", "-L", "POSTROUTING", "-n")
    if err != nil {
        return err
    }

    // Check FORWARD chain
    _, err = runCmd("iptables", "-L", "FORWARD", "-n")
    if err != nil {
        return err
    }

    return nil
}
```

### Diagnostic Commands

**Hardware Diagnostics**:
```bash
# Check WiFi interface status
iw dev
iwconfig

# Check driver capabilities
iw phy phy0 info

# Check regulatory domain
iw reg get
```

**Process Diagnostics**:
```bash
# Check if hostapd is running
ps aux | grep hostapd

# Check hostapd logs
journalctl -u hostapd -f

# Check dnsmasq status
ps aux | grep dnsmasq
```

**Network Diagnostics**:
```bash
# Check interface IP
ip addr show wlan0

# Check routing table
ip route show

# Check iptables rules
iptables -t nat -L -v
iptables -L FORWARD -v
```

## Testing Strategy

### Unit Tests

**Hardware Detection Tests**:
```go
func TestDetectHardwareCapabilities(t *testing.T) {
    wm := NewWiFiHotspotManager("/tmp/test-yaver")

    // Mock the runCmd function for testing
    // Test with mock iw output
    // Verify driver detection
    // Verify capability detection
}
```

**Configuration Validation Tests**:
```go
func TestValidateConfig(t *testing.T) {
    tests := []struct {
        name    string
        config  WiFiHotspotConfig
        wantErr bool
    }{
        {"valid AP config", WiFiHotspotConfig{
            SSID: "TestHotspot", Password: "password123", Mode: "ap",
            Interface: "wlan0"}, false},
        {"invalid password", WiFiHotspotConfig{
            SSID: "TestHotspot", Password: "short", Mode: "ap",
            Interface: "wlan0"}, true},
        {"missing upstream for AP+STA", WiFiHotspotConfig{
            SSID: "TestHotspot", Password: "password123", Mode: "apsta",
            Interface: "wlan0"}, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateHotspotConfig(tt.config)
            if (err != nil) != tt.wantErr {
                t.Errorf("validateHotspotConfig() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Integration Tests

**Service Integration Tests**:
```go
func TestWiFiHotspotService(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Requires actual hardware
    // Test full hotspot lifecycle
    // Test MCP endpoints
    // Test service manager integration
}
```

### Hardware Testing

**Test Matrix**:

| Hardware | Driver | AP Mode | AP+STA Mode | Notes |
|----------|--------|---------|-------------|-------|
| Intel AX200 | iwlwifi | ✅ | ✅ | Full support |
| Realtek 8821CE | rtw88 | ✅ | ⚠️ | AP+STA may require patches |
| Broadcom 4365 | brcmfmac | ✅ | ❌ | AP only |
| Atheros AR9280 | ath9k | ✅ | ✅ | Good support |

**Platform Testing**:
- **Linux**: Ubuntu 22.04, Fedora 38, Debian 12
- **macOS**: macOS 13+ (Ventura), macOS 14+ (Sonoma)
- **Windows**: Windows 11, Windows Server 2022

## Security Considerations

### Authentication and Encryption

**WPA2-PSK Configuration**:
```go
// Ensure WPA2 with strong encryption
func generateHostapdSecurityConfig(cfg *WiFiHotspotConfig) string {
    config := `
wpa=2
wpa_passphrase=%s
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
group_cipher=CCMP
`
    return fmt.Sprintf(config, cfg.Password)
}
```

**Password Requirements**:
- Minimum 8 characters
- Maximum 63 characters
- Should contain mix of letters, numbers, symbols
- Avoid common passwords

### Network Isolation

**Client Isolation** (optional):
```go
// Enable client isolation for public hotspots
func enableClientIsolation(iface string) error {
    // Add hostapd config option
    config := `
ap_isolate=1
`
    // Update hostapd.conf
    return nil
}
```

### Firewall Rules

**Restrict Access**:
```go
// Set up firewall rules to restrict hotspot access
func setupFirewallRules(iface string) error {
    // Allow DNS and DHCP
    runCmd("iptables", "-A", "INPUT", "-i", iface, "-p", "udp", "--dport", "53", "-j", "ACCEPT")
    runCmd("iptables", "-A", "INPUT", "-i", iface, "-p", "udp", "--dport", "67", "-j", "ACCEPT")

    // Block other incoming connections
    runCmd("iptables", "-A", "INPUT", "-i", iface, "-j", "DROP")

    return nil
}
```

## Performance Optimization

### Connection Management

**Limit Connected Clients**:
```go
// Limit number of simultaneous clients
func setMaxClients(iface string, maxClients int) error {
    // Add to hostapd config
    config := fmt.Sprintf("max_num_sta=%d", maxClients)
    // Update hostapd.conf
    return nil
}
```

### Bandwidth Management

**QoS Configuration**:
```go
// Set up QoS rules for bandwidth limiting
func setupQoSRules(iface string, uploadLimit, downloadLimit string) error {
    // Use tc (traffic control) for QoS
    runCmd("tc", "qdisc", "add", "dev", iface, "root", "handle", "1:", "htb")
    // Add rate limiting rules
    return nil
}
```

### Resource Monitoring

**Monitor Resource Usage**:
```go
func (wm *WiFiHotspotManager) getResourceUsage() (*WiFiResourceUsage, error) {
    usage := &WiFiResourceUsage{}

    // Get hostapd CPU/memory usage
    if pid := wm.getPID("hostapd"); pid > 0 {
        usage.HostapdCPU = wm.getProcessCPU(pid)
        usage.HostapdMemory = wm.getProcessMemory(pid)
    }

    // Get dnsmasq CPU/memory usage
    if pid := wm.getPID("dnsmasq"); pid > 0 {
        usage.DnsmasqCPU = wm.getProcessCPU(pid)
        usage.DnsmasqMemory = wm.getProcessMemory(pid)
    }

    // Get network throughput
    usage.NetworkThroughput = wm.getNetworkThroughput(wm.status.Interface)

    return usage, nil
}
```

## Error Handling

### Comprehensive Error Types

```go
type WiFiError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Details string `json:"details,omitempty"`
    Retry   bool   `json:"retry"`
}

var (
    ErrHardwareNotSupported = &WiFiError{
        Code:    "HARDWARE_NOT_SUPPORTED",
        Message: "WiFi hardware does not support required mode",
        Retry:   false,
    }
    ErrInterfaceInUse = &WiFiError{
        Code:    "INTERFACE_IN_USE",
        Message: "WiFi interface is already in use",
        Retry:   true,
    }
    ErrConfigInvalid = &WiFiError{
        Code:    "CONFIG_INVALID",
        Message: "Configuration is invalid",
        Retry:   false,
    }
    ErrDHCPFailed = &WiFiError{
        Code:    "DHCP_FAILED",
        Message: "DHCP server failed to start",
        Retry:   true,
    }
)
```

### Error Recovery

**Automatic Recovery**:
```go
func (wm *WiFiHotspotManager) attemptRecovery(err error) error {
    // Check if error is recoverable
    if wifiErr, ok := err.(*WiFiError); ok && wifiErr.Retry {
        // Attempt recovery steps
        switch wifiErr.Code {
        case "INTERFACE_IN_USE":
            return wm.releaseInterfaceAndRetry(wm.status.Interface)
        case "DHCP_FAILED":
            return wm.restartDHCP()
        }
    }
    return err
}
```

## Monitoring and Logging

### Event Logging

```go
func (wm *WiFiHotspotManager) logEvent(event string, details map[string]interface{}) {
    logEntry := struct {
        Timestamp string                 `json:"timestamp"`
        Event     string                 `json:"event"`
        Details   map[string]interface{} `json:"details"`
    }{
        Timestamp: time.Now().Format(time.RFC3339),
        Event:     event,
        Details:   details,
    }

    // Write to log file
    wm.writeLogEntry(logEntry)
}
```

### Status Monitoring

**Real-time Status Updates**:
```go
func (wm *WiFiHotspotManager) startStatusMonitor() {
    ticker := time.NewTicker(5 * time.Second)
    go func() {
        for {
            select {
            case <-ticker.C:
                status, err := wm.GetStatus()
                if err == nil {
                    wm.status = status
                    wm.logEvent("status_update", map[string]interface{}{
                        "clients": status.ConnectedClients,
                        "uptime":  status.Uptime,
                    })
                }
            case <-wm.stopChan:
                ticker.Stop()
                return
            }
        }
    }()
}
```

## Configuration Examples

### AP Mode Configuration

```yaml
# .yaver/wifi-hotspot.yaml
ssid: YaverHotspot
password: SecurePassword123
mode: ap
interface: wlan0
channel: 6
frequency: 2.4GHz
ip_address: 192.168.4.1
enable_dhcp: true
dhcp_range: 192.168.4.100,192.168.4.200
```

### AP+STA Mode Configuration

```yaml
ssid: YaverHotspot
password: SecurePassword123
mode: apsta
interface: wlan0
upstream_ssid: HomeWiFi
upstream_password: HomePassword
channel: 6
frequency: 2.4GHz
bridge_name: br0
enable_dhcp: true
dhcp_range: 192.168.4.100,192.168.4.200
```

### Service Configuration

```yaml
# .yaver/services.yaml
services:
  wifi-hotspot:
    type: wifi-hotspot
    enabled: true
    config_file: .yaver/wifi-hotspot.yaml
    auto_start: false
```

## Migration Path

### From Existing Solutions

**NetworkManager Integration**:
```go
// Disable NetworkManager control of interface before starting hotspot
func (wm *WiFiHotspotManager) prepareForHotspot() error {
    // Stop NetworkManager from managing interface
    _, err := runCmd("nmcli", "device", "set", wm.status.Interface, "managed", "no")
    if err != nil {
        // Alternative: stop NetworkManager service temporarily
        runCmd("systemctl", "stop", "NetworkManager")
    }
    return nil
}
```

## Dependencies

### Required Packages

**Linux**:
```bash
# Ubuntu/Debian
apt-get install hostapd dnsmasq iw iproute2 iptables

# Fedora/RHEL
dnf install hostapd dnsmasq iw iproute iptables

# Arch Linux
pacman -S hostapd dnsmasq iw iproute2 iptables-nft
```

**macOS**:
- Built-in `airport` command
- `pf` firewall (pre-installed)

**Windows**:
- Built-in `netsh` command
- PowerShell (pre-installed)

### Go Dependencies

```go
import (
    "bytes"
    "fmt"
    "osexec "os/exec"
    "path/filepath"
    "runtime"
    "strconv"
    "strings"
    "sync"
    "time"
)
```

## API Reference

### WiFiHotspotManager Methods

**NewWiFiHotspotManager(workDir string) *WiFiHotspotManager**
- Creates a new WiFi hotspot manager
- `workDir`: Working directory for configuration files

**DetectHardwareCapabilities() (*WiFiHardwareCapabilities, error)**
- Detects WiFi hardware capabilities
- Returns supported modes, driver, bands, channels

**ValidateConfig(cfg *WiFiHotspotConfig) error**
- Validates hotspot configuration
- Checks mode compatibility, password requirements, interface validity

**StartHotspot(cfg *WiFiHotspotConfig) error**
- Starts WiFi hotspot with given configuration
- Generates config files, starts processes, sets up networking

**StopHotspot() error**
- Stops running WiFi hotspot
- Cleans up processes, rules, and files

**GetStatus() (*WiFiHotspotStatus, error)**
- Gets current hotspot status
- Returns running state, connected clients, uptime

**RunDiagnostics() (*WiFiDiagnostics, error)**
- Runs comprehensive diagnostics
- Returns hardware status, process status, network status

### MCP Tool Endpoints

**POST /mcp/wifi/start**
- Starts WiFi hotspot
- Body: `{"ssid": "string", "password": "string", "mode": "ap|apsta", "interface": "string"}`

**POST /mcp/wifi/stop**
- Stops WiFi hotspot

**GET /mcp/wifi/status**
- Gets current hotspot status

**POST /mcp/wifi/diagnose**
- Runs diagnostic checks

## Best Practices

1. **Always validate hardware capabilities before starting hotspot**
2. **Release interface from NetworkManager before starting**
3. **Clean up properly on shutdown**
4. **Monitor hotspot status continuously**
5. **Log all events for troubleshooting**
6. **Implement proper error recovery**
7. **Test on actual hardware, not just simulations**
8. **Handle platform differences gracefully**
9. **Provide clear error messages**
10. **Support both AP and AP+STA modes where hardware allows**

## Known Limitations

1. **macOS AP mode support** is limited - often requires system-level sharing
2. **Windows AP mode** depends on driver support
3. **Some Intel cards** have buggy AP+STA implementation
4. **Regulatory domain restrictions** may limit channel availability
5. **Multiple WiFi interfaces** can cause conflicts
6. **Virtual interfaces** may not work with all drivers

## Future Enhancements

1. **WPA3 support** when hardware/software ready
2. **Mesh networking** capabilities
3. **Band steering** for dual-band operation
4. **Client bandwidth limiting** per device
5. **Captive portal** for authentication
6. **Analytics** for usage tracking
7. **Auto channel selection** to avoid interference
8. **Signal strength monitoring** and optimization
9. **Multiple hotspot profiles** support
10. **Integration with Yaver's network diagnostics tools**

## Troubleshooting Commands

### Quick Diagnostics

```bash
# Check WiFi interface status
iw dev wlan0 info

# Check hostapd status
systemctl status hostapd

# Check dnsmasq status
systemctl status dnsmasq

# Check iptables rules
iptables -t nat -L -v
iptables -L FORWARD -v

# Check logs
journalctl -u hostapd -f
tail -f /var/log/syslog | grep hostapd
```

### Hardware Testing

```bash
# Test if interface supports AP mode
sudo iw dev wlan0 interface add wlan0_ap type __ap

# Test driver capabilities
sudo iw phy phy0 info

# Check regulatory domain
sudo iw reg get
```

### Network Testing

```bash
# Test DHCP server
sudo tcpdump -i wlan0 udp port 67

# Test client connectivity
ping 8.8.8.8

# Test DNS resolution
nslookup google.com
```

## Support Matrix

| Feature | Linux | macOS | Windows | Notes |
|---------|-------|-------|---------|-------|
| AP Mode | ✅ | ⚠️ | ⚠️ | macOS/Windows require specific conditions |
| AP+STA Mode | ✅ | ❌ | ❌ | Linux only, driver-dependent |
| DHCP | ✅ | ✅ | ✅ | dnsmasq/bootpd |
| DNS | ✅ | ✅ | ✅ | dnsmasq/system DNS |
| WPA2 | ✅ | ✅ | ✅ | Full support |
| Hardware Detection | ✅ | ✅ | ✅ | Cross-platform |
| Auto Channel | ✅ | ❌ | ❌ | Manual selection only |
| QoS | ✅ | ❌ | ❌ | Linux only |
| Monitoring | ✅ | ✅ | ✅ | Basic status |

## Conclusion

This WiFi hotspot implementation provides a comprehensive, cross-platform solution for Yaver that integrates with existing service management, MCP tools, and mobile applications. The architecture follows Yaver's established patterns and includes extensive troubleshooting capabilities.

The implementation supports both standalone AP mode and advanced AP+STA repeater mode, making it suitable for various use cases from mobile device connectivity to network bridging.

All core functionality is ready to be implemented by following this guide, with clear patterns for each component and extensive testing strategies to ensure reliability across different hardware platforms.
