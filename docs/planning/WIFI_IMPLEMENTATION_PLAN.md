# WiFi Hotspot Cross-Platform Implementation Plan

## Status: Linux Fully Implemented, macOS Partially Supported

The codebase has excellent Linux support with hostapd/dnsmasq, but needs macOS enhancement and first-class wpa_supplicant support.

## Current Implementation Status

### ✅ Fully Working (Linux)
- WiFi Hotspot with hostapd + dnsmasq
- Mesh with wpa_supplicant (802.11s and BATMAN-adv)
- Full capabilities detection via iw/nl80211
- Mobile UI with full configuration

### ⚠️ Partially Working (macOS)
- WiFi hardware detection via networksetup
- Personal Hotspot enable/disable (basic)
- ❌ No wpa_supplicant support (mesh only works with 802.11s + wpa_supplicant)
- ❌ Limited control over SSID/password (system-managed)

### ❌ Not Implemented
- Windows support (needs netsh + hostednetwork API)
- First-class wpa_supplicant validation
- Per-instance wpa_supplicant configs for multiple interfaces
- Mobile UI showing platform-specific options

## Required Changes

### 1. macOS Hotspot Enhancement
**File:** `desktop/agent/wifi_hotspot.go`

Add to StartHotspot after macOS branch:
- Get current SSID/Password from system
- Check Personal Hotspot state via `/usr/libexec/PlistBuddy -x "com.apple.osx-ncsis.p2p" -c "Proxies"`
- Implement stopMacPersonalHotspot() for StopHotspot macOS branch

### 2. First-Class wpa_supplicant Support
**File:** `desktop/agent/wifi_mesh.go`

Add methods:
- `ValidateWpaEnvironment()` - Check wpa_supplicant, driver support, firmware
- `GenerateWpaSupplicantConfig()` ✅ already exists but needs validation
- `startWpaSupplicant()` ✅ exists but needs better error handling
- `StopWpaSupplicant()` ✅ exists but needs cleanup

Add to capabilities:
- `HasWpaSupplicant` - tool exists flag
- `SupportedWpaProtocols` - WPA2-PSK, WPA3-SAE, OWE
- `DriverWpaSupport` - driver supports wpa_supplicant

### 3. Mobile UI Platform Awareness
**File:** `mobile/app/(tabs)/console.tsx`

Add to WiFiTab and WiFiMeshPanel:
- Platform badge: `["Linux", "macOS", "Windows"]`
- Platform-specific options:
  - macOS: Hide channel, hardware mode, show "Personal Hotspot"
  - Linux: Show full config, channel, hardware mode
  - Windows: Show SSID/password, basic enable/disable
- wpa_supplicant config options for mesh:
  - WPA3-SAE toggle
  - Group rekeying interval
  - PMF (Protected Management Frames) toggle
- Device-specific SSID/password management

### 4. Remote Dev Configuration
**File:** `mobile/app/(tabs)/console.tsx`

Add to WiFi config:
- Remote device selector (similar to ConsoleTab remote machines)
- Sync configuration across devices
- Status shows which device controls the hotspot

## Implementation Order

1. Add macOS Personal Hotspot enhancement
2. Add wpa_supplicant validation to mesh manager
3. Update mobile UI to show platform-specific options
4. Add remote dev configuration to mobile UI
5. Test on macOS (this machine) with iPhone connection
6. Test on Linux with multiple devices

## wpa_supplicant First-Class Support

### Validation Functions Needed

```go
func (wm *WiFiMeshManager) ValidateWpaEnvironment() error {
    // Check wpa_supplicant binary exists
    if !isTool("wpa_supplicant") {
        return fmt.Errorf("wpa_supplicant not found in PATH")
    }

    // Check driver supports wpa_supplicant
    // Most modern Linux drivers do, but some need specific firmware

    // Check for control interface directory
    if err := os.MkdirAll(wm.wpaCtrlPath, 0o700); err != nil {
        return fmt.Errorf("create wpa_supplicant control dir: %w", err)
    }

    return nil
}
```

### Enhanced Config Generation

```go
func (wm *WiFiMeshManager) GenerateWpaSupplicantConfig(cfg *WiFiMeshConfig) error {
    // SAE (WPA3) support detection
    supportsSAE := true // Assume modern hardware, or detect from iw phy

    lines := []string{
        "ctrl_interface=" + wm.wpaCtrlPath,
        "ap_scan=2",
    }

    if cfg.CountryCode != "" {
        lines = append(lines, "country="+strings.ToUpper(cfg.CountryCode))
    }

    // Enhanced network block with multiple options
    network := []string{
        "network={",
        "\tssid=\"" + escapeWPAString(cfg.MeshID) + "\"",
        "\tmode=5", // Mesh point
        "\tfrequency=" + strconv.Itoa(cfg.FrequencyMHz),
        "\tkey_mgmt=WPA-PSK-SHA256",
    }

    // WPA3-SAE support
    if supportsSAE && cfg.EnableWPA3SAE {
        network = append(network, "\tsae=HmacSHA256")
    }

    // Password
    if cfg.Passphrase != "" {
        if cfg.EnableWPA3SAE {
            // WPA3 (SAE + PSK fallback)
            network = append(network,
                "\tpsk=\""+escapeWPAString(cfg.Passphrase)+"\"",
                "\tgroup_mgmt=SAE",
            )
        } else {
            // WPA2-PSK only
            network = append(network,
                "\tpsk=\""+escapeWPAString(cfg.Passphrase)+"\"",
                "\tgroup_mgmt=WPA-PSK",
            )
        }
    } else {
        // Open network (not recommended)
        network = append(network, "\tkey_mgmt=NONE")
    }

    // PMF (Protected Management Frames)
    if cfg.EnablePMF {
        network = append(network, "\tpmf=1")
    }

    network = append(network, "}")
    lines = append(lines, network...)

    return os.WriteFile(wm.wpaConfigPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
```

## Mobile UI Enhancements

### WiFi Tab Platform Detection

```tsx
const [platform, setPlatform] = useState<"linux" | "macos" | "windows" | "unknown">("unknown");

useEffect(() => {
  async function detectPlatform() {
    try {
      const info = await call("/console/system");
      setPlatform(info.os || "linux");
    } catch {}
  }
  detectPlatform();
}, []);

// Platform-specific UI
{platform === "macos" && (
  <View style={[card(c)]}>
    <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>
      macOS Personal Hotspot
    </Text>
    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
      SSID and password are managed by macOS System Settings. Use "Start" to enable.
    </Text>
  </View>
)}

{platform === "linux" && (
  // Full Linux configuration interface
  <View style={[card(c)]}>
    <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>
      Linux Hostapd Configuration
    </Text>
    {/* Existing full config */}
  </View>
)}
```

### Mesh wpa_supplicant Options

```tsx
// In WiFiMeshPanel
const [enableWPA3SAE, setEnableWPA3SAE] = useState(true);
const [enablePMF, setEnablePMF] = useState(false);

{/* WPA3-SAE toggle */}
<Pressable onPress={() => setEnableWPA3SAE(!enableWPA3SAE)} style={[actionBtn(c), { backgroundColor: enableWPA3SAE ? "#10b98120" : c.bgCard, borderColor: enableWPA3SAE ? "#10b981" : c.border, borderWidth: 1, flex: 1 }]}>
  <Text style={{ color: enableWPA3SAE ? "#10b981" : c.textMuted, fontSize: 12 }}>WPA3-SAE</Text>
</Pressable>

{/* PMF toggle */}
<Pressable onPress={() => setEnablePMF(!enablePMF)} style={[actionBtn(c), { backgroundColor: enablePMF ? "#10b98120" : c.bgCard, borderColor: enablePMF ? "#10b981" : c.border, borderWidth: 1, flex: 1 }]}>
  <Text style={{ color: enablePMF ? "#10b981" : c.textMuted, fontSize: 12 }}>PMF</Text>
</Pressable>
```

## Configuration Update for SSID "kivanc"

To set your SSID to "kivanc" with password "12345678":

### Via Mobile UI
1. Open Console → WiFi tab
2. Set SSID: `kivanc`
3. Set Password: `12345678`
4. Click "Start"

### Via API (for testing)
```bash
curl -X POST http://localhost:41011/console/wifi/start \
  -H "Content-Type: application/json" \
  -d '{
    "ssid": "kivanc",
    "password": "12345678",
    "mode": "ap",
    "channel": 6,
    "enableDhcp": true,
    "enableNat": false,
    "ipAddress": "192.168.47.1/24",
    "dhcpRange": "192.168.47.100,192.168.47.200"
  }'
```

## Testing Checklist

- [ ] macOS: Enable Personal Hotspot, verify iPhone can connect
- [ ] macOS: Stop Personal Hotspot, verify cleanup
- [ ] macOS: Update status on 5s interval refresh
- [ ] Linux: Start hotspot with "kivanc"/"12345678", verify connection
- [ ] Linux: Stop hotspot, verify cleanup
- [ ] Linux: Start 802.11s mesh with wpa_supplicant, verify peer discovery
- [ ] Linux: Start BATMAN mesh, verify bat0 interface and peers
- [ ] Mobile: macOS shows "Personal Hotspot" badge
- [ ] Mobile: Linux shows full configuration
- [ ] Mobile: mesh shows WPA3-SAE and PMF options
- [ ] Mobile: configuration syncs with remote device
- [ ] wpa_supplicant: Validate tool exists before starting
- [ ] wpa_supplicant: Generate config with SAE support
- [ ] wpa_supplicant: Stop properly and clean up PID/sock

## References

- macOS networksetup man pages: `man networksetup`
- macOS Personal Hotspot: System Preferences → Sharing → Internet Sharing
- Linux iw documentation: `man iw`
- wpa_supplicant docs: https://w1.fi/wpa_supplicant/
- BATMAN-adv docs: https://docs.kernel.org/networking/batman-adv.html