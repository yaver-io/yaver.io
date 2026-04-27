package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	osexec "os/exec"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Standup generator — what did I do yesterday / today (from git log)
// ---------------------------------------------------------------------------

func mcpStandup(dir string, days int) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if days <= 0 {
		days = 1
	}
	since := fmt.Sprintf("--since=%d.days.ago", days)

	// Get current user's commits
	user, _ := gitCmd(dir, "config", "user.name")
	user = strings.TrimSpace(user)

	log, err := gitCmd(dir, "log", since, "--author="+user, "--pretty=format:- %s", "--no-merges")
	if err != nil || log == "" {
		log, _ = gitCmd(dir, "log", since, "--pretty=format:- %s", "--no-merges", "-20")
	}

	// Get changed files
	files, _ := gitCmd(dir, "diff", "--stat", fmt.Sprintf("HEAD~%d", days*3), "HEAD")

	return map[string]interface{}{
		"author":   user,
		"commits":  log,
		"files":    files,
		"days":     days,
		"template": fmt.Sprintf("**Yesterday:**\n%s\n\n**Today:**\n- \n\n**Blockers:**\n- None", log),
	}
}

// ---------------------------------------------------------------------------
// Share snippet — create a GitHub Gist
// ---------------------------------------------------------------------------

func mcpCreateGist(filename, content, description string, public bool) interface{} {
	if filename == "" {
		filename = "snippet.txt"
	}
	if description == "" {
		description = "Shared from Yaver"
	}
	gist := map[string]interface{}{
		"description": description,
		"public":      public,
		"files": map[string]interface{}{
			filename: map[string]interface{}{
				"content": content,
			},
		},
	}
	data, _ := json.Marshal(gist)

	// Use gh CLI (authenticated)
	cmd := osexec.Command("gh", "gist", "create", "--filename", filename)
	if public {
		cmd.Args = append(cmd.Args, "--public")
	}
	if description != "" {
		cmd.Args = append(cmd.Args, "--desc", description)
	}
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback to API
		_ = data // suppress unused
		return map[string]interface{}{"error": fmt.Sprintf("gh gist create failed: %s (install: brew install gh)", string(out))}
	}
	return map[string]interface{}{"url": strings.TrimSpace(string(out)), "filename": filename}
}

// ---------------------------------------------------------------------------
// Changelog generator — from git tags/log
// ---------------------------------------------------------------------------

func mcpChangelog(dir string, from, to string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if to == "" {
		to = "HEAD"
	}
	if from == "" {
		// Get previous tag
		from, _ = gitCmd(dir, "describe", "--tags", "--abbrev=0", to+"^")
		from = strings.TrimSpace(from)
		if from == "" {
			from = "HEAD~20"
		}
	}

	log, _ := gitCmd(dir, "log", fmt.Sprintf("%s..%s", from, to), "--pretty=format:- %s (%h)", "--no-merges")

	// Categorize by conventional commits
	var features, fixes, other []string
	for _, line := range strings.Split(log, "\n") {
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "feat") || strings.Contains(lower, "add") {
			features = append(features, line)
		} else if strings.Contains(lower, "fix") || strings.Contains(lower, "bug") {
			fixes = append(fixes, line)
		} else {
			other = append(other, line)
		}
	}

	changelog := fmt.Sprintf("## Changelog (%s → %s)\n\n", from, to)
	if len(features) > 0 {
		changelog += "### Features\n" + strings.Join(features, "\n") + "\n\n"
	}
	if len(fixes) > 0 {
		changelog += "### Bug Fixes\n" + strings.Join(fixes, "\n") + "\n\n"
	}
	if len(other) > 0 {
		changelog += "### Other\n" + strings.Join(other, "\n") + "\n\n"
	}

	return map[string]interface{}{"changelog": changelog, "from": from, "to": to}
}

// ---------------------------------------------------------------------------
// Commit message helper — generate from diff
// ---------------------------------------------------------------------------

func mcpCommitMessage(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	diff, _ := gitCmd(dir, "diff", "--cached", "--stat")
	if diff == "" {
		diff, _ = gitCmd(dir, "diff", "--stat")
	}
	diffContent, _ := gitCmd(dir, "diff", "--cached")
	if diffContent == "" {
		diffContent, _ = gitCmd(dir, "diff")
	}

	// Truncate for analysis
	if len(diffContent) > 3000 {
		diffContent = diffContent[:3000] + "\n... (truncated)"
	}

	return map[string]interface{}{
		"diff_stat": diff,
		"diff":      diffContent,
		"hint":      "Use this diff to generate a conventional commit message. Format: type(scope): description",
	}
}

// ---------------------------------------------------------------------------
// Gitignore generator
// ---------------------------------------------------------------------------

func mcpGitignore(languages []string) interface{} {
	if len(languages) == 0 {
		return map[string]interface{}{"error": "specify languages (e.g. go, node, python, rust, java)"}
	}
	url := "https://www.toptal.com/developers/gitignore/api/" + strings.Join(languages, ",")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return map[string]interface{}{"gitignore": string(body), "languages": languages}
}

// ---------------------------------------------------------------------------
// License generator
// ---------------------------------------------------------------------------

func mcpLicense(licenseType, author string, year int) interface{} {
	if year <= 0 {
		year = time.Now().Year()
	}
	if author == "" {
		author, _ = gitCmd(".", "config", "user.name")
		author = strings.TrimSpace(author)
	}

	var text string
	switch strings.ToLower(licenseType) {
	case "mit":
		text = fmt.Sprintf(`MIT License

Copyright (c) %d %s

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.`, year, author)
	case "apache", "apache-2.0":
		text = fmt.Sprintf("Copyright %d %s\n\nLicensed under the Apache License, Version 2.0", year, author)
	case "gpl", "gpl-3.0":
		text = fmt.Sprintf("Copyright (C) %d %s\n\nThis program is free software: you can redistribute it and/or modify\nit under the terms of the GNU General Public License as published by\nthe Free Software Foundation, either version 3 of the License.", year, author)
	default:
		return map[string]interface{}{"error": "license type: mit, apache, gpl"}
	}
	return map[string]interface{}{"license": text, "type": licenseType}
}

// ---------------------------------------------------------------------------
// Color picker / converter
// ---------------------------------------------------------------------------

func mcpColor(input string) interface{} {
	input = strings.TrimSpace(input)
	// Try to parse hex
	hex := strings.TrimPrefix(input, "#")
	if len(hex) == 6 {
		r := hexToInt(hex[0:2])
		g := hexToInt(hex[2:4])
		b := hexToInt(hex[4:6])
		return map[string]interface{}{
			"hex": "#" + hex,
			"rgb": fmt.Sprintf("rgb(%d, %d, %d)", r, g, b),
			"hsl": rgbToHSL(r, g, b),
		}
	}
	if len(hex) == 3 {
		r := hexToInt(string(hex[0]) + string(hex[0]))
		g := hexToInt(string(hex[1]) + string(hex[1]))
		b := hexToInt(string(hex[2]) + string(hex[2]))
		return map[string]interface{}{
			"hex": fmt.Sprintf("#%02x%02x%02x", r, g, b),
			"rgb": fmt.Sprintf("rgb(%d, %d, %d)", r, g, b),
			"hsl": rgbToHSL(r, g, b),
		}
	}
	// Named colors
	colors := map[string]string{
		"red": "ff0000", "green": "00ff00", "blue": "0000ff",
		"white": "ffffff", "black": "000000", "yellow": "ffff00",
		"cyan": "00ffff", "magenta": "ff00ff", "orange": "ff8c00",
		"purple": "800080", "pink": "ff69b4", "gray": "808080",
	}
	if h, ok := colors[strings.ToLower(input)]; ok {
		return mcpColor("#" + h)
	}
	return map[string]interface{}{"error": "provide hex (#ff0000) or named color (red, blue, etc.)"}
}

func hexToInt(h string) int {
	var n int
	fmt.Sscanf(h, "%x", &n)
	return n
}

func rgbToHSL(r, g, b int) string {
	rf, gf, bf := float64(r)/255, float64(g)/255, float64(b)/255
	max := rf
	if gf > max { max = gf }
	if bf > max { max = bf }
	min := rf
	if gf < min { min = gf }
	if bf < min { min = bf }
	l := (max + min) / 2
	if max == min {
		return fmt.Sprintf("hsl(0, 0%%, %.0f%%)", l*100)
	}
	d := max - min
	s := d / (1 - abs(2*l-1))
	var h float64
	switch max {
	case rf:
		h = (gf - bf) / d
		if gf < bf { h += 6 }
	case gf:
		h = (bf-rf)/d + 2
	case bf:
		h = (rf-gf)/d + 4
	}
	h *= 60
	return fmt.Sprintf("hsl(%.0f, %.0f%%, %.0f%%)", h, s*100, l*100)
}

func abs(x float64) float64 {
	if x < 0 { return -x }
	return x
}

// ---------------------------------------------------------------------------
// ASCII art / figlet
// ---------------------------------------------------------------------------

func mcpFiglet(text string) interface{} {
	out, err := runCmd("figlet", text)
	if err != nil {
		out, err = runCmd("toilet", text)
		if err != nil {
			// Simple fallback
			return map[string]interface{}{"text": strings.ToUpper(text), "note": "Install figlet for ASCII art: brew install figlet"}
		}
	}
	return map[string]interface{}{"art": out}
}

// ---------------------------------------------------------------------------
// Lorem ipsum
// ---------------------------------------------------------------------------

func mcpLoremIpsum(paragraphs int) interface{} {
	if paragraphs <= 0 {
		paragraphs = 1
	}
	words := []string{"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit", "sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore", "et", "dolore", "magna", "aliqua", "enim", "ad", "minim", "veniam", "quis", "nostrud", "exercitation", "ullamco", "laboris", "nisi", "aliquip", "ex", "ea", "commodo", "consequat", "duis", "aute", "irure", "in", "reprehenderit", "voluptate", "velit", "esse", "cillum", "fugiat", "nulla", "pariatur", "excepteur", "sint", "occaecat", "cupidatat", "non", "proident", "sunt", "culpa", "qui", "officia", "deserunt", "mollit", "anim", "id", "est", "laborum"}

	var paras []string
	for p := 0; p < paragraphs; p++ {
		sentenceCount := 4 + rand.Intn(4)
		var sentences []string
		for s := 0; s < sentenceCount; s++ {
			wordCount := 8 + rand.Intn(12)
			var w []string
			for i := 0; i < wordCount; i++ {
				w = append(w, words[rand.Intn(len(words))])
			}
			w[0] = strings.Title(w[0])
			sentences = append(sentences, strings.Join(w, " ")+".")
		}
		paras = append(paras, strings.Join(sentences, " "))
	}
	return map[string]interface{}{"text": strings.Join(paras, "\n\n"), "paragraphs": paragraphs}
}

// ---------------------------------------------------------------------------
// tldr — quick command help
// ---------------------------------------------------------------------------

func mcpTldr(command string) interface{} {
	out, err := runCmd("tldr", command)
	if err != nil {
		// Fallback to man -f
		out, err = runCmd("man", "-f", command)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("no docs found for '%s'. Install tldr: npm install -g tldr", command)}
		}
	}
	return map[string]interface{}{"command": command, "help": out}
}

// ---------------------------------------------------------------------------
// GitHub badge generator — for READMEs
// ---------------------------------------------------------------------------

func mcpGitHubBadge(dir string) interface{} {
	remote, _ := gitCmd(dir, "config", "--get", "remote.origin.url")
	remote = strings.TrimSpace(remote)

	// Extract owner/repo
	repo := ""
	if strings.Contains(remote, "github.com") {
		parts := strings.Split(remote, "github.com")
		if len(parts) > 1 {
			repo = strings.TrimPrefix(parts[1], "/")
			repo = strings.TrimPrefix(repo, ":")
			repo = strings.TrimSuffix(repo, ".git")
		}
	}

	badges := map[string]string{
		"ci":       fmt.Sprintf("[![CI](https://github.com/%s/actions/workflows/ci.yml/badge.svg)](https://github.com/%s/actions)", repo, repo),
		"license":  fmt.Sprintf("[![License](https://img.shields.io/github/license/%s)](https://github.com/%s/blob/main/LICENSE)", repo, repo),
		"stars":    fmt.Sprintf("[![Stars](https://img.shields.io/github/stars/%s)](https://github.com/%s)", repo, repo),
		"release":  fmt.Sprintf("[![Release](https://img.shields.io/github/v/release/%s)](https://github.com/%s/releases)", repo, repo),
		"issues":   fmt.Sprintf("[![Issues](https://img.shields.io/github/issues/%s)](https://github.com/%s/issues)", repo, repo),
		"yaver":    "[![Yaver](https://img.shields.io/badge/MCP-Yaver-blue)](https://yaver.io)",
	}

	return map[string]interface{}{
		"repo":   repo,
		"badges": badges,
		"usage":  "Copy any badge markdown into your README.md",
	}
}

// ---------------------------------------------------------------------------
// Invite / share Yaver
// ---------------------------------------------------------------------------

func mcpInvite(method, recipient string) interface{} {
	shareURL := "https://yaver.io?ref=mcp"
	message := fmt.Sprintf("Check out Yaver — 157 MCP tools for developers. Control your dev machine, smart home, and AI agents from anywhere. %s", shareURL)

	switch method {
	case "clipboard":
		mcpClipboardWrite(message)
		return map[string]interface{}{"ok": true, "message": "Invite link copied to clipboard", "url": shareURL}
	case "email":
		subject := "Check out Yaver — developer MCP server"
		mailURL := fmt.Sprintf("mailto:%s?subject=%s&body=%s", recipient, url.QueryEscape(subject), url.QueryEscape(message))
		return map[string]interface{}{"ok": true, "message": "Email URL ready", "url": shareURL, "mailto": mailURL}
	case "slack":
		return map[string]interface{}{"message": message, "url": shareURL, "note": "Paste this in your Slack channel"}
	default:
		return map[string]interface{}{
			"url":     shareURL,
			"message": message,
			"methods": []string{"clipboard", "email", "slack"},
			"badge":   "[![Yaver](https://img.shields.io/badge/MCP-Yaver-blue)](https://yaver.io)",
		}
	}
}

// ---------------------------------------------------------------------------
// Git stats — contribution overview
// ---------------------------------------------------------------------------

func mcpGitStats(dir string, days int) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if days <= 0 {
		days = 30
	}
	since := fmt.Sprintf("--since=%d.days.ago", days)

	user, _ := gitCmd(dir, "config", "user.name")
	user = strings.TrimSpace(user)

	// Commit count
	count, _ := gitCmd(dir, "rev-list", "--count", since, "HEAD")
	// Lines changed
	shortlog, _ := gitCmd(dir, "log", since, "--author="+user, "--pretty=tformat:", "--numstat")
	var added, removed int
	for _, line := range strings.Split(shortlog, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			a, r := 0, 0
			fmt.Sscanf(parts[0], "%d", &a)
			fmt.Sscanf(parts[1], "%d", &r)
			added += a
			removed += r
		}
	}
	// Most changed files
	files, _ := gitCmd(dir, "log", since, "--author="+user, "--pretty=format:", "--name-only")
	fileCounts := map[string]int{}
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f != "" {
			fileCounts[f]++
		}
	}
	// Top 5 files
	type fc struct{ name string; count int }
	var topFiles []fc
	for name, count := range fileCounts {
		topFiles = append(topFiles, fc{name, count})
	}
	// Simple sort
	for i := 0; i < len(topFiles); i++ {
		for j := i + 1; j < len(topFiles); j++ {
			if topFiles[j].count > topFiles[i].count {
				topFiles[i], topFiles[j] = topFiles[j], topFiles[i]
			}
		}
	}
	if len(topFiles) > 5 {
		topFiles = topFiles[:5]
	}
	var topFileNames []string
	for _, f := range topFiles {
		topFileNames = append(topFileNames, fmt.Sprintf("%s (%d changes)", f.name, f.count))
	}

	// Languages (by extension)
	extCounts := map[string]int{}
	for name := range fileCounts {
		ext := filepath.Ext(name)
		if ext != "" {
			extCounts[ext]++
		}
	}

	return map[string]interface{}{
		"author":       user,
		"days":         days,
		"commits":      strings.TrimSpace(count),
		"lines_added":  added,
		"lines_removed": removed,
		"top_files":    topFileNames,
		"languages":    extCounts,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func gitCmd(dir string, args ...string) (string, error) {
	cmd := osexec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
