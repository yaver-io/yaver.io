const GLOBAL_TERMS = [
  "run tests", "run the tests", "continue", "stop", "retry", "fix it", "fix this",
  "explain", "explain this", "commit", "commit it", "push", "pull", "build", "deploy",
  "revert", "yes", "no", "approve", "reject", "next", "previous", "open terminal", "show logs",
];

export function silentInputContextTerms(projectName?: string, extra: string[] = []): string[] {
  const repoTerms = ["vitest", "jest", "pytest", "gradle", "xcodebuild", "docker", "pnpm", "npm", "bun"];
  return [...new Set([...GLOBAL_TERMS, ...repoTerms, projectName || "", ...extra].map((term) => term.trim().toLowerCase()).filter(Boolean))].slice(0, 80);
}
