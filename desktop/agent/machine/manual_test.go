package machine

import "testing"

func TestTagsFromSchematic(t *testing.T) {
	sch := Schematic{
		Driver: "modbus_rtu",
		Registers: []RegisterObs{
			{Unit: 1, Func: 3, Addr: 12, Name: "cut_length", Unit2: "mm", Scale: 0.25, Kind: KindSetpoint},
			{Unit: 1, Func: 3, Addr: 100, Kind: KindCounter}, // unnamed → synthetic
		},
	}
	tags := TagsFromSchematic(sch)
	if len(tags) != 2 {
		t.Fatalf("want 2 tags, got %d", len(tags))
	}
	if tags[0].Name != "cut_length" || !tags[0].Writable {
		t.Errorf("setpoint should be writable named tag, got %+v", tags[0])
	}
	if tags[0].Scale != 0.25 {
		t.Errorf("scale lost: %v", tags[0].Scale)
	}
	if tags[1].Name != "fc3_100" {
		t.Errorf("unnamed register should get synthetic name, got %q", tags[1].Name)
	}
	if tags[1].Writable {
		t.Error("counter must not be writable")
	}
}
