package main

import (
	"os"
	"strings"
)

// safePTYTermName accepts a client-provided TERM for PTY-backed terminals.
// It intentionally allows only terminfo-style names so query params cannot
// inject env entries or shell syntax.
func safePTYTermName(term string) string {
	term = strings.TrimSpace(term)
	if term == "" || len(term) > 64 {
		return "xterm-256color"
	}
	switch strings.ToLower(term) {
	case "dumb", "unknown":
		return "xterm-256color"
	}
	for _, r := range term {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return "xterm-256color"
		}
	}
	return term
}

func localPTYTermName() string {
	return safePTYTermName(os.Getenv("TERM"))
}
