package arm

import "strings"

// Config is the vault/file-backed definition of ONE arm cell. It is fully
// parametric: a user (or an auto-read) supplies the driver + address + the joint
// table, and Yaver drives it — no robot-specific code required for the generic
// drivers. Stored next to the Cartesian robot config (vault project "robot",
// name "arm-config").
type Config struct {
	// Driver: "fairino" (XML-RPC cobot), "generic_tcp" (command-template over a
	// socket), "generic_serial" (command-template over a tty/USB), or "bridge"
	// (HTTP JSON). Unknown → generic_tcp.
	Driver string `json:"driver,omitempty"`
	// Addr: "ip" / "ip:port" for TCP cobots, a /dev path for serial, a base URL
	// for bridge. For Fairino, just the IP (default XML-RPC port is used).
	Addr string `json:"addr,omitempty"`
	Port int    `json:"port,omitempty"` // overrides the driver default (TCP)
	Baud int    `json:"baud,omitempty"` // serial baud (myCobot M5 115200 / Pi 1000000)

	// Info: the parametric arm definition. When ReadFromRobot is true the backend
	// fills/overrides this from the controller on Describe; otherwise this table
	// (defined in the UI) IS the truth. Either way DOF is len(Joints), not a const.
	Info          ArmInfo `json:"info"`
	ReadFromRobot bool    `json:"readFromRobot,omitempty"`

	// DefaultVelPct / DefaultAccPct: speed/accel (0..100) when the caller omits.
	DefaultVelPct int `json:"defaultVelPct,omitempty"`
	DefaultAccPct int `json:"defaultAccPct,omitempty"`

	// Camera + vision reuse the robot package selectors: "external" (the box's own
	// camera push buffer), an http(s):// snapshot URL, or "/dev/videoN". Empty →
	// share the Cartesian robot cell's camera if one is configured.
	Camera string `json:"camera,omitempty"`

	// CommandTemplates drives the generic_tcp / generic_serial backends: a map of
	// logical op → a template string with {placeholders}, so ANY robot with a
	// line protocol is wired by parameters, not code. Recognized ops:
	//   "enable" / "disable" / "stop" / "estop" / "state" / "pose" /
	//   "moveJoints" / "movePose".
	// Placeholders: {joints} (comma-joined values), {jN} (1-based), {x}{y}{z}
	// {roll}{pitch}{yaw}, {vel}{acc}. The reply to "state"/"pose" is parsed by
	// StateParse / PoseParse (regexes with named groups jN / x,y,z,roll,...).
	CommandTemplates map[string]string `json:"commandTemplates,omitempty"`
	StateParse       string            `json:"stateParse,omitempty"`
	PoseParse        string            `json:"poseParse,omitempty"`

	// Sim configures the headless simulator backend (Driver "sim"). The harness
	// is a PyBullet process Yaver spawns; SimModel is its load token (see
	// SimModels / SimSource: "builtin:arm6", "pybullet:<path>", "desc:<name>",
	// "urdf:<path-or-url>"). The same arm_* verbs + the rendered-frame camera path
	// drive it, so sim and hardware are interchangeable everywhere.
	Sim SimConfig `json:"sim,omitempty"`

	Label     string `json:"label,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// SimConfig is the simulator-backend section of Config.
type SimConfig struct {
	Engine string `json:"engine,omitempty"` // "pybullet" (default) | "mujoco" (future seam)
	Model  string `json:"model,omitempty"`  // load token, e.g. "builtin:arm6" / "desc:ur5e" / "urdf:/path|url"
	Port   int    `json:"port,omitempty"`   // harness HTTP port (default 18092); 0 → default
	Python string `json:"python,omitempty"` // python interpreter (default: python3 on PATH)
	GUI    bool   `json:"gui,omitempty"`    // attach a desktop GUI window (dev only; default headless)
}

// SimDefaultPort is the harness HTTP port when Sim.Port is unset.
const SimDefaultPort = 18092

func (c *Config) Normalize() {
	c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	if c.Driver == "" {
		c.Driver = "generic_tcp"
	}
	if c.DefaultVelPct <= 0 || c.DefaultVelPct > 100 {
		c.DefaultVelPct = 30
	}
	if c.DefaultAccPct <= 0 || c.DefaultAccPct > 100 {
		c.DefaultAccPct = 30
	}
	if c.Driver == "sim" {
		if c.Sim.Engine == "" {
			c.Sim.Engine = "pybullet"
		}
		if c.Sim.Model == "" {
			c.Sim.Model = "builtin:arm6" // always-works, zero-download default
		}
		if c.Sim.Port <= 0 {
			c.Sim.Port = SimDefaultPort
		}
	}
	c.Info.Normalize()
}

// Enabled reports whether an arm cell is configured at all.
func (c Config) Enabled() bool {
	if c.Driver == "sim" && strings.TrimSpace(c.Sim.Model) != "" {
		return true // a sim needs no addr/hardware — just a model to load
	}
	return strings.TrimSpace(c.Addr) != "" || len(c.Info.Joints) > 0
}

// FairinoDefaults returns the canonical 6-DOF FR-series joint table as a
// starting point when the user hasn't read it from the robot yet. Generous soft
// limits (deg); the real per-model limits should be read via ReadFromRobot or
// tightened in the UI. NOT load-bearing — purely a convenience default so the UI
// shows 6 joints immediately for a Fairino.
func FairinoDefaults() ArmInfo {
	js := make([]JointSpec, 6)
	for i := range js {
		js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: -175, Max: 175, Unit: "deg", MaxVel: 180}
	}
	return ArmInfo{Model: "FR-series", Vendor: "Fairino", Joints: js, HasCartesian: true, PoseFrame: "base", DOF: 6, Source: "config"}
}
