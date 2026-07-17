package main

// Shared wire status vocabulary. Different subsystems may only use a subset,
// but they should not invent parallel spellings for the same state.
const (
	runStatusRunning    = "running"
	runStatusCompleted  = "completed"
	runStatusFailed     = "failed"
	runStatusStopped    = "stopped"
	runStatusStopping   = "stopping"
	runStatusBlocked    = "blocked"
	runStatusUnknown    = "unknown"
	runStatusRolledBack = "rolled-back"
	runStatusDispatched = "dispatched"
)
