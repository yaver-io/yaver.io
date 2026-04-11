package main

// changelog.go — self-hosted changelog / release-notes publisher.
// Replaces Headway / LaunchNotes / Canny Changelog for the solo-
// SaaS case. Sits alongside the existing /statuspage + /releases
// surfaces so a dev's users can see "what changed" without any
// third-party widget.
//
// Data model: one append-only JSON ledger at
// ~/.yaver/changelog/entries.json with {version, title, body,
// publishedAt, channel, tags[]}. Integrations:
//
//   - `yaver release publish` auto-appends a draft entry when
//     --notes is passed (handled via a follow-up — for now
//     the dev runs `yaver changelog add` manually right after
//     a release),
//   - `yaver changelog publish` re-renders the static HTML
//     page at ~/.yaver/changelog/index.html,
//   - `GET /changelog` returns the JSON feed,
//   - `GET /changelog.html` serves the rendered page live
//     through the existing relay,
//   - `GET /changelog.atom` exposes an Atom feed so RSS
//     readers / dev-marketing tools like Feedly can subscribe.

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChangelogEntry is one published change.
type ChangelogEntry struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty"`       // markdown-lite
	Channel     string   `json:"channel,omitempty"`    // "production" | "beta" | ...
	Tags        []string `json:"tags,omitempty"`       // ["fix","feature","breaking"]
	PublishedAt string   `json:"publishedAt"`
	Author      string   `json:"author,omitempty"`
}

var changelogMu sync.Mutex

func changelogDir() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "changelog")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func loadChangelog() ([]ChangelogEntry, error) {
	dir, err := changelogDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "entries.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return []ChangelogEntry{}, nil
		}
		return nil, err
	}
	var payload struct {
		Entries []ChangelogEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Entries, nil
}

func saveChangelog(entries []ChangelogEntry) error {
	dir, err := changelogDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "entries.json")
	data, err := json.MarshalIndent(map[string]interface{}{
		"entries": entries,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sortChangelog puts newest first. Ties broken lexicographically
// by ID (UUID-ish, so stable across re-renders).
func sortChangelog(entries []ChangelogEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].PublishedAt != entries[j].PublishedAt {
			return entries[i].PublishedAt > entries[j].PublishedAt
		}
		return entries[i].ID > entries[j].ID
	})
}

// --- CLI -----------------------------------------------------------------

func runChangelog(args []string) {
	if len(args) == 0 {
		printChangelogUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "add":
		changelogAddCmd(args[1:])
	case "list", "ls":
		changelogListCmd()
	case "delete", "rm":
		changelogDeleteCmd(args[1:])
	case "publish", "render":
		changelogPublishCmd()
	case "help", "--help", "-h":
		printChangelogUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown changelog subcommand: %s\n\n", args[0])
		printChangelogUsage()
		os.Exit(1)
	}
}

func printChangelogUsage() {
	fmt.Print(`Yaver changelog — self-hosted release notes.

Usage:
  yaver changelog add <version> <title> [--body <md>] [--tag fix] [--tag feature] [--channel production]
  yaver changelog list                       Print every entry (newest first)
  yaver changelog delete <id>                Remove one entry
  yaver changelog publish                    Render static HTML to ~/.yaver/changelog/index.html

Entries live in ~/.yaver/changelog/entries.json and are served
live at GET /changelog (JSON) / /changelog.html (rendered) /
/changelog.atom (RSS). The static render is also writable so
you can push it to any static host without serving through the
agent.
`)
}

func changelogAddCmd(args []string) {
	fs := flag.NewFlagSet("changelog add", flag.ExitOnError)
	body := fs.String("body", "", "markdown body")
	channel := fs.String("channel", "production", "release channel")
	author := fs.String("author", "", "author name")
	var tags multiString
	fs.Var(&tags, "tag", "tag (repeatable)")
	fs.Parse(args)

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver changelog add <version> <title> [flags]")
		os.Exit(1)
	}
	version := fs.Arg(0)
	title := strings.Join(fs.Args()[1:], " ")

	// Optional stdin body if --body wasn't given and stdin is piped.
	if *body == "" {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			buf := make([]byte, 1<<20)
			n, _ := os.Stdin.Read(buf)
			*body = strings.TrimRight(string(buf[:n]), "\n")
		}
	}

	changelogMu.Lock()
	defer changelogMu.Unlock()

	entries, err := loadChangelog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	entry := ChangelogEntry{
		ID:          randomID(),
		Version:     version,
		Title:       title,
		Body:        *body,
		Channel:     *channel,
		Tags:        tags,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
		Author:      *author,
	}
	entries = append(entries, entry)
	sortChangelog(entries)
	if err := saveChangelog(entries); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s  %s\n", entry.Version, entry.Title)
}

func changelogListCmd() {
	entries, err := loadChangelog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("No changelog entries yet. `yaver changelog add 1.0.0 \"Initial release\"`")
		return
	}
	sortChangelog(entries)
	for _, e := range entries {
		fmt.Printf("%s  %s  %s\n", e.PublishedAt, e.Version, e.Title)
		if len(e.Tags) > 0 {
			fmt.Printf("  tags: %s\n", strings.Join(e.Tags, ", "))
		}
		if e.Body != "" {
			for _, line := range strings.Split(e.Body, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
}

func changelogDeleteCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver changelog delete <id>")
		os.Exit(1)
	}
	id := args[0]
	changelogMu.Lock()
	defer changelogMu.Unlock()
	entries, _ := loadChangelog()
	out := entries[:0]
	hit := false
	for _, e := range entries {
		if e.ID == id || e.Version == id {
			hit = true
			continue
		}
		out = append(out, e)
	}
	if !hit {
		fmt.Fprintf(os.Stderr, "entry %q not found\n", id)
		os.Exit(2)
	}
	_ = saveChangelog(out)
	fmt.Printf("✓ removed %s\n", id)
}

func changelogPublishCmd() {
	dir, err := changelogDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dir: %v\n", err)
		os.Exit(1)
	}
	entries, err := loadChangelog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	sortChangelog(entries)
	out := filepath.Join(dir, "index.html")
	if err := renderChangelogHTML(out, entries); err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ rendered %d entries → %s\n", len(entries), out)
}

// multiString collects a repeated CLI flag into a slice.
type multiString []string

func (m *multiString) String() string     { return strings.Join(*m, ",") }
func (m *multiString) Set(s string) error { *m = append(*m, s); return nil }

// --- HTTP handlers --------------------------------------------------------

// handleChangelog serves GET /changelog (JSON) and POST
// /changelog to append new entries over HTTP for devs who want
// to trigger from a CI job.
func (s *HTTPServer) handleChangelog(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries, err := loadChangelog()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sortChangelog(entries)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"entries": entries,
		})
	case http.MethodPost:
		var body ChangelogEntry
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Version == "" || body.Title == "" {
			jsonError(w, http.StatusBadRequest, "version and title required")
			return
		}
		if body.ID == "" {
			body.ID = randomID()
		}
		if body.PublishedAt == "" {
			body.PublishedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if body.Channel == "" {
			body.Channel = "production"
		}
		changelogMu.Lock()
		defer changelogMu.Unlock()
		entries, _ := loadChangelog()
		entries = append(entries, body)
		sortChangelog(entries)
		if err := saveChangelog(entries); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "entry": body})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

// handleChangelogHTML serves the rendered page live. Re-renders
// on every request since the entries list is small and the
// template is fast.
func (s *HTTPServer) handleChangelogHTML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	entries, err := loadChangelog()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sortChangelog(entries)
	tmpl, err := template.New("changelog").Funcs(changelogFuncs).Parse(changelogTemplate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = tmpl.Execute(w, changelogPageData{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Entries:     entries,
	})
}

// handleChangelogAtom serves the Atom feed. Useful for RSS
// readers + dev-marketing tools.
func (s *HTTPServer) handleChangelogAtom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	entries, _ := loadChangelog()
	sortChangelog(entries)
	type atomEntry struct {
		XMLName xml.Name `xml:"entry"`
		Title   string   `xml:"title"`
		ID      string   `xml:"id"`
		Updated string   `xml:"updated"`
		Summary string   `xml:"summary"`
	}
	type atomFeed struct {
		XMLName xml.Name    `xml:"feed"`
		Xmlns   string      `xml:"xmlns,attr"`
		Title   string      `xml:"title"`
		Updated string      `xml:"updated"`
		Entries []atomEntry `xml:"entry"`
	}
	feed := atomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		Title:   "Yaver Changelog",
		Updated: time.Now().UTC().Format(time.RFC3339),
	}
	for _, e := range entries {
		feed.Entries = append(feed.Entries, atomEntry{
			Title:   e.Version + " — " + e.Title,
			ID:      e.ID,
			Updated: e.PublishedAt,
			Summary: e.Body,
		})
	}
	data, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/atom+xml")
	w.Write([]byte(xml.Header))
	w.Write(data)
}

// --- HTML template --------------------------------------------------------

type changelogPageData struct {
	GeneratedAt string
	Entries     []ChangelogEntry
}

var changelogFuncs = template.FuncMap{
	"fmtDate": func(iso string) string {
		t, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return iso
		}
		return t.Format("January 2, 2006")
	},
	"tagColor": func(tag string) string {
		switch strings.ToLower(tag) {
		case "breaking":
			return "#dc2626"
		case "feature":
			return "#2563eb"
		case "fix":
			return "#16a34a"
		case "security":
			return "#dc2626"
		case "performance":
			return "#9333ea"
		}
		return "#6b7280"
	},
}

const changelogTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Changelog — Yaver</title>
<link rel="alternate" type="application/atom+xml" title="Changelog" href="/changelog.atom">
<style>
  :root { color-scheme: light dark; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 820px; margin: 2rem auto; padding: 0 1rem; line-height: 1.6; }
  h1 { margin-bottom: 0.25rem; }
  header p { color: #888; margin-top: 0; }
  .entry { border: 1px solid rgba(128,128,128,0.2); border-radius: 10px; padding: 1.2rem 1.4rem; margin: 1.2rem 0; }
  .entry header { display: flex; justify-content: space-between; align-items: baseline; gap: 1rem; margin-bottom: 0.3rem; }
  .entry h2 { margin: 0; font-size: 1.1rem; }
  .version { font-family: "SF Mono", Menlo, monospace; color: #6366f1; font-weight: 600; }
  .date { color: #888; font-size: 0.85rem; }
  .tags { margin-top: 0.3rem; }
  .tag { display: inline-block; padding: 0.1rem 0.6rem; border-radius: 999px; font-size: 0.7rem; font-weight: 600; color: #fff; margin-right: 0.3rem; text-transform: uppercase; }
  .body { margin-top: 0.8rem; white-space: pre-wrap; color: #333; }
  @media (prefers-color-scheme: dark) { .body { color: #ddd; } }
  footer { color: #888; font-size: 0.75rem; text-align: center; margin-top: 3rem; }
</style>
</head>
<body>

<header>
  <h1>Changelog</h1>
  <p>Self-hosted · generated {{.GeneratedAt}} · <a href="/changelog.atom">RSS</a></p>
</header>

{{range .Entries}}
<article class="entry">
  <header>
    <h2><span class="version">{{.Version}}</span> &mdash; {{.Title}}</h2>
    <span class="date">{{fmtDate .PublishedAt}}</span>
  </header>
  {{if .Tags}}
  <div class="tags">
    {{range .Tags}}<span class="tag" style="background-color: {{tagColor .}}">{{.}}</span>{{end}}
  </div>
  {{end}}
  {{if .Body}}
  <div class="body">{{.Body}}</div>
  {{end}}
</article>
{{end}}

<footer>
  Powered by Yaver &middot; self-hosted, no vendor &middot; <a href="https://yaver.io">yaver.io</a>
</footer>

</body>
</html>
`

// renderChangelogHTML writes the static page to disk. Used by
// `yaver changelog publish`.
func renderChangelogHTML(out string, entries []ChangelogEntry) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	tmpl, err := template.New("changelog").Funcs(changelogFuncs).Parse(changelogTemplate)
	if err != nil {
		return err
	}
	return tmpl.Execute(f, changelogPageData{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Entries:     entries,
	})
}
