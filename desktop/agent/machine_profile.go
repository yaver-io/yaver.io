package main

import (
	"os"
	"path/filepath"
	"strings"
)

type MachineProfile struct {
	Path         string   `json:"path,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Signatures   []string `json:"signatures,omitempty"`
	PreferredFor []string `json:"preferredFor,omitempty"`
	Raw          string   `json:"raw,omitempty"`
}

func loadMachineProfile(workDir string) *MachineProfile {
	for _, path := range candidateMachineProfilePaths(workDir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		profile := parseMachineProfileText(text)
		profile.Path = path
		profile.Raw = text
		return profile
	}
	return nil
}

func candidateMachineProfilePaths(workDir string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	if workDir != "" {
		add(filepath.Join(workDir, ".yaver", "machine.md"))
	}
	if dir, err := ConfigDir(); err == nil {
		add(filepath.Join(dir, "machine.md"))
	}
	return out
}

func parseMachineProfileText(text string) *MachineProfile {
	p := &MachineProfile{}
	lines := strings.Split(text, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		clean := strings.TrimLeft(line, "#-* ")
		lower := strings.ToLower(clean)
		switch {
		case strings.HasPrefix(lower, "summary:"):
			p.Summary = strings.TrimSpace(clean[len("summary:"):])
		case strings.HasPrefix(lower, "tags:"):
			p.Tags = append(p.Tags, splitProfileCSV(clean[len("tags:"):])...)
		case strings.HasPrefix(lower, "signatures:"):
			p.Signatures = append(p.Signatures, splitProfileCSV(clean[len("signatures:"):])...)
		case strings.HasPrefix(lower, "preferred_for:"):
			p.PreferredFor = append(p.PreferredFor, splitProfileCSV(clean[len("preferred_for:"):])...)
		case strings.HasPrefix(lower, "preferred-for:"):
			p.PreferredFor = append(p.PreferredFor, splitProfileCSV(clean[len("preferred-for:"):])...)
		case strings.HasPrefix(lower, "roles:"):
			p.PreferredFor = append(p.PreferredFor, splitProfileCSV(clean[len("roles:"):])...)
		default:
			for _, token := range splitProfileCSV(clean) {
				switch token {
				case "testflight", "android", "playstore", "ios", "mac", "linux", "raspberry-pi", "hetzner", "docker", "ssd", "ollama", "codex", "claude", "xcode", "gradle":
					p.Tags = append(p.Tags, token)
				}
			}
		}
	}
	p.Tags = uniqStrings(p.Tags)
	p.Signatures = uniqStrings(p.Signatures)
	p.PreferredFor = uniqStrings(p.PreferredFor)
	return p
}

func splitProfileCSV(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(s)), func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == '/'
	})
	var out []string
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		out = append(out, field)
	}
	return out
}
