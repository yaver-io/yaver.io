// projectStoreSandbox.ts — phone-sandbox-tier ProjectStore.
//
// Lives in a separate file from projectStore.ts because the sandbox
// implementation pulls in expo-sqlite (via phoneSandboxLocal.ts),
// which the path-aliased mobile-headless build cannot resolve. The
// agent-tier and pure types stay in projectStore.ts so headless
// tests can use them; this file is RN-only.
//
// What this gives the rest of the app:
//
//   const store = phoneSandboxStore;
//   await pullFromAgent(slug, { baseUrl, headers }, store);
//
// closes the missing pull-from-agent direction the design doc calls
// out. Today the phone only knows how to push; this lets it pull a
// teammate's schema change without needing the consuming app code.

import {
  deleteLocalPhoneProject,
  ensureLocalPhoneProject,
  getLocalPhoneProjectMeta,
  listLocalPhoneProjectsMeta,
} from "./phoneSandboxLocal";
import type { PhoneProject } from "./phoneProjects";
import {
  ProjectNotFoundError,
  type Project,
  type ProjectMeta,
  type ProjectStore,
  type WriteOptions,
} from "./projectStore";

function projectMetaFromPhone(p: PhoneProject): ProjectMeta {
  return {
    slug: p.slug,
    name: p.name,
    template: p.template,
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
    tier: "phone-sandbox",
  };
}

function projectFromPhone(p: PhoneProject): Project {
  return {
    slug: p.slug,
    name: p.name,
    template: p.template,
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
    schema: p.schema ?? null,
    auth: p.auth ?? null,
    seed: p.seed ?? null,
    app: p.app ?? null,
    stats: p.stats ?? null,
  };
}

function phoneFromProject(p: Project): PhoneProject {
  // PhoneProject has a `dir` field that the sandbox layer doesn't
  // actually consult — the sandbox keys everything by slug — but
  // ensureLocalPhoneProject expects the type to be present. Build a
  // minimal PhoneProject that satisfies the call site.
  const now = new Date().toISOString();
  return {
    slug: p.slug,
    name: p.name,
    template: p.template ?? "",
    dir: "",
    createdAt: p.createdAt ?? now,
    updatedAt: p.updatedAt ?? now,
    schema: p.schema ?? null,
    auth: p.auth ?? null,
    seed: p.seed ?? null,
    app: p.app ?? null,
    stats: p.stats ?? null,
  } as PhoneProject;
}

/** phoneSandboxStore is the on-device sandbox tier — backed by
 *  expo-sqlite via phoneSandboxLocal.ts. Stateless; safe to import
 *  as a singleton. */
export const phoneSandboxStore: ProjectStore = {
  async list(): Promise<ProjectMeta[]> {
    const ps = await listLocalPhoneProjectsMeta();
    return ps.map(projectMetaFromPhone);
  },

  async read(slug: string): Promise<Project> {
    const meta = await getLocalPhoneProjectMeta(slug);
    if (!meta) throw new ProjectNotFoundError(slug);
    return projectFromPhone(meta);
  },

  async write(p: Project, opts?: WriteOptions): Promise<ProjectMeta> {
    if (!p.slug) throw new Error("phoneSandboxStore.write: project.slug required");
    const existing = await getLocalPhoneProjectMeta(p.slug);
    if (existing) {
      const policy = opts?.onConflict ?? "reject";
      switch (policy) {
        case "overwrite":
          await deleteLocalPhoneProject(p.slug);
          break;
        case "rename":
          throw new Error(
            "phoneSandboxStore.write: ConflictRename not supported on the phone sandbox — pick a different slug explicitly",
          );
        case "":
        case "reject":
        default:
          throw new Error(`phone-sandbox: project already exists: ${p.slug}`);
      }
    }
    // Strip seed from the on-disk write when caller asked us to.
    // ensureLocalPhoneProject consumes seed as the initial-data set;
    // skipping it keeps the sandbox empty, which is what callers
    // typically want when pulling a fresh schema after the seed
    // already ran on a remote target.
    const phone = phoneFromProject(opts?.skipSeed ? { ...p, seed: null } : p);
    await ensureLocalPhoneProject(phone);
    return projectMetaFromPhone(phone);
  },
};
