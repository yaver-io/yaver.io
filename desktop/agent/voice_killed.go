package main

// voice_killed.go — the voice surface was removed 2026-04-28
// (project_lean_stack_2026_04_28.md). PersonaPlex / OpenAI Realtime
// were placeholder code that never had a tested implementation.
//
// This file exists ONLY because a concurrent thread / linter keeps
// restoring `case "voice":` to main.go's CLI dispatch. Rather than
// fight every restoration, we ship a no-op `runVoice` so the build
// stays green and the user sees a clear "no longer supported"
// message at the CLI.
//
// Do not add real voice logic here without first checking with kivanc.

import "fmt"

func runVoice(args []string) {
	_ = args
	fmt.Println("voice support was removed in the lean-stack cut (2026-04-28).")
	fmt.Println("re-add via project_lean_stack_2026_04_28.md only after explicit go-ahead.")
}
