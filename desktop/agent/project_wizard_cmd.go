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
	"fmt"
	"os"
	"strings"
)

func runNew(args []string) {
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
