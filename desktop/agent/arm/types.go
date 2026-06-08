// Package arm is the GENERIC, parametric articulated-robot layer: an arm is
// defined entirely by DATA (its joints, limits, units), never by hardcoded DOF.
// Yaver carries no per-robot knowledge — DOF / joint ranges come from the robot
// itself (a backend's Describe) or from a profile a user defines in the UI. The
// same Controller drives a 6-DOF Fairino cobot, a 4-DOF SCARA, a 7-DOF arm, or a
// homebrew "myrobot" — they differ only in their ArmInfo and backend.
//
// Conventions follow industry/URDF norms so the model maps onto real robots:
//   - joints carry a TYPE (revolute / prismatic / continuous) + limits
//     (lower/upper/velocity/effort), matching URDF <joint><limit>;
//   - motion is the standard cobot triplet: MoveJ (joint space), MoveL (linear
//     in Cartesian), and jog, with speed/accel as a percentage of the robot's
//     configured max;
//   - a Cartesian pose is x,y,z (mm) + roll,pitch,yaw (deg, the industrial Euler
//     convention) in a named FRAME (base / tool).
//
// It sits BESIDE the Cartesian robot package (which stays X/Y/Z + Marlin) and
// REUSES that package's camera + vision so move-and-verify and the host-vision
// `robot_camera` MCP tool work unchanged.
package arm

// JointType mirrors URDF joint kinds.
const (
	JointRevolute   = "revolute"   // rotates between lower/upper (deg)
	JointPrismatic  = "prismatic"  // slides between lower/upper (mm)
	JointContinuous = "continuous" // rotates without limit
)

// JointSpec is the parametric definition of ONE joint, read from the robot or
// defined in the UI. There is no DOF constant anywhere; an arm IS its joints.
// Field names follow URDF <joint>/<limit> so it round-trips with standard tools.
type JointSpec struct {
	Name string `json:"name"`           // "J1".. / "shoulder" / "base"
	Type string `json:"type,omitempty"` // revolute (default) | prismatic | continuous

	Min float64 `json:"min"`            // URDF limit/lower — refused, never clamped
	Max float64 `json:"max"`            // URDF limit/upper
	Home float64 `json:"home,omitempty"` // home / zero position

	Unit       string  `json:"unit,omitempty"`       // "deg" (revolute, default) | "mm" (prismatic) | "rad"
	MaxVel     float64 `json:"maxVel,omitempty"`     // URDF limit/velocity (unit/s)
	MaxEffort  float64 `json:"maxEffort,omitempty"`  // URDF limit/effort (N·m / N), informational
}

func (j JointSpec) jtype() string {
	if j.Type == "" {
		return JointRevolute
	}
	return j.Type
}

func (j JointSpec) unit() string {
	if j.Unit != "" {
		return j.Unit
	}
	if j.jtype() == JointPrismatic {
		return "mm"
	}
	return "deg"
}

// ArmInfo is the full parametric description of an arm. DOF == len(Joints).
// Source records WHERE it came from so the UI shows "read from robot" vs
// "defined here".
type ArmInfo struct {
	Model        string      `json:"model,omitempty"`
	Vendor       string      `json:"vendor,omitempty"`
	DOF          int         `json:"dof"`
	Joints       []JointSpec `json:"joints"`
	HasCartesian bool        `json:"hasCartesian"`     // backend reports / accepts a TCP pose
	PoseFrame    string      `json:"poseFrame,omitempty"` // "base" (default) | "tool"
	PayloadKg    float64     `json:"payloadKg,omitempty"`
	ReachMm      float64     `json:"reachMm,omitempty"`
	Source       string      `json:"source,omitempty"` // "robot" | "config"
}

// Normalize fills DOF from the joint list and defaults joint types/units/names.
func (a *ArmInfo) Normalize() {
	a.DOF = len(a.Joints)
	for i := range a.Joints {
		if a.Joints[i].Type == "" {
			a.Joints[i].Type = JointRevolute
		}
		if a.Joints[i].Unit == "" {
			a.Joints[i].Unit = a.Joints[i].unit()
		}
		if a.Joints[i].Name == "" {
			a.Joints[i].Name = jointName(i)
		}
	}
	if a.PoseFrame == "" {
		a.PoseFrame = "base"
	}
}

// JointState is a live joint reading.
type JointState struct {
	Name     string  `json:"name"`
	Position float64 `json:"position"`
	Unit     string  `json:"unit,omitempty"`
}

// Pose is a Cartesian TCP pose: position mm, orientation roll/pitch/yaw deg
// (industrial Euler ZYX). Only meaningful when ArmInfo.HasCartesian.
type Pose struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Roll  float64 `json:"roll"`
	Pitch float64 `json:"pitch"`
	Yaw   float64 `json:"yaw"`
}

// ArmStatus is the no-motion snapshot for arm_status.
type ArmStatus struct {
	OK        bool         `json:"ok"`
	Backend   string       `json:"backend"`
	Connected bool         `json:"connected"`
	Enabled   bool         `json:"enabled"`
	EStopped  bool         `json:"estopped"`
	Joints    []JointState `json:"joints,omitempty"`
	Pose      *Pose        `json:"pose,omitempty"`
	CameraOK  bool         `json:"cameraOk"`
	Error     string       `json:"error,omitempty"`
}

// MoveResult is the single result shape for arm motion (MoveJ / MoveL / jog /
// home), mirroring the robot move-and-verify response but in joint/pose space.
type MoveResult struct {
	OK     bool         `json:"ok"`
	Code   string       `json:"code,omitempty"`
	Error  string       `json:"error,omitempty"`
	Kind   string       `json:"kind,omitempty"` // "movej" | "movel" | "jog" | "home" | "enable" | "verify"
	Joints []JointState `json:"joints,omitempty"`
	Pose   *Pose        `json:"pose,omitempty"`
	Verify *Verdict     `json:"verify,omitempty"`
	Frames *Frames      `json:"frames,omitempty"`
	TookMs int64        `json:"tookMs"`
}

// Verdict / Frames are the camera-judgment shapes (shared vocabulary with the
// Cartesian robot cell so the UI renders one way for both).
type Verdict struct {
	Mode        string  `json:"mode"` // "agent" | "frames"
	Moved       bool    `json:"moved,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
	Obstruction bool    `json:"obstruction,omitempty"`
	Expectation string  `json:"expectation,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Observed    string  `json:"observed,omitempty"`
}

type Frames struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

func jointName(i int) string { return "J" + itoa(i+1) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
