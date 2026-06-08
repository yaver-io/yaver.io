package arm

// urdf.go — import a URDF (Unified Robot Description Format) into Yaver's
// parametric ArmInfo. URDF is the lingua franca of robot descriptions: every
// simulator (PyBullet, MuJoCo via conversion, Gazebo, Isaac) and every vendor
// ships one, and curated catalogs (ROS-Industrial, mujoco_menagerie,
// robot_descriptions.py) are all URDF-first. So "support any robot model" reduces
// to "parse a URDF" — the joint chain IS the DOF, exactly the data-not-code
// contract the arm package is built on.
//
// We read only the kinematic facts we model: the actuated joints in <robot>
// order, their type, and their <limit> (lower/upper/velocity/effort). Fixed /
// floating / planar joints are not degrees of freedom, so they're skipped. URDF
// is SI (radians for revolute/continuous, metres for prismatic); ArmInfo is the
// human/industrial convention (degrees, millimetres), so we convert on import.

import (
	"encoding/xml"
	"fmt"
	"math"
	"strings"
)

// urdfRobot mirrors the subset of URDF we care about.
type urdfRobot struct {
	XMLName xml.Name    `xml:"robot"`
	Name    string      `xml:"name,attr"`
	Joints  []urdfJoint `xml:"joint"`
}

type urdfJoint struct {
	Name  string     `xml:"name,attr"`
	Type  string     `xml:"type,attr"`
	Limit *urdfLimit `xml:"limit"`
	Mimic *struct {
		Joint string `xml:"joint,attr"`
	} `xml:"mimic"`
}

type urdfLimit struct {
	Lower    float64 `xml:"lower,attr"`
	Upper    float64 `xml:"upper,attr"`
	Velocity float64 `xml:"velocity,attr"`
	Effort   float64 `xml:"effort,attr"`
}

const radToDeg = 180.0 / math.Pi

// ParseURDF turns URDF XML into an ArmInfo. The actuated joints (revolute /
// continuous / prismatic), in document order, become the DOF — a joint named
// mimic of another is dropped (it isn't independently commandable). Returns an
// error if the XML is malformed or has no actuated joints.
func ParseURDF(data []byte) (ArmInfo, error) {
	var r urdfRobot
	if err := xml.Unmarshal(data, &r); err != nil {
		return ArmInfo{}, fmt.Errorf("urdf: parse: %w", err)
	}
	info := ArmInfo{
		Model:        strings.TrimSpace(r.Name),
		HasCartesian: true, // a kinematic chain has a TCP; the sim solves IK
		PoseFrame:    "base",
		Source:       "config",
	}
	for _, j := range r.Joints {
		jt := strings.ToLower(strings.TrimSpace(j.Type))
		switch jt {
		case "revolute", "continuous", "prismatic":
		default:
			continue // fixed / floating / planar are not DOF
		}
		if j.Mimic != nil && strings.TrimSpace(j.Mimic.Joint) != "" {
			continue // a mimic joint follows another; not independently commanded
		}
		spec := JointSpec{Name: strings.TrimSpace(j.Name)}
		switch jt {
		case "prismatic":
			spec.Type = JointPrismatic
			spec.Unit = "mm"
			if j.Limit != nil {
				spec.Min = j.Limit.Lower * 1000 // m → mm
				spec.Max = j.Limit.Upper * 1000
				spec.MaxVel = j.Limit.Velocity * 1000
				spec.MaxEffort = j.Limit.Effort
			}
		case "continuous":
			spec.Type = JointContinuous
			spec.Unit = "deg"
			// continuous joints have no <limit lower/upper>; expose a full turn so
			// the UI/soft-limit check has a finite, sane envelope.
			spec.Min, spec.Max = -360, 360
			if j.Limit != nil {
				spec.MaxVel = j.Limit.Velocity * radToDeg
				spec.MaxEffort = j.Limit.Effort
			}
		default: // revolute
			spec.Type = JointRevolute
			spec.Unit = "deg"
			if j.Limit != nil {
				spec.Min = j.Limit.Lower * radToDeg // rad → deg
				spec.Max = j.Limit.Upper * radToDeg
				spec.MaxVel = j.Limit.Velocity * radToDeg
				spec.MaxEffort = j.Limit.Effort
			}
		}
		// home defaults to 0 when in range, else the midpoint, so a freshly
		// imported robot has a valid, reachable home.
		if spec.Min <= 0 && 0 <= spec.Max {
			spec.Home = 0
		} else {
			spec.Home = (spec.Min + spec.Max) / 2
		}
		info.Joints = append(info.Joints, spec)
	}
	if len(info.Joints) == 0 {
		return ArmInfo{}, fmt.Errorf("urdf: no actuated joints (revolute/continuous/prismatic) found in %q", info.Model)
	}
	info.Normalize()
	return info, nil
}
