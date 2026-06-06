package robot

import "strings"

// Modular profiles: a robot device exposes only the modules its hardware has,
// chosen by a profile the user picks in Yaver. "Control only the screwdriver" is
// just the screwdriver-only profile — same verbs, no XYZ required.
// docs/yaver-robot-teach-motor-multicam.md §2b.

// Module identifiers a device can advertise.
const (
	ModuleMotion = "motion" // XYZ Marlin (home/jog/move)
	ModuleTool   = "tool"   // end-effector on/off (screwdriver)
	ModuleRotate = "rotate" // screwdriver rotation (E-stepper / motor)
	ModuleGPIO   = "gpio"   // generic board pins (M42)
	ModuleCamera = "camera" // vision / snapshot
)

// Profile kind constants.
const (
	KindCartesian      = "cartesian"             // Ender-3 motion only
	KindCartesianScrew = "cartesian+screwdriver" // motion + screwdriver
	KindScrewOnly      = "screwdriver"           // screwdriver only, no XYZ
	KindCustom         = "custom"                // explicit module set
)

// Config is the robot cell's per-user configuration. It lives in the Yaver
// VAULT (encrypted, per-user, local-first) under project "robot", name "config"
// — never in Yaver's platform Convex. Talos backup is optional. So a user can
// run the whole cell standalone with just this.
type Config struct {
	Profile string   `json:"profile"`           // one of the Kind* constants
	Modules []string `json:"modules,omitempty"` // explicit set when Profile==custom

	// Hardware / calibration (private R&D — encrypted at rest in the vault).
	Serial   string    `json:"serial,omitempty"`   // e.g. /dev/ttyUSB0
	ToolMode string    `json:"toolMode,omitempty"` // "fan" | "screw"
	ToolPin  int       `json:"toolPin,omitempty"`  // M42 pin when toolMode=screw
	EPerTurn float64   `json:"ePerTurn,omitempty"` // E units per screwdriver revolution
	Camera   string    `json:"camera,omitempty"`   // /dev/video0 or rtsp/http URL
	Envelope *Envelope `json:"envelope,omitempty"` // soft limits (motion profiles)
	Strict   bool      `json:"strictEncoder,omitempty"`

	Label     string `json:"label,omitempty"` // human name for this cell
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// ProfileOption describes a selectable profile for the mobile UI.
type ProfileOption struct {
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	Modules []string `json:"modules"`
	Desc    string   `json:"desc"`
}

// Profiles is the menu shown in the app's robot settings.
func Profiles() []ProfileOption {
	return []ProfileOption{
		{KindCartesian, "Ender-3 Cartesian", modulesFor(KindCartesian),
			"XYZ motion + camera. Move/jog/home/verify."},
		{KindCartesianScrew, "Cartesian + screwdriver", modulesFor(KindCartesianScrew),
			"XYZ motion plus a screwdriver end-effector — full cell."},
		{KindScrewOnly, "Screwdriver only", modulesFor(KindScrewOnly),
			"Just the screwdriver: on/off, rotation, GPIO. No XYZ."},
	}
}

// modulesFor returns the default module set for a kind.
func modulesFor(kind string) []string {
	switch kind {
	case KindCartesian:
		return []string{ModuleMotion, ModuleCamera}
	case KindScrewOnly:
		return []string{ModuleTool, ModuleRotate, ModuleGPIO, ModuleCamera}
	case KindCartesianScrew, "":
		return []string{ModuleMotion, ModuleTool, ModuleRotate, ModuleGPIO, ModuleCamera}
	default:
		return []string{ModuleMotion, ModuleTool, ModuleRotate, ModuleGPIO, ModuleCamera}
	}
}

// ResolvedModules is the active module set for this config.
func (c Config) ResolvedModules() []string {
	if c.Profile == KindCustom && len(c.Modules) > 0 {
		return c.Modules
	}
	return modulesFor(c.Profile)
}

// Has reports whether a module is active under this config.
func (c Config) Has(module string) bool {
	for _, m := range c.ResolvedModules() {
		if m == module {
			return true
		}
	}
	return false
}

// DefaultConfig derives a config from hardware presence when the vault has none
// yet — so an existing rig keeps working with zero setup ("use it as is"). A
// serial/bridge present with a tool wired ⇒ full cell; otherwise cartesian.
func DefaultConfig(hasBackend, hasTool bool) Config {
	switch {
	case hasBackend && hasTool:
		return Config{Profile: KindCartesianScrew}
	case hasBackend:
		return Config{Profile: KindCartesian}
	default:
		return Config{Profile: KindScrewOnly}
	}
}

// Normalize fills blanks and validates the profile kind.
func (c *Config) Normalize() {
	c.Profile = strings.TrimSpace(c.Profile)
	switch c.Profile {
	case KindCartesian, KindCartesianScrew, KindScrewOnly, KindCustom:
	case "screwdriver-only", "screw":
		c.Profile = KindScrewOnly
	default:
		c.Profile = KindCartesianScrew
	}
	if c.ToolMode == "" {
		c.ToolMode = "fan"
	}
	if c.EPerTurn <= 0 {
		c.EPerTurn = 1
	}
}
