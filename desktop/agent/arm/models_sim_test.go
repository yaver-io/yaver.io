package arm

import (
	"strings"
	"testing"
)

func TestSimModelsCatalog(t *testing.T) {
	ms := SimModels()
	if len(ms) < 5 {
		t.Fatalf("expected a curated sim catalog, got %d", len(ms))
	}
	sawBuiltin := false
	for _, m := range ms {
		if m.Driver != "sim" {
			t.Errorf("%s: driver = %q, want sim", m.Model, m.Driver)
		}
		if m.SimSource == "" {
			t.Errorf("%s: missing SimSource", m.Model)
		}
		if m.Vendor != SimVendor {
			t.Errorf("%s: vendor = %q, want %q", m.Model, m.Vendor, SimVendor)
		}
		// DOF must equal the prefilled joint count.
		if m.Info.DOF != len(m.Info.Joints) {
			t.Errorf("%s: DOF %d != joints %d", m.Model, m.Info.DOF, len(m.Info.Joints))
		}
		if m.SimSource == "builtin:arm6" {
			sawBuiltin = true
		}
		// every source uses a known scheme
		ok := false
		for _, p := range []string{"builtin:", "pybullet:", "desc:", "urdf:"} {
			if strings.HasPrefix(m.SimSource, p) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: unknown SimSource scheme %q", m.Model, m.SimSource)
		}
	}
	if !sawBuiltin {
		t.Error("catalog must include the zero-download builtin:arm6 default")
	}
}

func TestSimSources(t *testing.T) {
	if len(SimSources()) != len(SimModels()) {
		t.Error("SimSources length mismatch")
	}
}
