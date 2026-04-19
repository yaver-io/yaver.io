package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type ConversationImportRequest struct {
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
	Title   string `json:"title,omitempty"`
	Runner  string `json:"runner,omitempty"`
	WorkDir string `json:"workDir,omitempty"`
}

type ConversationImportResult struct {
	SourceLabel     string   `json:"sourceLabel"`
	SourceURL       string   `json:"sourceUrl,omitempty"`
	FetchedURL      string   `json:"fetchedUrl,omitempty"`
	DetectedTitle   string   `json:"detectedTitle,omitempty"`
	SuggestedName   string   `json:"suggestedName,omitempty"`
	NormalizedText  string   `json:"normalizedText"`
	ProductGoal     string   `json:"productGoal"`
	UserProblem     string   `json:"userProblem,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	ResearchTopics  []string `json:"researchTopics,omitempty"`
	Surfaces        []string `json:"surfaces,omitempty"`
	TechnicalPlan   []string `json:"technicalPlan,omitempty"`
	DataFlow        []string `json:"dataFlow,omitempty"`
	MVPScope        []string `json:"mvpScope,omitempty"`
	Risks           []string `json:"risks,omitempty"`
	Assumptions     []string `json:"assumptions,omitempty"`
	NextPrompt      string   `json:"nextPrompt,omitempty"`
	GeneratedPrompt string   `json:"generatedPrompt"`
}

type conversationImportLLMResult struct {
	SuggestedName  string   `json:"suggestedName"`
	ProductGoal    string   `json:"productGoal"`
	UserProblem    string   `json:"userProblem"`
	Summary        string   `json:"summary"`
	ResearchTopics []string `json:"researchTopics"`
	Surfaces       []string `json:"surfaces"`
	TechnicalPlan  []string `json:"technicalPlan"`
	DataFlow       []string `json:"dataFlow"`
	MVPScope       []string `json:"mvpScope"`
	Risks          []string `json:"risks"`
	Assumptions    []string `json:"assumptions"`
	NextPrompt     string   `json:"nextPrompt"`
}

type resolvedConversationImport struct {
	SourceLabel    string
	SourceURL      string
	FetchedURL     string
	DetectedTitle  string
	SuggestedName  string
	NormalizedText string
}

var runConversationImportGenerator = RunAIGenerator
var conversationImportHTTPClient = &http.Client{Timeout: 20 * time.Second}

var (
	conversationURLRE = regexp.MustCompile(`https?://[^\s<>"')]+`)
	htmlTitleRE       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlScriptStyleRE = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	htmlTagRE         = regexp.MustCompile(`(?s)<[^>]+>`)
	whitespaceRE      = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLinesRE      = regexp.MustCompile(`\n{3,}`)
)

func AnalyzeConversationImport(req ConversationImportRequest) (*ConversationImportResult, error) {
	resolved, err := resolveConversationImportInput(req)
	if err != nil {
		return nil, err
	}
	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	body, err := runConversationImportGenerator(AIGeneratorSpec{
		Runner:  req.Runner,
		WorkDir: workDir,
		Prompt:  buildConversationImportAIPrompt(resolved),
		Timeout: 6 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("conversation import analysis: %w", err)
	}
	var llm conversationImportLLMResult
	if err := json.Unmarshal([]byte(extractJSONObject(body)), &llm); err != nil {
		return nil, fmt.Errorf("conversation import json: %w", err)
	}
	out := &ConversationImportResult{
		SourceLabel:    resolved.SourceLabel,
		SourceURL:      resolved.SourceURL,
		FetchedURL:     resolved.FetchedURL,
		DetectedTitle:  firstNonEmptyImport(resolved.DetectedTitle, llm.SuggestedName),
		SuggestedName:  firstNonEmptyImport(llm.SuggestedName, resolved.SuggestedName),
		NormalizedText: resolved.NormalizedText,
		ProductGoal:    strings.TrimSpace(llm.ProductGoal),
		UserProblem:    strings.TrimSpace(llm.UserProblem),
		Summary:        strings.TrimSpace(llm.Summary),
		ResearchTopics: compactStrings(llm.ResearchTopics),
		Surfaces:       compactStrings(llm.Surfaces),
		TechnicalPlan:  compactStrings(llm.TechnicalPlan),
		DataFlow:       compactStrings(llm.DataFlow),
		MVPScope:       compactStrings(llm.MVPScope),
		Risks:          compactStrings(llm.Risks),
		Assumptions:    compactStrings(llm.Assumptions),
		NextPrompt:     strings.TrimSpace(llm.NextPrompt),
	}
	out.GeneratedPrompt = buildConversationImportGeneratedPrompt(out)
	return out, nil
}

func resolveConversationImportInput(req ConversationImportRequest) (*resolvedConversationImport, error) {
	rawContent := strings.TrimSpace(req.Content)
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		rawURL = firstURL(rawContent)
	}
	if rawURL == rawContent {
		rawContent = ""
	}
	var fetchedTitle, fetchedText, fetchedURL string
	if rawURL != "" {
		text, title, finalURL, err := fetchConversationImportURL(rawURL)
		if err != nil {
			return nil, err
		}
		fetchedTitle = title
		fetchedText = text
		fetchedURL = finalURL
	}
	title := firstNonEmptyImport(strings.TrimSpace(req.Title), fetchedTitle, detectConversationTitle(rawContent), detectConversationTitle(fetchedText))
	parts := make([]string, 0, 3)
	if title != "" {
		parts = append(parts, "Detected title: "+title)
	}
	if fetchedText != "" {
		parts = append(parts, "Fetched share or source page:\n"+fetchedText)
	}
	if rawContent != "" {
		parts = append(parts, "Pasted conversation or notes:\n"+normalizeConversationText(rawContent))
	}
	combined := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if combined == "" {
		return nil, fmt.Errorf("conversation import requires a share URL or pasted content")
	}
	return &resolvedConversationImport{
		SourceLabel:    detectConversationSourceLabel(rawURL),
		SourceURL:      rawURL,
		FetchedURL:     fetchedURL,
		DetectedTitle:  title,
		SuggestedName:  suggestConversationName(title),
		NormalizedText: truncateConversationText(combined, 24000),
	}, nil
}

func buildConversationImportAIPrompt(resolved *resolvedConversationImport) string {
	return fmt.Sprintf(`You are turning an imported user conversation into a concrete technical plan for Yaver.

The original user may be non-technical. Your first job is to understand their real product goal in plain language, then fill in the technical path needed to build it.

If the imported thread references existing products, APIs, platforms, share links, app-store behavior, MCP flows, or external services, treat them as research targets. If your runner can browse the internet or check docs, do that first. If not, infer the smallest practical path and list explicit assumptions.

Return ONLY one JSON object with this exact shape:
{
  "suggestedName": "short project name",
  "productGoal": "plain English product goal",
  "userProblem": "what user is really trying to solve",
  "summary": "one paragraph implementation-oriented summary",
  "researchTopics": ["what should be researched or verified first"],
  "surfaces": ["mobile app", "web dashboard", "mcp console"],
  "technicalPlan": ["step 1", "step 2"],
  "dataFlow": ["input -> parser -> storage -> ui"],
  "mvpScope": ["first shippable milestone items"],
  "risks": ["important risk or unknown"],
  "assumptions": ["assumption made due to ambiguity"],
  "nextPrompt": "a strong next coding prompt for an autonomous coding loop"
}

Rules:
- Be concrete and implementation-oriented, not generic.
- Prefer a mobile-first full-stack plan when the thread points that way.
- Mention MCP or console surfaces when the imported thread asks for them.
- The technicalPlan should describe what must exist in code and infra.
- The mvpScope should be the first realistic version to ship.
- Keep every list compact and high-signal.

Imported material:
%s
`, resolved.NormalizedText)
}

func buildConversationImportGeneratedPrompt(plan *ConversationImportResult) string {
	sections := []string{
		"Use this analyzed conversation import as the source of truth for the initial implementation.",
	}
	if plan.SourceLabel != "" || plan.SourceURL != "" {
		line := plan.SourceLabel
		if line == "" {
			line = "Imported conversation"
		}
		if plan.SourceURL != "" {
			line += " (" + plan.SourceURL + ")"
		}
		sections = append(sections, "Source:\n"+line)
	}
	if plan.ProductGoal != "" {
		sections = append(sections, "Product goal:\n"+plan.ProductGoal)
	}
	if plan.UserProblem != "" {
		sections = append(sections, "User problem:\n"+plan.UserProblem)
	}
	if plan.Summary != "" {
		sections = append(sections, "Implementation summary:\n"+plan.Summary)
	}
	if len(plan.ResearchTopics) > 0 {
		sections = append(sections, "Research first:\n"+dashList(plan.ResearchTopics))
	}
	if len(plan.Surfaces) > 0 {
		sections = append(sections, "Required surfaces:\n"+dashList(plan.Surfaces))
	}
	if len(plan.TechnicalPlan) > 0 {
		sections = append(sections, "Technical plan:\n"+dashList(plan.TechnicalPlan))
	}
	if len(plan.DataFlow) > 0 {
		sections = append(sections, "Data flow:\n"+dashList(plan.DataFlow))
	}
	if len(plan.MVPScope) > 0 {
		sections = append(sections, "First shippable MVP:\n"+dashList(plan.MVPScope))
	}
	if len(plan.Risks) > 0 {
		sections = append(sections, "Risks and unknowns:\n"+dashList(plan.Risks))
	}
	if len(plan.Assumptions) > 0 {
		sections = append(sections, "Assumptions made:\n"+dashList(plan.Assumptions))
	}
	if plan.NextPrompt != "" {
		sections = append(sections, "Recommended next coding prompt:\n"+plan.NextPrompt)
	}
	return strings.Join(sections, "\n\n")
}

func fetchConversationImportURL(raw string) (string, string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", fmt.Errorf("invalid conversation import URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", "", fmt.Errorf("unsupported URL scheme")
	}
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "YaverConversationImport/1.0")
	res, err := conversationImportHTTPClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("fetch import URL: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", "", "", fmt.Errorf("fetch import URL: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", "", "", fmt.Errorf("read import URL: %w", err)
	}
	title := ""
	if match := htmlTitleRE.FindSubmatch(body); len(match) > 1 {
		title = normalizeConversationText(html.UnescapeString(string(match[1])))
	}
	text := normalizeFetchedDocument(string(body), res.Header.Get("Content-Type"))
	return text, title, res.Request.URL.String(), nil
}

func normalizeFetchedDocument(body, contentType string) string {
	lower := strings.ToLower(contentType)
	if strings.Contains(lower, "html") || strings.Contains(strings.ToLower(body), "<html") {
		body = htmlScriptStyleRE.ReplaceAllString(body, " ")
		body = strings.NewReplacer(
			"</p>", "\n",
			"<br>", "\n",
			"<br/>", "\n",
			"<br />", "\n",
			"</div>", "\n",
			"</li>", "\n",
			"</section>", "\n",
			"</article>", "\n",
			"</h1>", "\n",
			"</h2>", "\n",
			"</h3>", "\n",
		).Replace(body)
		body = htmlTagRE.ReplaceAllString(body, " ")
		body = html.UnescapeString(body)
	}
	return truncateConversationText(normalizeConversationText(body), 18000)
}

func normalizeConversationText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = whitespaceRE.ReplaceAllString(line, " ")
		out = append(out, line)
	}
	text = strings.Join(out, "\n")
	text = blankLinesRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

func truncateConversationText(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "\n\n[truncated]"
}

func detectConversationSourceLabel(rawURL string) string {
	if rawURL == "" {
		return "Pasted conversation"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "Imported URL"
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(host, "claude.ai"):
		return "Claude share"
	case strings.Contains(host, "chatgpt.com"), strings.Contains(host, "chat.openai.com"):
		return "ChatGPT share"
	case strings.Contains(host, "github.com"):
		return "GitHub link"
	default:
		return host
	}
}

func detectConversationTitle(text string) string {
	for _, line := range strings.Split(normalizeConversationText(text), "\n") {
		if line == "" || len(line) < 5 || len(line) > 96 {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "shared by") || strings.HasPrefix(lower, "this is a copy") || strings.HasPrefix(lower, "tip:") || strings.HasPrefix(lower, "model:") || strings.HasPrefix(lower, "directory:") {
			continue
		}
		return line
	}
	return ""
}

func suggestConversationName(title string) string {
	title = normalizeConversationText(title)
	if title == "" {
		return ""
	}
	title = regexp.MustCompile(`[^\p{L}\p{N}\s-]+`).ReplaceAllString(title, " ")
	parts := strings.Fields(title)
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 4 {
		parts = parts[:4]
	}
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, " ")
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func dashList(items []string) string {
	lines := make([]string, 0, len(items))
	for _, item := range compactStrings(items) {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func firstNonEmptyImport(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstURL(text string) string {
	return conversationURLRE.FindString(strings.TrimSpace(text))
}
