package main

import "testing"

// TestCellAndArmVerbsRegistered guards two things at once:
//  1. the arm cell verbs are actually compiled+registered — ops_armcell.go was
//     previously named ops_arm.go, which Go treated as a GOARCH=arm build
//     constraint and silently excluded on arm64/amd64 (the real edge platforms).
//  2. the new harness-cell verbs register without an init() duplicate-name panic.
func TestCellAndArmVerbsRegistered(t *testing.T) {
	want := []string{
		"arm_status", "arm_movej", "arm_freedrive", "arm_program_run",
		"arm_wrench", "arm_force_move", // the new force/contact gap-closers
		"cell_station_add", "cell_station_teach", "cell_station_test",
		"cell_program_save", "cell_program_run", "cell_status",
		"cell_job_save", "cell_job_run", "cell_job_get", // data-driven wire-list jobs
	}
	have := map[string]bool{}
	for _, v := range listOpsVerbs() {
		have[v.Name] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("ops verb %q not registered", w)
		}
	}
}
