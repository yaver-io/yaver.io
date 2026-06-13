package printer

import "strings"

// Config is the vault/file-backed definition of ONE printer cell, mirroring
// arm.Config. A printer is wired by parameters — driver + address + the LAN
// access code + serial — and Yaver drives it; no per-printer code beyond the
// driver. Stored encrypted in the vault (project "printer", name "config").
//
// AccessCode is a secret (the 8-digit LAN code from the printer screen) and
// MUST live only in the vault on the box next to the printer — never in Convex,
// never in a tracked file. Serial drives the MQTT topics but is not secret.
type Config struct {
	// Driver: "bambu" (default). Future: "octoprint", "moonraker", "prusalink".
	Driver string `json:"driver,omitempty"`
	// Addr: printer IP (or "ip:port" — port then overrides MQTTPort).
	Addr string `json:"addr,omitempty"`
	// AccessCode: Bambu LAN access code (Settings → WLAN on the printer). Secret.
	AccessCode string `json:"accessCode,omitempty"`
	// Serial: printer serial / SSDP USN. Drives MQTT topics device/<serial>/*.
	Serial string `json:"serial,omitempty"`

	Model string `json:"model,omitempty"` // human model ("P1S")
	Name  string `json:"name,omitempty"`  // friendly name

	MQTTPort   int `json:"mqttPort,omitempty"`   // default 8883
	CameraPort int `json:"cameraPort,omitempty"` // default 6000
	FTPPort    int `json:"ftpPort,omitempty"`    // default 990 (implicit TLS)

	// CameraOverride: if set ("http(s)://snapshot" or "/dev/videoN"), use it
	// instead of the built-in Bambu chamber camera. Empty → chamber camera.
	CameraOverride string `json:"cameraOverride,omitempty"`

	Label     string `json:"label,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

func (c *Config) Normalize() {
	c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	if c.Driver == "" {
		c.Driver = "bambu"
	}
	c.Addr = strings.TrimSpace(c.Addr)
	c.AccessCode = strings.TrimSpace(c.AccessCode)
	c.Serial = strings.TrimSpace(c.Serial)
	if c.MQTTPort <= 0 {
		c.MQTTPort = 8883
	}
	if c.CameraPort <= 0 {
		c.CameraPort = 6000
	}
	if c.FTPPort <= 0 {
		c.FTPPort = 990
	}
	if c.Model == "" && c.ModelFromSerial() != "" {
		c.Model = c.ModelFromSerial()
	}
}

// ModelFromSerial infers the model family from a Bambu serial prefix when the
// model wasn't supplied. "01P" → P1 series (best-effort, P1S the common case).
func (c Config) ModelFromSerial() string {
	s := strings.ToUpper(c.Serial)
	switch {
	case strings.HasPrefix(s, "01P"):
		return "P1S"
	case strings.HasPrefix(s, "01S"):
		return "X1"
	case strings.HasPrefix(s, "039"), strings.HasPrefix(s, "030"):
		return "A1"
	default:
		return ""
	}
}

// Enabled reports whether a printer is configured enough to attempt a connect.
// AccessCode is required for any authenticated action; discovery needs nothing.
func (c Config) Enabled() bool {
	return c.Addr != "" && c.Serial != "" && c.AccessCode != ""
}

// Redacted returns a copy safe to surface in UI/logs (access code masked).
func (c Config) Redacted() Config {
	if c.AccessCode != "" {
		c.AccessCode = "••••••••"
	}
	return c
}
