package main

import "strings"

func deploySlot(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		target = "unknown"
	}
	return "deploy:" + target
}

func deployStatusOK(status string) bool {
	switch strings.TrimSpace(status) {
	case runStatusCompleted:
		return true
	default:
		return false
	}
}

func deployStatusInProgress(status string) bool {
	switch strings.TrimSpace(status) {
	case runStatusRunning, runStatusStopping:
		return true
	default:
		return false
	}
}
