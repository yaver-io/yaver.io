package arm

// models_sim.go — Yaver's built-in catalog of SIMULATED robot arms. Picking one
// of these (Driver "sim") spins up a headless physics sim (PyBullet today, a
// MuJoCo seam later) loaded with that robot, so a user can jog/teach/repeat and
// SEE the arm move — with no hardware at all. Every sim model is driven through
// the exact same arm_* verbs and the same camera path as a real robot, so the
// mobile/web UI, teach-and-repeat, and the host vision loop work unchanged.
//
// Model loading is layered by how much the box must fetch, so there is ALWAYS a
// zero-network fallback:
//   - "builtin:arm6"   — a procedural 6-DOF arm the harness builds in memory.
//                        No asset, no download: works on a bare headless box.
//   - "pybullet:<path>"— a URDF bundled inside the pybullet wheel (pybullet_data:
//                        kuka_iiwa, franka_panda). Installed with pybullet, no net.
//   - "desc:<name>"    — fetched via robot_descriptions.py (UR, KUKA iiwa14,
//                        Kinova) the first time, then cached. Needs net once.
//
// The Info joint tables here are NOMINAL (so the UI shows the right DOF instantly
// before the sim is up); once the sim loads the URDF, Describe reads the EXACT
// joints back via the URDF importer — data, not code, end to end.

// SimVendor is the catalog group these appear under in the model picker.
const SimVendor = "Simulator"

func simRevolute(limitsDeg [][2]float64) []JointSpec {
	return revoluteJoints(limitsDeg, 180)
}

// SimModels is the curated simulator catalog. SimSource tells the harness how to
// load each one; Info prefills DOF/limits for the UI.
func SimModels() []RobotModel {
	// nominal joint envelopes (deg). Exact limits come from the loaded URDF.
	arm6 := [][2]float64{{-170, 170}, {-120, 120}, {-160, 160}, {-170, 170}, {-120, 120}, {-175, 175}}
	ur := [][2]float64{{-360, 360}, {-360, 360}, {-360, 360}, {-360, 360}, {-360, 360}, {-360, 360}}
	panda := [][2]float64{{-166, 166}, {-101, 101}, {-166, 166}, {-176, -4}, {-166, 166}, {-1, 215}, {-166, 166}}   // 7-DOF
	iiwa := [][2]float64{{-170, 170}, {-120, 120}, {-170, 170}, {-120, 120}, {-170, 170}, {-120, 120}, {-175, 175}} // 7-DOF
	gen3 := [][2]float64{{-360, 360}, {-128, 128}, {-360, 360}, {-147, 147}, {-360, 360}, {-120, 120}, {-360, 360}} // 7-DOF

	mk := func(model, source string, dof int, payload, reach float64, joints []JointSpec, note string) RobotModel {
		return RobotModel{
			Vendor: SimVendor, Model: model, Driver: "sim", Transport: "sim",
			PayloadKg: payload, ReachMm: reach, SimSource: source,
			Info: ArmInfo{Model: model, Vendor: SimVendor, Joints: joints, HasCartesian: true,
				PoseFrame: "base", PayloadKg: payload, ReachMm: reach, DOF: dof, Source: "config"},
			Note: note,
		}
	}

	return []RobotModel{
		// Always-works, zero-download default.
		mk("Generic 6-DOF (built-in)", "builtin:arm6", 6, 5, 900, simRevolute(arm6),
			"Procedural 6-axis arm — runs headless with no asset download. The safe default to prove the sim end to end."),

		// Bundled inside the pybullet wheel (pybullet_data) — no network.
		mk("KUKA iiwa (pybullet)", "pybullet:kuka_iiwa/model.urdf", 7, 14, 800, simRevolute(iiwa),
			"7-DOF, bundled with PyBullet (pybullet_data). No download."),
		mk("Franka Panda (pybullet)", "pybullet:franka_panda/panda.urdf", 7, 3, 855, simRevolute(panda),
			"7-DOF Franka Emika Panda, bundled with PyBullet. No download."),

		// Fetched once via robot_descriptions.py, then cached.
		mk("Universal Robots UR5e", "desc:ur5e", 6, 5, 850, simRevolute(ur),
			"Fetched via robot_descriptions.py on first load (cached). Mirrors the ur5e hardware driver."),
		mk("Universal Robots UR10e", "desc:ur10e", 6, 12.5, 1300, simRevolute(ur),
			"Fetched via robot_descriptions.py on first load (cached)."),
		mk("KUKA iiwa14", "desc:iiwa14", 7, 14, 820, simRevolute(iiwa),
			"7-DOF iiwa14 via robot_descriptions.py (cached)."),
		mk("Kinova Gen3", "desc:gen3", 7, 4, 902, simRevolute(gen3),
			"7-DOF Kinova Gen3 via robot_descriptions.py (cached)."),
	}
}

// SimSources returns the load tokens of the curated catalog, for validation /
// docs / the harness self-test.
func SimSources() []string {
	ms := SimModels()
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.SimSource
	}
	return out
}
