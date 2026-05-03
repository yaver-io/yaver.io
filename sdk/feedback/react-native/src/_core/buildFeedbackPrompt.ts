// buildFeedbackPrompt — shared prompt enrichment used by every Yaver
// feedback surface, in-Yaver native pane (mirrored in Swift + Kotlin)
// AND the standalone RN feedback SDK (this file). Keep all three
// implementations in lockstep — the wording is what the AI on the
// remote is conditioned to expect.
//
// The bare user text on its own loses crucial context: WHICH app the
// user is testing, WHICH screen they're looking at, and whether a
// screenshot is attached for visual reference. Without that the agent
// guesses, edits the wrong project, or asks clarifying questions
// instead of acting. The wrapper below tells the agent:
//   - this feedback comes from the in-app drawer while the user is
//     mid-test,
//   - which project the user is in (when known),
//   - that the FIRST attached image (when present) is a snapshot of
//     the current screen — open it to see what the user is pointing
//     at,
//   - that changes should be applied to that project's source +
//     saved so the user can trigger a Hermes reload to see them.
//
// Cross-reference: mobile/ios/Yaver/YaverFeedbackPane.swift's
// `buildFeedbackPrompt` and mobile/android/.../YaverFeedbackPane.kt's
// `buildFeedbackPrompt`. All three must match.

export interface BuildFeedbackPromptInput {
  userPrompt: string;
  /** Hot-Reload project name when running inside Yaver mobile, OR
   *  the host app's bundle/package name when running standalone. */
  projectName?: string;
  /** Absolute path on the host where the project lives (only known
   *  when running inside Yaver mobile via Hot Reload). */
  projectPath?: string;
  /** True when the caller has attached a screenshot of the current
   *  screen as the first image in the task's images array. */
  hasScreenshot: boolean;
}

export function buildFeedbackPrompt(input: BuildFeedbackPromptInput): string {
  const userPrompt = input.userPrompt ?? "";
  const projectName = (input.projectName ?? "").trim();
  const projectPath = (input.projectPath ?? "").trim();
  const hasScreenshot = !!input.hasScreenshot;

  const lines: string[] = [];
  lines.push("[Mobile feedback from inside Yaver]");
  lines.push(
    "The user is providing this feedback while running a mobile app inside the Yaver mobile container " +
      "and is currently looking at a specific screen of that app."
  );
  lines.push("");
  if (projectName || projectPath) {
    lines.push("App being tested:");
    if (projectName) lines.push(`  name: ${projectName}`);
    if (projectPath) lines.push(`  path: ${projectPath}`);
    lines.push("");
  }
  if (hasScreenshot) {
    lines.push(
      "A screenshot of the current screen is attached as the first image. " +
        "Open it before deciding what to change — the user is pointing at what they SEE, " +
        "not necessarily what is named most prominently in the source."
    );
    lines.push("");
  } else {
    lines.push("(The user chose not to attach a screenshot for this round.)");
    lines.push("");
  }
  if (projectName || projectPath) {
    lines.push(
      "Apply the requested change to the source of that app. Save the affected files. " +
        "The user will trigger a Hermes reload from the drawer to see the result."
    );
    lines.push("");
  } else {
    lines.push(
      "If you can identify the project from the prompt or the screenshot, apply the change there. " +
        "Otherwise ask the user briefly which project to target."
    );
    lines.push("");
  }
  lines.push("User feedback:");
  lines.push(userPrompt);
  return lines.join("\n");
}
