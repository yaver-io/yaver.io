package arm

// models.go — Yaver's built-in knowledge of common robot-arm MODELS. Picking a
// model in the UI prefills the parametric config (DOF, per-joint limits, the
// recommended driver/transport, payload, reach) so a user recognizes their
// robot and is ready in one tap — yet everything stays editable (the arm is
// still just its ArmInfo). Specs are NOMINAL vendor figures; per-joint limits
// should be tightened or read from the robot (ReadFromRobot) for production.
//
// Vendors: Fairino (FR-series cobots), Elephant Robotics (myCobot family),
// Source Robotics (PAROL6).

// RobotModel is a catalog entry the UI lists under its vendor.
type RobotModel struct {
	Vendor    string  `json:"vendor"`
	Model     string  `json:"model"`
	Driver    string  `json:"driver"`           // recommended arm driver
	Transport string  `json:"transport"`         // tcp | serial | bridge
	PayloadKg float64 `json:"payloadKg,omitempty"`
	ReachMm   float64 `json:"reachMm,omitempty"`
	Info      ArmInfo `json:"info"`              // DOF + joint table to prefill
	Note      string  `json:"note,omitempty"`
}

// revolute builds an N-joint revolute table from symmetric ±limit degrees (or a
// per-joint list when provided).
func revoluteJoints(limitsDeg [][2]float64, maxVel float64) []JointSpec {
	js := make([]JointSpec, len(limitsDeg))
	for i, l := range limitsDeg {
		js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: l[0], Max: l[1], Unit: "deg", MaxVel: maxVel}
	}
	return js
}

func fairinoModel(model string, payload, reach float64) RobotModel {
	// FR-series are 6-DOF; joint ranges are similar across the line. ±175 is a
	// safe nominal envelope — tighten per the model's manual or read from robot.
	lim := [][2]float64{{-175, 175}, {-175, 175}, {-160, 160}, {-175, 175}, {-175, 175}, {-175, 175}}
	return RobotModel{
		Vendor: "Fairino", Model: model, Driver: "fairino", Transport: "tcp",
		PayloadKg: payload, ReachMm: reach,
		Info: ArmInfo{Model: model, Vendor: "Fairino", Joints: revoluteJoints(lim, 180),
			HasCartesian: true, PoseFrame: "base", PayloadKg: payload, ReachMm: reach, DOF: 6, Source: "config"},
		Note: "XML-RPC on the controller (:20003). Default IP 192.168.58.2.",
	}
}

func mycobotModel(model string, dof int, payload, reach float64, lim [][2]float64) RobotModel {
	return RobotModel{
		Vendor: "Elephant Robotics", Model: model, Driver: "mycobot", Transport: "serial|tcp",
		PayloadKg: payload, ReachMm: reach,
		Info: ArmInfo{Model: model, Vendor: "Elephant Robotics", Joints: revoluteJoints(lim, 120),
			HasCartesian: true, PoseFrame: "base", PayloadKg: payload, ReachMm: reach, DOF: dof, Source: "config"},
		Note: "pymycobot protocol over USB serial (M5 115200 / Pi 1000000 baud) or TCP.",
	}
}

// RobotModels is the curated catalog. Specs are nominal; DOF + driver are exact.
func RobotModels() []RobotModel {
	mc280 := [][2]float64{{-168, 168}, {-135, 135}, {-150, 150}, {-145, 145}, {-165, 165}, {-175, 175}}
	mc320 := [][2]float64{{-170, 170}, {-120, 120}, {-148, 148}, {-173, 173}, {-170, 170}, {-180, 180}}
	mechArm := [][2]float64{{-160, 160}, {-130, 130}, {-160, 160}, {-160, 160}, {-160, 160}, {-179, 179}}
	pal260 := [][2]float64{{-162, 162}, {-2, 90}, {-92, 60}, {-180, 180}} // 4-DOF palletizer
	myArm := [][2]float64{{-165, 165}, {-90, 90}, {-180, 180}, {-165, 165}, {-115, 115}, {-175, 175}, {-180, 180}} // 7-DOF

	// PAROL6 6-DOF (nominal — verify against your build's calibration).
	parol := [][2]float64{{-123, 123}, {-145, 0}, {-148, 148}, {-120, 120}, {-105, 105}, {-180, 180}}

	return []RobotModel{
		// Fairino FR-series (payload kg ≈ model number; reach nominal mm)
		fairinoModel("FR3", 3, 590),
		fairinoModel("FR5", 5, 922),
		fairinoModel("FR10", 10, 1408),
		fairinoModel("FR16", 16, 930),
		fairinoModel("FR20", 20, 1100),
		fairinoModel("FR30", 30, 1100),

		// Elephant Robotics myCobot family (varied DOF)
		mycobotModel("myCobot 280", 6, 0.25, 280, mc280),
		mycobotModel("myCobot 320", 6, 1.0, 350, mc320),
		mycobotModel("mechArm 270", 6, 0.25, 270, mechArm),
		mycobotModel("myPalletizer 260", 4, 0.25, 260, pal260),
		mycobotModel("myArm 300", 7, 0.25, 300, myArm),

		// Source Robotics PAROL6 (via headless_commander bridge)
		{
			Vendor: "Source Robotics", Model: "PAROL6", Driver: "parol6", Transport: "bridge",
			PayloadKg: 1.0, ReachMm: 440,
			Info: ArmInfo{Model: "PAROL6", Vendor: "Source Robotics", Joints: revoluteJoints(parol, 120),
				HasCartesian: true, PoseFrame: "base", PayloadKg: 1.0, ReachMm: 440, DOF: 6, Source: "config"},
			Note: "Run scripts/parol6_bridge.py (wraps the official headless_commander).",
		},
	}
}

// RobotModelsByVendor groups the catalog for a vendor→models UI picker.
func RobotModelsByVendor() map[string][]RobotModel {
	out := map[string][]RobotModel{}
	for _, m := range RobotModels() {
		out[m.Vendor] = append(out[m.Vendor], m)
	}
	return out
}
