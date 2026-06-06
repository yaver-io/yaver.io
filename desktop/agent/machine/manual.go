package machine

// manual.go — bridge the discovery output (a learned Schematic from sniff +
// machine_understand) into a Driver's tag map (the Machine Operating Manual).
// This closes the loop: SNIFF → UNDERSTAND → CONNECT → WATCH/CONTROL, so a
// machine learned once can be driven without re-typing its register map.

import "fmt"

// TagsFromSchematic converts a learned register Schematic into driver Tags.
// A register the classifier marked as a setpoint becomes writable; everything
// keeps its inferred name/unit/scale. Unnamed registers get a stable synthetic
// name so they're still addressable.
func TagsFromSchematic(sch Schematic) []Tag {
	out := make([]Tag, 0, len(sch.Registers))
	for _, r := range sch.Registers {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("fc%d_%d", r.Func, r.Addr)
		}
		out = append(out, Tag{
			Name:     name,
			Addr:     r.Addr,
			Func:     int(r.Func),
			Unit:     int(r.Unit),
			Kind:     string(r.Kind),
			Unit2:    r.Unit2,
			Scale:    r.Scale,
			Writable: r.Kind == KindSetpoint,
		})
	}
	return out
}
