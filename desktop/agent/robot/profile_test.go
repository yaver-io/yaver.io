package robot

import "testing"

func TestProfileModules(t *testing.T) {
	cases := []struct {
		kind          string
		wantMotion    bool
		wantTool      bool
		wantRotate    bool
		wantHasCamera bool
	}{
		{KindCartesian, true, false, false, true},
		{KindCartesianScrew, true, true, true, true},
		{KindScrewOnly, false, true, true, true},
	}
	for _, tc := range cases {
		c := Config{Profile: tc.kind}
		if got := c.Has(ModuleMotion); got != tc.wantMotion {
			t.Errorf("%s motion=%v want %v", tc.kind, got, tc.wantMotion)
		}
		if got := c.Has(ModuleTool); got != tc.wantTool {
			t.Errorf("%s tool=%v want %v", tc.kind, got, tc.wantTool)
		}
		if got := c.Has(ModuleRotate); got != tc.wantRotate {
			t.Errorf("%s rotate=%v want %v", tc.kind, got, tc.wantRotate)
		}
		if got := c.Has(ModuleCamera); got != tc.wantHasCamera {
			t.Errorf("%s camera=%v want %v", tc.kind, got, tc.wantHasCamera)
		}
	}
}

func TestCustomProfileModules(t *testing.T) {
	c := Config{Profile: KindCustom, Modules: []string{ModuleTool, ModuleGPIO}}
	if !c.Has(ModuleTool) || !c.Has(ModuleGPIO) {
		t.Fatal("custom should expose its explicit modules")
	}
	if c.Has(ModuleMotion) {
		t.Fatal("custom must not expose modules it didn't list")
	}
}

func TestNormalizeDefaults(t *testing.T) {
	c := Config{Profile: "screwdriver-only"}
	c.Normalize()
	if c.Profile != KindScrewOnly {
		t.Errorf("alias not normalized: %q", c.Profile)
	}
	if c.ToolMode == "" || c.EPerTurn <= 0 {
		t.Errorf("normalize should fill toolMode/ePerTurn, got %q %v", c.ToolMode, c.EPerTurn)
	}
	bad := Config{Profile: "nonsense"}
	bad.Normalize()
	if bad.Profile != KindCartesianScrew {
		t.Errorf("unknown profile should fall back to full cell, got %q", bad.Profile)
	}
}

func TestDefaultConfigFromHardware(t *testing.T) {
	if got := DefaultConfig(true, true).Profile; got != KindCartesianScrew {
		t.Errorf("backend+tool should be full cell, got %q", got)
	}
	if got := DefaultConfig(true, false).Profile; got != KindCartesian {
		t.Errorf("backend only should be cartesian, got %q", got)
	}
	if got := DefaultConfig(false, false).Profile; got != KindScrewOnly {
		t.Errorf("no backend should be screwdriver-only, got %q", got)
	}
}
