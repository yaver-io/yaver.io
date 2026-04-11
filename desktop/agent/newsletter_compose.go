package main

// newsletter_compose.go — compose a newsletter draft from the
// dev's own git activity for a given window.
//
// The solo dev's newsletter is almost always "here's what I
// shipped this week" — this module reads that directly out of
// git + gh + glab and assembles a draft that can be reviewed
// from the mobile app and handed off to the existing
// newsletter broadcaster. AI polish is optional: if
// `execute=true` is set the prompt is piped through the
// configured runner (same path as mail draft), otherwise we
// return the raw composed draft + the prompt so the dev can
// iterate.
//
// Sources:
//
//   - git log (last N days) for the selected repo — commits,
//     authors, short messages
//   - gh pr list / gh issue list (if gh is installed + the
//     repo is a GitHub clone) for merged/closed PRs and issues
//   - glab mr list / glab issue list (if glab is installed)
//
// Everything runs against the dev's own local repo clone. No
// API tokens from the mobile app — the agent already has the
// gh / glab CLIs wired through its user session.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// GitActivity is the normalised shape fed into the draft
// composer. It maps 1:1 from what the templating logic walks
// so "more sources" (linear, jira, …) just append into the
// same lists.
type GitActivity struct {
	Repo        string           `json:"repo"`
	SinceMs     int64            `json:"sinceMs"`
	UntilMs     int64            `json:"untilMs"`
	Commits     []CommitEntry    `json:"commits"`
	PRs         []PREntry        `json:"prs,omitempty"`
	Issues      []IssueEntry     `json:"issues,omitempty"`
	Highlights  []string         `json:"highlights,omitempty"`
}

type CommitEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
}

type PREntry struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author string `json:"author"`
	State  string `json:"state"`
	URL    string `json:"url"`
}

type IssueEntry struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	URL    string `json:"url"`
}

// ComposeNewsletterOptions drives what the composer collects.
type ComposeNewsletterOptions struct {
	Repo         string `json:"repo"`                   // project path (from /files/roots) or absolute
	SinceDays    int    `json:"sinceDays,omitempty"`    // default 7
	IncludePRs   bool   `json:"includePrs,omitempty"`   // gh/glab pr list
	IncludeIssues bool  `json:"includeIssues,omitempty"`
	Subject      string `json:"subject,omitempty"`      // optional override
	Instructions string `json:"instructions,omitempty"` // AI tone hint
	Execute      bool   `json:"execute,omitempty"`      // run inline
	Runner       string `json:"runner,omitempty"`
	SaveDraft    bool   `json:"saveDraft,omitempty"`    // persist as newsletter campaign
}

// CollectGitActivity shells out to git/gh/glab and returns the
// raw activity struct. Missing CLIs degrade gracefully — a repo
// that isn't a GitHub clone still gets commits, just no PR list.
func CollectGitActivity(opts ComposeNewsletterOptions) (*GitActivity, error) {
	if opts.Repo == "" {
		return nil, fmt.Errorf("repo path required")
	}
	if opts.SinceDays <= 0 {
		opts.SinceDays = 7
	}
	since := time.Now().AddDate(0, 0, -opts.SinceDays)
	sinceArg := since.Format("2006-01-02")

	act := &GitActivity{
		Repo:    opts.Repo,
		SinceMs: since.UnixMilli(),
		UntilMs: time.Now().UnixMilli(),
	}

	// Commits via git log. Format: hash\tauthor\tsubject\tdate.
	out, err := exec.Command("git", "-C", opts.Repo,
		"log", "--since="+sinceArg,
		"--pretty=format:%h%x09%an%x09%s%x09%ai",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git log: %v — %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			continue
		}
		act.Commits = append(act.Commits, CommitEntry{
			Hash: parts[0], Author: parts[1], Subject: parts[2], Date: parts[3],
		})
	}

	// GitHub PRs via gh
	if opts.IncludePRs {
		if _, err := exec.LookPath("gh"); err == nil {
			ghOut, err := exec.Command("gh", "-R", opts.Repo, "pr", "list",
				"--state", "all",
				"--search", "merged:>"+sinceArg,
				"--json", "number,title,author,state,url",
				"--limit", "30",
			).CombinedOutput()
			if err == nil {
				var prs []struct {
					Number int
					Title  string
					Author struct{ Login string }
					State  string
					URL    string
				}
				if err := json.Unmarshal(ghOut, &prs); err == nil {
					for _, p := range prs {
						act.PRs = append(act.PRs, PREntry{
							Number: p.Number, Title: p.Title,
							Author: p.Author.Login, State: p.State, URL: p.URL,
						})
					}
				}
			}
		} else if _, err := exec.LookPath("glab"); err == nil {
			glabOut, err := exec.Command("glab", "mr", "list",
				"--state", "merged",
				"--output", "json",
			).CombinedOutput()
			if err == nil {
				var mrs []struct {
					Iid    int    `json:"iid"`
					Title  string `json:"title"`
					Author struct{ Username string } `json:"author"`
					State  string `json:"state"`
					WebURL string `json:"web_url"`
				}
				if err := json.Unmarshal(glabOut, &mrs); err == nil {
					for _, m := range mrs {
						act.PRs = append(act.PRs, PREntry{
							Number: m.Iid, Title: m.Title,
							Author: m.Author.Username, State: m.State, URL: m.WebURL,
						})
					}
				}
			}
		}
	}

	// Issues via gh/glab.
	if opts.IncludeIssues {
		if _, err := exec.LookPath("gh"); err == nil {
			ghOut, err := exec.Command("gh", "-R", opts.Repo, "issue", "list",
				"--state", "closed",
				"--search", "closed:>"+sinceArg,
				"--json", "number,title,state,url",
				"--limit", "30",
			).CombinedOutput()
			if err == nil {
				var issues []struct {
					Number int
					Title  string
					State  string
					URL    string
				}
				if err := json.Unmarshal(ghOut, &issues); err == nil {
					for _, i := range issues {
						act.Issues = append(act.Issues, IssueEntry{
							Number: i.Number, Title: i.Title,
							State: i.State, URL: i.URL,
						})
					}
				}
			}
		}
	}
	act.Highlights = pickHighlights(act)
	return act, nil
}

// pickHighlights picks the top ~5 commits that look like user-
// facing changes — features, fixes, perf. Filters chore/docs/
// refactor out so the newsletter reads like a changelog, not a
// stream of CI bumps.
func pickHighlights(act *GitActivity) []string {
	wanted := []string{"feat:", "fix:", "perf:", "feature:", "add ", "ship "}
	skip := []string{"chore:", "docs:", "refactor:", "test:", "ci:", "style:"}
	out := make([]string, 0, 5)
	for _, c := range act.Commits {
		lower := strings.ToLower(c.Subject)
		for _, s := range skip {
			if strings.HasPrefix(lower, s) {
				goto next
			}
		}
		for _, w := range wanted {
			if strings.Contains(lower, w) {
				out = append(out, c.Subject)
				break
			}
		}
		if len(out) >= 5 {
			break
		}
	next:
	}
	// Fallback: if nothing matched, take the 5 most recent.
	if len(out) == 0 && len(act.Commits) > 0 {
		n := 5
		if n > len(act.Commits) {
			n = len(act.Commits)
		}
		for i := 0; i < n; i++ {
			out = append(out, act.Commits[i].Subject)
		}
	}
	return out
}

// BuildNewsletterDraft turns activity into a plain-text draft
// the dev can iterate on. Deliberately simple so it reads well
// even without AI polish.
func BuildNewsletterDraft(act *GitActivity, subject string) (string, string) {
	if subject == "" {
		subject = fmt.Sprintf("What shipped (%s)", time.Now().Format("Jan 2"))
	}
	var b strings.Builder
	b.WriteString("Hey everyone,\n\n")
	b.WriteString(fmt.Sprintf("Here's what shipped in %s over the last week:\n\n", act.Repo))
	if len(act.Highlights) > 0 {
		for _, h := range act.Highlights {
			b.WriteString("• " + h + "\n")
		}
		b.WriteString("\n")
	}
	if len(act.PRs) > 0 {
		b.WriteString("Merged pull requests:\n")
		for _, p := range act.PRs {
			b.WriteString(fmt.Sprintf("  - #%d %s (%s)\n", p.Number, p.Title, p.Author))
		}
		b.WriteString("\n")
	}
	if len(act.Issues) > 0 {
		b.WriteString("Closed issues:\n")
		for _, i := range act.Issues {
			b.WriteString(fmt.Sprintf("  - #%d %s\n", i.Number, i.Title))
		}
		b.WriteString("\n")
	}
	b.WriteString("Thanks for following along.\n")
	return subject, b.String()
}

// BuildComposePrompt assembles the prompt that gets fed to the
// runner when execute=true. Same pattern as mail draft: raw
// activity + instruction → AI rewrite in the dev's voice.
func BuildComposePrompt(act *GitActivity, existingDraft, instructions string) string {
	var b strings.Builder
	b.WriteString("You are writing a solo developer's weekly newsletter.\n")
	b.WriteString("Keep it conversational, in first person, under 300 words.\n")
	if instructions != "" {
		b.WriteString("Tone notes: " + instructions + "\n")
	}
	b.WriteString("\nUse this raw activity as the source material:\n\n")
	out, _ := json.MarshalIndent(act, "", "  ")
	b.Write(out)
	b.WriteString("\n\nHere is a skeleton draft to rewrite (don't just reformat — add a narrative):\n\n")
	b.WriteString(existingDraft)
	b.WriteString("\n\nReturn only the finished newsletter body. No preamble.\n")
	return b.String()
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleNewsletterCompose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var opts ComposeNewsletterOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	act, err := CollectGitActivity(opts)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	subject, draft := BuildNewsletterDraft(act, opts.Subject)
	prompt := BuildComposePrompt(act, draft, opts.Instructions)
	result := map[string]interface{}{
		"ok":       true,
		"subject":  subject,
		"draft":    draft,
		"prompt":   prompt,
		"activity": act,
	}
	if opts.Execute {
		polished, runErr := runMailDraftInline(opts.Runner, prompt)
		if runErr == nil && polished != "" {
			draft = polished
			result["draft"] = polished
		} else if runErr != nil {
			result["runnerError"] = runErr.Error()
		}
	}
	if opts.SaveDraft {
		camp := Campaign{
			ID:        randomFormID(),
			Subject:   subject,
			Body:      draft,
			Status:    "draft",
			CreatedAt: time.Now().UTC(),
		}
		camps := append(loadCampaigns(), camp)
		_ = saveCampaigns(camps)
		result["campaignId"] = camp.ID
	}
	jsonReply(w, http.StatusOK, result)
}
