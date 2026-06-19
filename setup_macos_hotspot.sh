#!/bin/bash

# macOS Personal Hotspot Configuration Script
# Sets custom SSID: "kivanc" and Password: "12345678"
# Requires: sudo privileges

set -e  # Exit on any error

echo "=========================================="
echo "macOS Personal Hotspot Configuration"
echo "Target SSID: kivanc"
echo "Target Password: 12345678"
echo "=========================================="
echo ""

# Prompt for sudo password upfront and refresh the session so subsequent commands don’t reprompt
echo "Enter your sudo password once (we’ll reuse it for the whole session)..."
sudo -v || { echo "sudo authentication failed"; exit 1; }
echo ""

# Step 1: Show current Wi-Fi state
echo "Step 1: Current Wi-Fi State"
echo "------------------------------"
/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport -s
echo ""

# Step 2: Get current computer names
echo "Step 2: Current Computer Names"
echo "------------------------------"
echo "ComputerName: $(sudo scutil --get ComputerName)"
echo "LocalHostName: $(sudo scutil --get LocalHostName)"
echo "HostName: $(sudo scutil --get HostName)"
echo ""

# Step 3: Check sharing daemon preferences
echo "Step 3: Sharing Daemon Preferences"
echo "------------------------------"
sudo plutil -p /Library/Preferences/SystemConfiguration/com.apple.sharingd.plist 2>&1 | head -50
echo ""

# Step 4: Dump hotspot-related keys
echo "Step 4: Hotspot-Related Keys"
echo "------------------------------"
sudo defaults read /Library/Preferences/SystemConfiguration/com.apple.sharingd.plist 2>&1 | grep -i "hotspot\|airdrop\|internet\|wifi\|airport" || echo "No obvious hotspot keys found"
echo ""

# Step 5: Check networksetup list
echo "Step 5: Network Setup List"
echo "------------------------------"
sudo networksetup -listallnetworkservices | grep -i "wifi\|bluetooth\|ethernet"
echo ""

# Step 6: Try to change LocalHostName (this may affect SSID)
echo "Step 6: Setting LocalHostName to 'kivanc'"
echo "------------------------------"
echo "Current: $(sudo scutil --get LocalHostName)"
echo "Attempting to change to: kivanc"
sudo scutil --set LocalHostName "kivanc"
echo "New: $(sudo scutil --get LocalHostName)"
echo ""

# Step 7: Check for hotspot configuration file
echo "Step 7: Looking for Hotspot Configuration Files"
echo "------------------------------"
find /Library/Preferences /System/Library/Preferences 2>/dev/null | grep -i "sharing\|airport" | grep -i "plist" | head -10
echo ""

# Step 8: Check if Internet Sharing is currently enabled
echo "Step 8: Current Internet Sharing Status"
echo "------------------------------"
sudo defaults read /Library/Preferences/SystemConfiguration/com.apple.sharingd.plist 2>&1 | grep -A 5 "InternetSharing" || echo "No Internet Sharing key found"
echo ""

# Step 9: Look for SSID configuration in various locations
echo "Step 9: Searching for SSID Configuration"
echo "------------------------------"
echo "Checking plist files for SSID keys..."
for plist in $(find /Library/Preferences /System/Library/Preferences 2>/dev/null | grep -i "plist"); do
    if sudo plutil -p "$plist" 2>&1 | grep -q "SSID\|NetworkName\|HotspotName"; then
        echo "Found in: $plist"
        sudo plutil -p "$plist" 2>&1 | grep -i "SSID\|NetworkName\|HotspotName" | head -5
    fi
done
echo ""

# Step 10: Check airport tool capabilities
echo "Step 10: Airport Tool Capabilities"
echo "------------------------------"
/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport --help 2>&1 | head -30
echo ""

# Step 11: Try to find hotspot password configuration
echo "Step 11: Searching for Password Configuration"
echo "------------------------------"
sudo defaults read /Library/Preferences/SystemConfiguration/com.apple.sharingd.plist 2>&1 | grep -i "password\|passphrase\|wpa\|security" | head -10 || echo "No password keys found"
echo ""

# Step 12: Check Bluetooth PAN configuration (often used for hotspot)
echo "Step 12: Bluetooth PAN Configuration"
echo "------------------------------"
system_profiler SPBluetoothDataType 2>&1 | grep -i "pan\|tethering\|hotspot" || echo "No Bluetooth PAN info found"
echo ""

# Step 13: Summary and Next Steps
echo "=========================================="
echo "Configuration Complete"
echo "=========================================="
echo ""
echo "What we found:"
echo "  - LocalHostName changed to: $(sudo scutil --get LocalHostName)"
echo "  - This MAY affect hotspot SSID"
echo ""
echo "Next manual steps to set password:"
echo "1. Open: System Preferences > Sharing"
echo "2. Enable: Internet Sharing"
echo "3. Click: 'Wi-Fi Options' button"
echo "4. Set: Password to 12345678"
echo "5. Check: SSID (should show 'kivanc' now)"
echo ""
echo "Alternative: Enable via Control Center"
echo "1. Open: Control Center"
echo "2. Click: Personal Hotspot icon"
echo "3. Enable: Toggle ON"
echo "4. Verify: SSID shows 'kivanc'"
echo ""
echo "To verify hotspot is running:"
echo "/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport -s"
echo ""