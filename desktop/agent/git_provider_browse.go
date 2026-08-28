package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Remote repo browsing (commits / branches / file tree / file content /
// README / audit) via the configured GitHub / GitLab tokens. These are the
// agent-side counterparts to the mobile git widget: /git/provider/repos lists
// every repo once, and this file answers "what's inside repo X" without a
// local clone.
//
// Every handler is authenticated by the s.auth wrapper and looks up the
// stored PAT by host — the token never leaves the agent machine.
// ---------------------------------------------------------------------------

type browseCommit struct {
	SHA       string `json:"sha"`
	ShortSHA  string `json:"shortSha"`
	Message   string `json:"message"`
	Author    string `json:"author"`
	AvatarURL string `json:"avatarUrl"`
	Date      string `json:"date"`
}

type browseBranch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	HeadSHA   string `json:"headSha"`
	Default   bool   `json:"default"`
}

type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // "file" | "dir"
	Size int64  `json:"size,omitempty"`
}

// repoRef holds the parsed identity of the repo to browse. For GitHub
// `Owner` + `Repo` are the two path segments; for GitLab `Full` is the
// URL-escaped path_with_namespace (group/sub/project).
type repoRef struct {
	Host  string
	Owner string
	Repo  string
	Full  string
}

// parseRepoRef splits a full_name ("owner/repo" or "group/sub/project") into
// the pieces each provider API needs. Returns an error if the caller supplied
// nothing usable.
func parseRepoRef(host, fullName string) (*repoRef, error) {
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	ref := &repoRef{Host: host}
	fullName = strings.Trim(strings.TrimSpace(fullName), "/")
	if fullName == "" {
		return nil, fmt.Errorf("repo is required (owner/repo or group/sub/project)")
	}
	ref.Full = fullName
	parts := strings.Split(fullName, "/")
	if len(parts) >= 2 {
		ref.Owner = parts[0]
		ref.Repo = parts[len(parts)-1]
	}
	return ref, nil
}

// githubAPI builds a request against api.github.com with the provider token.
func (ref *repoRef) githubAPI(method, path string) (*http.Request, error) {
	provider := findProvider(ref.Host)
	if provider == nil {
		return nil, fmt.Errorf("no provider configured for %s — set up in Settings first", ref.Host)
	}
	if provider.Provider != "github" {
		return nil, fmt.Errorf("%s is not a GitHub provider", ref.Host)
	}
	u := "https://api.github.com" + path
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+provider.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

// gitlabAPI builds a request against the GitLab host's /api/v4 with the token.
func (ref *repoRef) gitlabAPI(method, path string) (*http.Request, error) {
	provider := findProvider(ref.Host)
	if provider == nil {
		return nil, fmt.Errorf("no provider configured for %s — set up in Settings first", ref.Host)
	}
	if provider.Provider != "gitlab" {
		return nil, fmt.Errorf("%s is not a GitLab provider", ref.Host)
	}
	u := fmt.Sprintf("https://%s/api/v4%s", ref.Host, path)
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+provider.Token)
	return req, nil
}

func (ref *repoRef) do(req *http.Request) ([]byte, int, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("provider API returned %d: %s", resp.StatusCode, truncateForBrowse(string(body), 300))
	}
	return body, resp.StatusCode, nil
}

func truncateForBrowse(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ── Branches ────────────────────────────────────────────────────────────

func (s *HTTPServer) handleGitProviderRepoBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	host := r.URL.Query().Get("host")
	ref, err := parseRepoRef(host, r.URL.Query().Get("repo"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}

	var branches []browseBranch
	switch provider.Provider {
	case "github":
		branches, err = ref.listGitHubBranches()
	case "gitlab":
		branches, err = ref.listGitLabBranches()
	default:
		jsonError(w, http.StatusBadRequest, "unsupported provider "+provider.Provider)
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to list branches: "+err.Error())
		return
	}
	if branches == nil {
		branches = []browseBranch{}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "branches": branches, "provider": provider.Provider})
}

func (ref *repoRef) listGitHubBranches() ([]browseBranch, error) {
	req, err := ref.githubAPI("GET", "/repos/"+ref.Full+"/branches?per_page=100")
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
		Commit    struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	branches := make([]browseBranch, 0, len(raw))
	for _, b := range raw {
		branches = append(branches, browseBranch{Name: b.Name, Protected: b.Protected, HeadSHA: b.Commit.SHA})
	}
	return branches, nil
}

func (ref *repoRef) listGitLabBranches() ([]browseBranch, error) {
	req, err := ref.gitlabAPI("GET", "/projects/"+url.QueryEscape(ref.Full)+"/repository/branches?per_page=100")
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
		Commit    struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	branches := make([]browseBranch, 0, len(raw))
	for _, b := range raw {
		branches = append(branches, browseBranch{Name: b.Name, Protected: b.Protected, HeadSHA: b.Commit.ID})
	}
	return branches, nil
}

// ── Commits ─────────────────────────────────────────────────────────────

func (s *HTTPServer) handleGitProviderRepoCommits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	host := q.Get("host")
	ref, err := parseRepoRef(host, q.Get("repo"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}
	branch := q.Get("branch")
	perPage := 30
	if pp := q.Get("per_page"); pp != "" {
		fmt.Sscanf(pp, "%d", &perPage)
	}
	if perPage < 1 || perPage > 100 {
		perPage = 30
	}

	var commits []browseCommit
	switch provider.Provider {
	case "github":
		commits, err = ref.listGitHubCommits(branch, perPage)
	case "gitlab":
		commits, err = ref.listGitLabCommits(branch, perPage)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported provider "+provider.Provider)
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to list commits: "+err.Error())
		return
	}
	if commits == nil {
		commits = []browseCommit{}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "commits": commits, "branch": branch})
}

func (ref *repoRef) listGitHubCommits(branch string, perPage int) ([]browseCommit, error) {
	p := "/repos/" + ref.Full + "/commits?per_page=" + fmt.Sprint(perPage)
	if branch != "" {
		p += "&sha=" + url.QueryEscape(branch)
	}
	req, err := ref.githubAPI("GET", p)
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
		Author struct {
			AvatarURL string `json:"avatar_url"`
		} `json:"author"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	commits := make([]browseCommit, 0, len(raw))
	for _, c := range raw {
		sha := c.SHA
		short := sha
		if len(short) > 7 {
			short = short[:7]
		}
		commits = append(commits, browseCommit{
			SHA:       sha,
			ShortSHA:  short,
			Message:   firstLineTrim(c.Commit.Message),
			Author:    c.Commit.Author.Name,
			AvatarURL: c.Author.AvatarURL,
			Date:      c.Commit.Author.Date,
		})
	}
	return commits, nil
}

func (ref *repoRef) listGitLabCommits(branch string, perPage int) ([]browseCommit, error) {
	p := "/projects/" + url.QueryEscape(ref.Full) + "/repository/commits?per_page=" + fmt.Sprint(perPage)
	if branch != "" {
		p += "&ref_name=" + url.QueryEscape(branch)
	}
	req, err := ref.gitlabAPI("GET", p)
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Message   string `json:"message"`
		Author    string `json:"author_name"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	commits := make([]browseCommit, 0, len(raw))
	for _, c := range raw {
		sha := c.ID
		short := sha
		if len(short) > 8 {
			short = short[:8]
		}
		msg := c.Title
		if msg == "" {
			msg = firstLineTrim(c.Message)
		}
		commits = append(commits, browseCommit{SHA: sha, ShortSHA: short, Message: msg, Author: c.Author, Date: c.CreatedAt})
	}
	return commits, nil
}

func firstLineTrim(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// ── File tree (contents listing) ────────────────────────────────────────

func (s *HTTPServer) handleGitProviderRepoTrees(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	host := q.Get("host")
	ref, err := parseRepoRef(host, q.Get("repo"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}
	branch := q.Get("branch")
	path := q.Get("path")

	var entries []browseEntry
	switch provider.Provider {
	case "github":
		entries, err = ref.listGitHubTree(branch, path)
	case "gitlab":
		entries, err = ref.listGitLabTree(branch, path)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported provider "+provider.Provider)
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to list directory: "+err.Error())
		return
	}
	if entries == nil {
		entries = []browseEntry{}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "entries": entries, "path": path, "branch": branch})
}

func (ref *repoRef) listGitHubTree(branch, path string) ([]browseEntry, error) {
	p := "/repos/" + ref.Full + "/contents/"
	if path != "" {
		p += url.PathEscape(strings.TrimPrefix(path, "/"))
	}
	if branch != "" {
		p += "?ref=" + url.QueryEscape(branch)
	}
	req, err := ref.githubAPI("GET", p)
	if err != nil {
		return nil, err
	}
	body, status, err := ref.do(req)
	if err != nil {
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("path not found in repo (empty repo or bad path)")
		}
		return nil, err
	}
	// contents/ on a file returns a single object, not an array.
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if strings.TrimSpace(string(body))[0] == '{' {
		if err := json.Unmarshal(body, &obj); err != nil {
			return nil, err
		}
		return []browseEntry{{Name: obj.Name, Path: obj.Path, Type: obj.Type, Size: obj.Size}}, nil
	}
	var raw []struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	entries := make([]browseEntry, 0, len(raw))
	for _, e := range raw {
		entries = append(entries, browseEntry{Name: e.Name, Path: e.Path, Type: e.Type, Size: e.Size})
	}
	return entries, nil
}

func (ref *repoRef) listGitLabTree(branch, path string) ([]browseEntry, error) {
	p := "/projects/" + url.QueryEscape(ref.Full) + "/repository/tree?per_page=100&recursive=false"
	if path != "" {
		p += "&path=" + url.QueryEscape(strings.TrimPrefix(path, "/"))
	}
	if branch != "" {
		p += "&ref=" + url.QueryEscape(branch)
	}
	req, err := ref.gitlabAPI("GET", p)
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"` // "blob" | "tree"
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	entries := make([]browseEntry, 0, len(raw))
	for _, e := range raw {
		t := "file"
		if e.Type == "tree" {
			t = "dir"
		}
		entries = append(entries, browseEntry{Name: e.Name, Path: e.Path, Type: t})
	}
	return entries, nil
}

// ── File content ────────────────────────────────────────────────────────

func (s *HTTPServer) handleGitProviderRepoFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	host := q.Get("host")
	ref, err := parseRepoRef(host, q.Get("repo"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}
	branch := q.Get("branch")
	path := q.Get("path")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "path is required")
		return
	}

	var content, language string
	switch provider.Provider {
	case "github":
		content, language, err = ref.getGitHubFile(branch, path)
	case "gitlab":
		content, language, err = ref.getGitLabFile(branch, path)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported provider "+provider.Provider)
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to read file: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "content": content, "language": language, "path": path})
}

func (ref *repoRef) getGitHubFile(branch, path string) (string, string, error) {
	p := "/repos/" + ref.Full + "/contents/" + url.PathEscape(strings.TrimPrefix(path, "/"))
	if branch != "" {
		p += "?ref=" + url.QueryEscape(branch)
	}
	req, err := ref.githubAPI("GET", p)
	if err != nil {
		return "", "", err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return "", "", err
	}
	var raw struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", "", err
	}
	// GitHub contents returns base64 with embedded newlines; strip them.
	raw.Content = strings.ReplaceAll(raw.Content, "\n", "")
	decoded := raw.Content
	if raw.Encoding == "base64" {
		b, err := base64.StdEncoding.DecodeString(raw.Content)
		if err != nil {
			return "", "", fmt.Errorf("decode: %v", err)
		}
		decoded = string(b)
	}
	return decoded, detectLanguage(path), nil
}

func (ref *repoRef) getGitLabFile(branch, path string) (string, string, error) {
	p := "/projects/" + url.QueryEscape(ref.Full) + "/repository/files/" + url.PathEscape(strings.TrimPrefix(path, "/")) + "/raw"
	if branch != "" {
		p += "?ref=" + url.QueryEscape(branch)
	}
	req, err := ref.gitlabAPI("GET", p)
	if err != nil {
		return "", "", err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return "", "", err
	}
	return string(body), detectLanguage(path), nil
}

// detectLanguage returns a best-effort language label from the file extension.
func detectLanguage(path string) string {
	ext := strings.ToLower(path[strings.LastIndexByte(path, '.')+1:])
	switch ext {
	case "go":
		return "go"
	case "ts", "tsx":
		return "typescript"
	case "js", "jsx", "mjs":
		return "javascript"
	case "py":
		return "python"
	case "swift":
		return "swift"
	case "kt", "kts":
		return "kotlin"
	case "rs":
		return "rust"
	case "c", "h":
		return "c"
	case "cpp", "cc", "hpp", "hh":
		return "cpp"
	case "java":
		return "java"
	case "rb":
		return "ruby"
	case "php":
		return "php"
	case "html", "htm":
		return "html"
	case "css", "scss", "less":
		return "css"
	case "json":
		return "json"
	case "yaml", "yml":
		return "yaml"
	case "toml":
		return "toml"
	case "md", "markdown":
		return "markdown"
	case "sh", "bash", "zsh":
		return "shell"
	case "sql":
		return "sql"
	case "dart":
		return "dart"
	case "m":
		return "objective-c"
	case "vue":
		return "vue"
	case "xml":
		return "xml"
	case "dockerfile":
		return "dockerfile"
	case "gradle":
		return "groovy"
	case "txt", "text":
		return "text"
	default:
		return ""
	}
}

// ── README ──────────────────────────────────────────────────────────────

func (s *HTTPServer) handleGitProviderRepoReadme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	host := q.Get("host")
	ref, err := parseRepoRef(host, q.Get("repo"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}
	branch := q.Get("branch")

	var readme, readmePath string
	switch provider.Provider {
	case "github":
		readme, readmePath, err = ref.getGitHubReadme(branch)
	case "gitlab":
		readme, readmePath, err = ref.getGitLabReadme(branch)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported provider "+provider.Provider)
		return
	}
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "readme": "", "path": "", "error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "readme": readme, "path": readmePath})
}

func (ref *repoRef) getGitHubReadme(branch string) (string, string, error) {
	p := "/repos/" + ref.Full + "/readme"
	if branch != "" {
		p += "?ref=" + url.QueryEscape(branch)
	}
	req, err := ref.githubAPI("GET", p)
	if err != nil {
		return "", "", err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return "", "", err
	}
	var raw struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", "", err
	}
	raw.Content = strings.ReplaceAll(raw.Content, "\n", "")
	if raw.Encoding == "base64" {
		b, err := base64.StdEncoding.DecodeString(raw.Content)
		if err != nil {
			return "", "", err
		}
		raw.Content = string(b)
	}
	return raw.Content, raw.Path, nil
}

func (ref *repoRef) getGitLabReadme(branch string) (string, string, error) {
	// GitLab has no single readme endpoint; probe the common names.
	candidates := []string{"README.md", "README.markdown", "README.rst", "readme.md", "README.txt", "Readme.md"}
	for _, name := range candidates {
		p := "/projects/" + url.QueryEscape(ref.Full) + "/repository/files/" + url.PathEscape(name) + "/raw"
		if branch != "" {
			p += "?ref=" + url.QueryEscape(branch)
		}
		req, err := ref.gitlabAPI("GET", p)
		if err != nil {
			return "", "", err
		}
		body, status, err := ref.do(req)
		if err != nil {
			if status == http.StatusNotFound {
				continue
			}
			return "", "", err
		}
		if len(body) > 0 || status == http.StatusOK {
			return string(body), name, nil
		}
	}
	return "", "", fmt.Errorf("no README found")
}

// ── Deep analysis audit ─────────────────────────────────────────────────

func (s *HTTPServer) handleGitProviderRepoAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	host := q.Get("host")
	ref, err := parseRepoRef(host, q.Get("repo"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}

	var audit map[string]interface{}
	switch provider.Provider {
	case "github":
		audit, err = ref.auditGitHub()
	case "gitlab":
		audit, err = ref.auditGitLab()
	default:
		jsonError(w, http.StatusBadRequest, "unsupported provider "+provider.Provider)
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to audit repo: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "audit": audit})
}

func (ref *repoRef) auditGitHub() (map[string]interface{}, error) {
	req, err := ref.githubAPI("GET", "/repos/"+ref.Full)
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var repo struct {
		FullName        string `json:"full_name"`
		Description     string `json:"description"`
		Private         bool   `json:"private"`
		Fork            bool   `json:"fork"`
		DefaultBranch   string `json:"default_branch"`
		Language        string `json:"language"`
		Stars           int    `json:"stargazers_count"`
		Forks           int    `json:"forks_count"`
		OpenIssues      int    `json:"open_issues_count"`
		Watchers        int    `json:"subscribers_count"`
		Archived        bool   `json:"archived"`
		License         *struct {
			SPDXID string `json:"spdx_id"`
			Name   string `json:"name"`
		} `json:"license"`
		Topics    []string `json:"topics"`
		CreatedAt string   `json:"created_at"`
		UpdatedAt string   `json:"updated_at"`
		PushedAt  string   `json:"pushed_at"`
		Size      int      `json:"size"`
	}
	if err := json.Unmarshal(body, &repo); err != nil {
		return nil, err
	}
	license := ""
	if repo.License != nil {
		license = repo.License.SPDXID
		if license == "" {
			license = repo.License.Name
		}
	}
	return map[string]interface{}{
		"fullName":      repo.FullName,
		"description":   repo.Description,
		"private":       repo.Private,
		"fork":          repo.Fork,
		"defaultBranch": repo.DefaultBranch,
		"language":      repo.Language,
		"stars":         repo.Stars,
		"forks":         repo.Forks,
		"openIssues":    repo.OpenIssues,
		"watchers":      repo.Watchers,
		"archived":      repo.Archived,
		"license":       license,
		"topics":        repo.Topics,
		"createdAt":     repo.CreatedAt,
		"updatedAt":     repo.UpdatedAt,
		"pushedAt":      repo.PushedAt,
		"sizeKb":        repo.Size,
	}, nil
}

func (ref *repoRef) auditGitLab() (map[string]interface{}, error) {
	req, err := ref.gitlabAPI("GET", "/projects/"+url.QueryEscape(ref.Full))
	if err != nil {
		return nil, err
	}
	body, _, err := ref.do(req)
	if err != nil {
		return nil, err
	}
	var repo struct {
		PathWithNS    string `json:"path_with_namespace"`
		Description   string `json:"description"`
		Visibility    string `json:"visibility"`
		DefaultBranch string `json:"default_branch"`
		StarCount     int    `json:"star_count"`
		ForksCount    int    `json:"forks_count"`
		OpenIssues    int    `json:"open_issues_count"`
		Archived      bool   `json:"archived"`
		CreatedAt     string `json:"created_at"`
		LastActivity  string `json:"last_activity_at"`
	}
	if err := json.Unmarshal(body, &repo); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"fullName":      repo.PathWithNS,
		"description":   repo.Description,
		"private":       repo.Visibility != "public",
		"fork":          false,
		"defaultBranch": repo.DefaultBranch,
		"language":      "",
		"stars":         repo.StarCount,
		"forks":         repo.ForksCount,
		"openIssues":    repo.OpenIssues,
		"watchers":      0,
		"archived":      repo.Archived,
		"license":       "",
		"topics":        []string{},
		"createdAt":     repo.CreatedAt,
		"updatedAt":     repo.LastActivity,
		"pushedAt":      repo.LastActivity,
		"sizeKb":        0,
	}, nil
}
