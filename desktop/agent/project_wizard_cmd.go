package main

// project_wizard_cmd.go — `yaver new` CLI.
//
// Terminal driver for the project_wizard state machine. Loops
// over questions, prints them, reads answers, runs the
// generator. The only state that lives in the CLI is the
// session ID — everything else is in project_wizard.go so the
// HTTP and MCP surfaces can drive the same flow.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// runNew dispatches between the interactive wizard and the one-shot
// --quick mode. Quick mode reads a JSON object from stdin (or the path
// given after --quick) with the same keys as the project_new_quick MCP
// tool, prefills the wizard, and generates. Used for dogfooding (and
// for the Bento demo-app scaffold).
func runNew(args []string) {
	if len(args) > 0 && args[0] == "--quick" {
		runNewQuick(args[1:])
		return
	}

	fmt.Println("Yaver New — fullstack project generator")
	fmt.Println("---------------------------------------")
	fmt.Println("Press Enter to accept defaults. Good choices for a solo dev are pre-selected.")
	fmt.Println()

	sess, q := StartWizard()
	reader := bufio.NewReader(os.Stdin)

	for q != nil && q.Kind != QDone && q.Kind != QConfirm {
		renderQuestion(q)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(answer)
		if q.Kind == QBool && answer != "" {
			answer = normalizeBool(answer)
		}
		next, err := AnswerWizard(sess.ID, q.ID, answer)
		if err != nil {
			fmt.Println("  !", err)
			continue
		}
		q = next
	}

	if q != nil && q.Kind == QConfirm {
		renderQuestion(q)
		answer, _ := reader.ReadString('\n')
		answer = normalizeBool(strings.TrimSpace(answer))
		if answer == "" {
			answer = q.Default
		}
		_, _ = AnswerWizard(sess.ID, q.ID, answer)
		if answer != "true" {
			fmt.Println("Aborted. Re-run `yaver new` any time.")
			return
		}
	}

	parentDir := ""
	if len(args) > 0 {
		parentDir = args[0]
	}
	res, err := GenerateProject(sess.ID, parentDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("✓ Generated", res.Directory)
	fmt.Printf("  %d files written\n\n", len(res.Files))
	fmt.Println("Next steps:")
	for _, s := range res.NextSteps {
		fmt.Println("  •", s)
	}
}

// runNewQuick is the non-interactive scaffold: `yaver new --quick [answers.json] [parentDir]`.
// Mirrors project_new_quick (MCP) so any path through the code ends up in one
// generator. Every question in project_wizard.go's catalog gets prefilled with
// either a value from the JSON, a typed default, or the empty string.
func runNewQuick(args []string) {
	var (
		answersPath string
		parentDir   string
	)
	for _, a := range args {
		if strings.HasSuffix(a, ".json") && answersPath == "" {
			answersPath = a
		} else {
			parentDir = a
		}
	}

	var raw []byte
	var err error
	if answersPath != "" {
		raw, err = os.ReadFile(answersPath)
	} else {
		raw, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "read answers: %v\n", err)
		os.Exit(1)
	}

	var in map[string]interface{}
	if err := json.Unmarshal(raw, &in); err != nil {
		fmt.Fprintf(os.Stderr, "parse answers JSON: %v\n", err)
		os.Exit(1)
	}

	sess, _ := StartWizard()
	toStr := func(v interface{}) string {
		switch x := v.(type) {
		case string:
			return x
		case bool:
			if x {
				return "true"
			}
			return "false"
		case float64:
			return fmt.Sprintf("%v", x)
		case nil:
			return ""
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	}
	// answerKnown sends a value through the wizard state machine. Unknown
	// questions fall back to direct-write below. Empty strings still go
	// through so the wizard's "use default" path fires for optional fields.
	answerKnown := func(k string, v interface{}) {
		s := toStr(v)
		if _, err := AnswerWizard(sess.ID, k, s); err != nil {
			// Not a real question — stash directly so servicesFor() can read it.
			if strings.Contains(err.Error(), "unknown question") {
				sess.Answers[k] = s
				return
			}
			fmt.Fprintf(os.Stderr, "answer %s=%q: %v\n", k, s, err)
		}
	}

	// Every known wizard key. Unknown keys are ignored — JSON is the
	// contract, not a spec. Order matches project_wizard.go for reviewability.
	for _, k := range []string{
		"app_name", "slug", "description", "tagline", "app_template", "supported_languages", "domain",
		"primary_color", "secondary_color", "accent_color", "surface_color", "tone",
		"include_web", "include_mobile", "include_backend", "include_landing",
		"web_framework", "web_host", "backend", "mobile_stack",
		"mobile_nav_style", "mobile_nav_count", "mobile_nav_labels",
		"design_source", "design_reference_url", "design_notes",
		"oauth_apple", "oauth_google", "oauth_microsoft", "oauth_email",
		"payments",
		"ios_bundle_id", "android_package",
		"apple_team_id", "play_service_account", "cloudflare_zone",
		"git_provider", "git_visibility", "git_org", "git_repo_name",
		"include_email", "include_cache", "include_storage",
	} {
		if v, ok := in[k]; ok {
			answerKnown(k, v)
		}
	}
	// Force every remaining unanswered question to its default so the wizard
	// reaches Done. Without this loop optional-but-never-mentioned keys like
	// apple_team_id stay pending forever.
	for {
		q := nextQuestion(sess)
		if q == nil || q.Kind == QDone {
			break
		}
		if q.ID == "confirm" {
			_, _ = AnswerWizard(sess.ID, q.ID, "true")
			continue
		}
		_, _ = AnswerWizard(sess.ID, q.ID, q.Default)
	}

	res, err := GenerateProject(sess.ID, parentDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
}

func renderQuestion(q *WizardQuestion) {
	fmt.Println()
	fmt.Println(q.Prompt)
	if q.Help != "" {
		fmt.Println("  ", q.Help)
	}
	if q.Kind == QChoice && len(q.Choices) > 0 {
		fmt.Println("   choices:", strings.Join(q.Choices, ", "))
	}
	if q.Default != "" {
		fmt.Printf("> [%s] ", q.Default)
	} else {
		fmt.Print("> ")
	}
}

func normalizeBool(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1", "on":
		return "true"
	case "n", "no", "false", "0", "off":
		return "false"
	}
	return s
}
