package main

// mcp_network_extra.go — real implementations of the three network tools
// (public_ip, speed_test, wifi_info) that were shipped as no-op stubs in the
// 2026-04-28 lean-stack cut. Restored end-to-end on explicit go-ahead so the
// agent's network coverage is complete (used by the mobile Connection screen
// to report the RUNNER's internet/IP/WiFi alongside the phone's own).
//
// All three run on the host where the agent executes (local dev box / cloud
// runner / paired machine) — that's exactly the "runner side" the mobile
// Connection screen wants to show next to the phone's on-device readings.

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// traceField pulls a `key=value` line out of a Cloudflare /cdn-cgi/trace body.
func traceField(body, key string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimSpace(line[len(key)+1:])
		}
	}
	return ""
}

// wifiField returns the text after the colon on the first line that mentions
// key (case-insensitive). Handles both "SSID: x" and "SSID                : x".
func wifiField(out, key string) string {
	lk := strings.ToLower(key)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(strings.ToLower(line), lk) {
			if i := strings.LastIndex(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// tcpLatencyMs is a root-free RTT proxy: time to open a TCP connection.
func tcpLatencyMs(addr string) int64 {
	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 4*time.Second)
	if err != nil {
		return -1
	}
	conn.Close()
	return time.Since(t0).Milliseconds()
}

func mcpPublicIP() interface{} {
	client := &http.Client{Timeout: 8 * time.Second}
	// Cloudflare trace returns ip + country (loc) in one cheap call.
	if resp, err := client.Get("https://1.1.1.1/cdn-cgi/trace"); err == nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		body := string(b)
		ip := traceField(body, "ip")
		if ip != "" {
			return map[string]interface{}{
				"ip":      ip,
				"country": traceField(body, "loc"),
				"source":  "cloudflare",
			}
		}
	}
	// Fallback: ipify.
	resp, err := client.Get("https://api.ipify.org?format=text")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
	resp.Body.Close()
	return map[string]interface{}{"ip": strings.TrimSpace(string(b)), "source": "ipify"}
}

func mcpSpeedTest() interface{} {
	client := &http.Client{Timeout: 30 * time.Second}
	const dlBytes = 10000000 // 10 MB
	start := time.Now()
	resp, err := client.Get(fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", dlBytes))
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	secs := time.Since(start).Seconds()
	var dlMbps float64
	if secs > 0 && n > 0 {
		dlMbps = float64(n) * 8 / 1e6 / secs
	}
	lat := tcpLatencyMs("1.1.1.1:443")
	return map[string]interface{}{
		"download_mbps":    fmt.Sprintf("%.1f", dlMbps),
		"downloaded_bytes": n,
		"seconds":          fmt.Sprintf("%.2f", secs),
		"latency_ms":       lat,
		"source":           "cloudflare",
	}
}

func mcpWiFiInfo() interface{} {
	switch runtime.GOOS {
	case "darwin":
		// macOS 14+: wdutil info. (Some RSSI/BSSID fields need sudo; SSID is fine.)
		if out, err := runCmd("/usr/bin/wdutil", "info"); err == nil && strings.Contains(out, "SSID") {
			return map[string]interface{}{
				"platform": "darwin", "tool": "wdutil",
				"ssid": wifiField(out, "SSID"), "rssi": wifiField(out, "RSSI"),
				"channel": wifiField(out, "Channel"), "raw": clip(out, 2000),
			}
		}
		// Older path: the airport CLI.
		airport := "/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport"
		if out, err := runCmd(airport, "-I"); err == nil && strings.TrimSpace(out) != "" {
			return map[string]interface{}{
				"platform": "darwin", "tool": "airport",
				"ssid": wifiField(out, " SSID"), "rssi": wifiField(out, "agrCtlRSSI"),
				"channel": wifiField(out, "channel"), "raw": clip(out, 2000),
			}
		}
		return map[string]interface{}{"platform": "darwin", "error": "wifi info unavailable (wdutil/airport failed; runner may be on ethernet)"}
	case "linux":
		// nmcli is the most reliable when present.
		if out, err := runCmd("sh", "-c", "nmcli -t -f active,ssid,signal,freq dev wifi 2>/dev/null | grep '^yes'"); err == nil && strings.TrimSpace(out) != "" {
			f := strings.Split(strings.TrimSpace(out), ":")
			m := map[string]interface{}{"platform": "linux", "tool": "nmcli"}
			if len(f) >= 4 {
				m["ssid"] = f[1]
				m["signal"] = f[2]
				m["frequency"] = f[3]
			}
			return m
		}
		if ssid, err := runCmd("iwgetid", "-r"); err == nil && strings.TrimSpace(ssid) != "" {
			return map[string]interface{}{"platform": "linux", "tool": "iwgetid", "ssid": strings.TrimSpace(ssid)}
		}
		return map[string]interface{}{"platform": "linux", "error": "no WiFi (nmcli/iwgetid found nothing; runner may be on ethernet)"}
	default:
		return map[string]interface{}{"platform": runtime.GOOS, "error": "wifi_info not supported on " + runtime.GOOS}
	}
}
