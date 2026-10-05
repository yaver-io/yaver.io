export type StudioProjectRow = {
  name: string;
  path: string;
  framework?: string;
  frameworks?: string[];
  surfaces?: string[];
  role?: string;
  monorepoRoot?: string;
  monorepoApp?: string;
  isRepoRoot?: boolean;
};

export type StudioRepoRow = { name: string; path: string };

const INTERNAL_PATH_PARTS = new Set([
  "test", "tests", "__tests__", "fixture", "fixtures", "demo", "demos",
  "example", "examples", "e2e", "benchmark", "benchmarks", "node_modules",
  "build", "dist", "sdk", "scripts", ".git",
]);

const UI_FRAMEWORKS = new Set([
  "expo", "react-native", "react-native-expo", "flutter", "next", "nextjs",
  "vite", "react", "swift", "kotlin", "unity", "electron", "tauri", "uwp",
]);

const UI_SURFACES = new Set([
  "web", "mobile", "desktop", "ios", "android", "tv", "watch", "wear",
  "vision", "car", "xbox", "playstation",
]);

function normalized(path: string): string {
  return String(path || "").replace(/\\/g, "/").replace(/\/+$/, "");
}

function pathParts(path: string): string[] {
  return normalized(path).toLowerCase().split("/").filter(Boolean);
}

export function isStudioRunnableProject(project: StudioProjectRow): boolean {
  if (!project.path || project.isRepoRoot) return false;
  const parts = pathParts(project.path);
  if (parts.some((part) => INTERNAL_PATH_PARTS.has(part))) return false;

  const frameworks = [project.framework, ...(project.frameworks ?? [])]
    .filter(Boolean)
    .map((value) => String(value).toLowerCase());
  const surfaces = (project.surfaces ?? []).map((value) => value.toLowerCase());
  const role = String(project.role || "").toLowerCase();
  if (frameworks.some((framework) => UI_FRAMEWORKS.has(framework))) return true;
  if (surfaces.some((surface) => UI_SURFACES.has(surface))) return true;
  if (["frontend", "web", "mobile", "desktop"].includes(role)) return true;

  // Desktop shells are often declared as node projects. Keep those visible
  // without turning every CLI or package in the repo into a Studio target.
  const hint = `${project.name} ${parts.slice(-2).join("/")}`.toLowerCase();
  return /(^|[\s/_-])(desktop[-_ ]?app|web[-_ ]?ui|frontend)([\s/_-]|$)/.test(hint);
}

export function projectBelongsToRepo(project: StudioProjectRow, repo: StudioRepoRow): boolean {
  const projectPath = normalized(project.path);
  const repoPath = normalized(repo.path);
  const lineage = normalized(project.monorepoRoot || "");
  if (!projectPath || !repoPath) return false;
  if (lineage) return lineage === repoPath;
  return projectPath === repoPath || projectPath.startsWith(`${repoPath}/`);
}

export function studioTargetsForRepo<T extends StudioProjectRow>(projects: readonly T[], repo: StudioRepoRow): T[] {
  const byPath = new Map<string, T>();
  for (const project of projects) {
    if (!projectBelongsToRepo(project, repo) || !isStudioRunnableProject(project)) continue;
    byPath.set(normalized(project.path), project);
  }
  return Array.from(byPath.values()).sort((a, b) => studioTargetLabel(a, repo).localeCompare(studioTargetLabel(b, repo)));
}

export function studioTargetLabel(project: StudioProjectRow, repo: StudioRepoRow): string {
  if (project.monorepoApp?.trim()) return project.monorepoApp.trim();
  const projectPath = normalized(project.path);
  const repoPath = normalized(repo.path);
  if (projectPath === repoPath) return project.name || repo.name;
  const relative = projectPath.startsWith(`${repoPath}/`) ? projectPath.slice(repoPath.length + 1) : "";
  if (relative) return relative.replace(/^apps\//, "");
  const slashName = String(project.name || "").split(" / ").pop()?.trim();
  return slashName || project.name || "App";
}
