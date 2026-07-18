// taskPlacementClassifier.ts — pure, privacy-safe project/task classifier.
//
// Inputs are deliberately coarse metadata only: task kind, basename slug, stack
// labels, and counters. No prompts, paths, package names, file contents, logs,
// branch names, or secrets belong here.

export type PlacementResourceClass = "phone" | "relay-source" | "standard" | "heavy" | "build";
export type PlacementTaskKind = "vibe" | "build" | "deploy" | "test" | "source" | "autorun" | "unknown";

export type ProjectClassificationInput = {
  kind: PlacementTaskKind;
  projectSlug?: string;
  stack?: string;
  appCount?: number;
  repoSizeMb?: number;
  fileCount?: number;
  hasNativeMobile?: boolean;
  hasDocker?: boolean;
};

export type ProjectClassification = {
  resourceClass: PlacementResourceClass;
  largeMonorepo: boolean;
  largeProject: boolean;
  reason: string;
};

const RESOURCE_ORDER: PlacementResourceClass[] = ["phone", "relay-source", "standard", "heavy", "build"];

function clean(value: string | undefined): string {
  return String(value || "").trim().toLowerCase();
}

function containsAny(text: string, words: RegExp[]): boolean {
  return words.some((word) => word.test(text));
}

export function strongestResourceClass(
  a: PlacementResourceClass,
  b: PlacementResourceClass,
): PlacementResourceClass {
  return RESOURCE_ORDER[Math.max(RESOURCE_ORDER.indexOf(a), RESOURCE_ORDER.indexOf(b))] ?? b;
}

export function classifyProjectForPlacement(input: ProjectClassificationInput): ProjectClassification {
  const slug = clean(input.projectSlug);
  const stack = clean(input.stack);
  const haystack = `${slug} ${stack}`;
  const appCount = input.appCount ?? 0;
  const repoSizeMb = input.repoSizeMb ?? 0;
  const fileCount = input.fileCount ?? 0;
  const native = input.hasNativeMobile === true ||
    containsAny(haystack, [/\breact[- ]?native\b/, /\bexpo\b/, /\bios\b/, /\bandroid\b/, /\bhermes\b/, /\bapk\b/, /\bipa\b/]);
  const docker = input.hasDocker === true || containsAny(haystack, [/\bdocker\b/, /\bcompose\b/, /\bcontainer\b/]);
  const monorepo = containsAny(haystack, [
    /\bmonorepo\b/,
    /\bworkspace\b/,
    /\bpnpm\b/,
    /\bturborepo\b/,
    /\bnx\b/,
    /\byarn workspaces?\b/,
  ]);
  const largeProject = fileCount > 40_000 || repoSizeMb > 2_000 || appCount >= 2;
  const hugeProject = fileCount > 150_000 || repoSizeMb > 8_000;
  const largeMonorepo =
    (native && monorepo && (largeProject || appCount >= 2)) ||
    (monorepo && hugeProject);

  if (input.kind === "deploy" || input.kind === "build") {
    return {
      resourceClass: "build",
      largeMonorepo,
      largeProject: largeProject || hugeProject || largeMonorepo,
      reason: input.kind === "deploy"
        ? "deploy work needs native/build capacity"
        : "build work needs native/build capacity",
    };
  }
  if (largeMonorepo) {
    return {
      resourceClass: "build",
      largeMonorepo: true,
      largeProject: true,
      reason: "large/native monorepo needs 32GB build capacity",
    };
  }
  if (hugeProject) {
    return {
      resourceClass: "build",
      largeMonorepo: false,
      largeProject: true,
      reason: "very large project metadata needs 32GB build capacity",
    };
  }
  if (native || docker || largeProject) {
    return {
      resourceClass: "heavy",
      largeMonorepo: false,
      largeProject,
      reason: native
        ? "native mobile project needs 16GB workspace capacity"
        : docker
          ? "Docker project needs 16GB workspace capacity"
          : "multi-app or medium-large project needs 16GB workspace capacity",
    };
  }
  if (input.kind === "source" || input.kind === "vibe") {
    return {
      resourceClass: "relay-source",
      largeMonorepo: false,
      largeProject: false,
      reason: "source-only/vibe task can start on relay",
    };
  }
  return {
    resourceClass: "standard",
    largeMonorepo: false,
    largeProject: false,
    reason: "default single-app task fits standard workspace capacity",
  };
}
