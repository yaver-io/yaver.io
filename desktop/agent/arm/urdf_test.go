package arm

import "testing"

const ur5eURDF = `<?xml version="1.0"?>
<robot name="ur5e">
  <link name="base_link"/>
  <joint name="base_fixed" type="fixed">
    <parent link="world"/><child link="base_link"/>
  </joint>
  <joint name="shoulder_pan_joint" type="revolute">
    <limit lower="-6.2831853" upper="6.2831853" effort="150" velocity="3.14159"/>
  </joint>
  <joint name="shoulder_lift_joint" type="revolute">
    <limit lower="-3.1415927" upper="3.1415927" effort="150" velocity="3.14159"/>
  </joint>
  <joint name="elbow_joint" type="revolute">
    <limit lower="-3.1415927" upper="3.1415927" effort="150" velocity="3.14159"/>
  </joint>
  <joint name="wrist_1_joint" type="continuous">
    <limit effort="28" velocity="6.2831853"/>
  </joint>
  <joint name="rail_joint" type="prismatic">
    <limit lower="0" upper="0.8" effort="1000" velocity="0.5"/>
  </joint>
  <joint name="finger_mimic" type="revolute">
    <limit lower="-1" upper="1" effort="1" velocity="1"/>
    <mimic joint="shoulder_pan_joint"/>
  </joint>
</robot>`

func TestParseURDF(t *testing.T) {
	info, err := ParseURDF([]byte(ur5eURDF))
	if err != nil {
		t.Fatalf("ParseURDF: %v", err)
	}
	if info.Model != "ur5e" {
		t.Errorf("model = %q, want ur5e", info.Model)
	}
	// fixed joint skipped, mimic joint skipped → 5 actuated DOF
	if info.DOF != 5 || len(info.Joints) != 5 {
		t.Fatalf("DOF = %d (joints %d), want 5", info.DOF, len(info.Joints))
	}
	if !info.HasCartesian {
		t.Error("HasCartesian should be true for a kinematic chain")
	}

	pan := info.Joints[0]
	if pan.Name != "shoulder_pan_joint" || pan.Type != JointRevolute {
		t.Errorf("joint0 = %+v", pan)
	}
	// 6.2831853 rad → ~360 deg
	if d := pan.Max - 360; d > 0.5 || d < -0.5 {
		t.Errorf("pan.Max = %f deg, want ~360", pan.Max)
	}
	if pan.Unit != "deg" {
		t.Errorf("revolute unit = %q, want deg", pan.Unit)
	}

	wrist := info.Joints[3]
	if wrist.Type != JointContinuous {
		t.Errorf("wrist type = %q, want continuous", wrist.Type)
	}
	if wrist.Min != -360 || wrist.Max != 360 {
		t.Errorf("continuous envelope = [%f,%f], want [-360,360]", wrist.Min, wrist.Max)
	}

	rail := info.Joints[4]
	if rail.Type != JointPrismatic || rail.Unit != "mm" {
		t.Errorf("rail = %+v, want prismatic/mm", rail)
	}
	// 0.8 m → 800 mm
	if rail.Max != 800 {
		t.Errorf("rail.Max = %f mm, want 800", rail.Max)
	}
	if rail.Home != 0 { // 0 ∈ [0,800] → home 0
		t.Errorf("rail.Home = %f, want 0 (0 in range)", rail.Home)
	}
}

func TestParseURDFErrors(t *testing.T) {
	if _, err := ParseURDF([]byte("not xml <<<")); err == nil {
		t.Error("expected error on malformed XML")
	}
	noJoints := `<robot name="x"><link name="a"/><joint name="f" type="fixed"/></robot>`
	if _, err := ParseURDF([]byte(noJoints)); err == nil {
		t.Error("expected error when no actuated joints")
	}
}
