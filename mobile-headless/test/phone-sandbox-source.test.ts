// phone-sandbox-source.test.ts — pins the on-device source-tree
// storage contract from phoneSandboxSource.ts. Slice 1 of the
// phone-first dev stack.
//
// What's worth pinning:
//   1. Path-safety rules — every traversal/abs-path/backslash/etc.
//      MUST be rejected. This is the security boundary; a regression
//      here lets an editor hand the source store a path that
//      escapes the project root.
//   2. Round-trip — writeSourceFile → readSourceFile → identical
//      contents, even with subdirs and unicode.
//   3. Atomic write semantics — listSourceFiles must hide .tmp
//      droppings from in-flight writes; a successful write leaves
//      no .tmp behind.
//   4. listSourceFiles is recursive, sorted, and reports size +
//      modification time for files.
//   5. Sentinels (SourceFileNotFoundError, UnsafeSourcePathError)
//      survive the path through async boundaries so callers can
//      detect them with isSourceFileNotFound / instanceof.

import { afterEach, beforeEach, describe, expect, it } from "bun:test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  createSourceStore,
  isSourceFileNotFound,
  normaliseSourceRelPath,
  SourceFileNotFoundError,
  UnsafeSourcePathError,
} from "@yaver/mobile-lib/phoneSandboxSource";
import type { SandboxFsAdapter, SandboxFsInfo } from "@yaver/mobile-lib/phoneSandboxFs";

// Per-test sandbox so on-disk state stays isolated.
let scratch: string;

beforeEach(() => {
  scratch = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-source-"));
});

afterEach(() => {
  try {
    fs.rmSync(scratch, { recursive: true, force: true });
  } catch {
    // ignore
  }
});

/** Build a Node-fs-backed adapter rooted at `scratch`. URIs are
 *  plain absolute paths starting with `${scratch}/` — no file://
 *  scheme; the source store treats URIs opaquely so this works. */
function newAdapter(): SandboxFsAdapter {
  const root = scratch.endsWith("/") ? scratch : scratch + "/";
  return {
    documentDirectory: root,
    async getInfo(uri: string): Promise<SandboxFsInfo> {
      try {
        const st = fs.statSync(uri);
        return {
          exists: true,
          isDirectory: st.isDirectory(),
          size: st.size,
          modificationTime: Math.floor(st.mtimeMs / 1000),
        };
      } catch (e: any) {
        if (e?.code === "ENOENT") {
          return { exists: false, isDirectory: false, size: 0, modificationTime: 0 };
        }
        throw e;
      }
    },
    async readText(uri: string): Promise<string> {
      return fs.readFileSync(uri, "utf8");
    },
    async writeText(uri: string, content: string): Promise<void> {
      fs.mkdirSync(path.dirname(uri), { recursive: true });
      fs.writeFileSync(uri, content, "utf8");
    },
    async remove(uri: string, opts: { idempotent: boolean }): Promise<void> {
      try {
        fs.rmSync(uri, { recursive: true, force: opts.idempotent });
      } catch (e: any) {
        if (opts.idempotent && e?.code === "ENOENT") return;
        throw e;
      }
    },
    async mkdirp(uri: string): Promise<void> {
      fs.mkdirSync(uri, { recursive: true });
    },
    async readDir(uri: string): Promise<string[]> {
      try {
        return fs.readdirSync(uri);
      } catch (e: any) {
        if (e?.code === "ENOENT") return [];
        throw e;
      }
    },
    async move(opts: { from: string; to: string }): Promise<void> {
      fs.mkdirSync(path.dirname(opts.to), { recursive: true });
      fs.renameSync(opts.from, opts.to);
    },
  };
}

describe("normaliseSourceRelPath — path safety", () => {
  it("accepts plain relative paths", () => {
    expect(normaliseSourceRelPath("App.tsx")).toBe("App.tsx");
    expect(normaliseSourceRelPath("screens/Home.tsx")).toBe("screens/Home.tsx");
    expect(normaliseSourceRelPath("a/b/c/d.ts")).toBe("a/b/c/d.ts");
  });

  it("strips leading ./", () => {
    expect(normaliseSourceRelPath("./App.tsx")).toBe("App.tsx");
    expect(normaliseSourceRelPath("./screens/Home.tsx")).toBe("screens/Home.tsx");
  });

  it("rejects absolute paths", () => {
    expect(() => normaliseSourceRelPath("/etc/passwd")).toThrow(UnsafeSourcePathError);
    expect(() => normaliseSourceRelPath("/App.tsx")).toThrow(UnsafeSourcePathError);
  });

  it("rejects path traversal", () => {
    expect(() => normaliseSourceRelPath("..")).toThrow(UnsafeSourcePathError);
    expect(() => normaliseSourceRelPath("../etc/passwd")).toThrow(UnsafeSourcePathError);
    expect(() => normaliseSourceRelPath("src/../../../etc/passwd")).toThrow(UnsafeSourcePathError);
    expect(() => normaliseSourceRelPath("foo/..")).toThrow(UnsafeSourcePathError);
  });

  it("rejects backslashes (Windows-style separators)", () => {
    expect(() => normaliseSourceRelPath("foo\\bar")).toThrow(UnsafeSourcePathError);
    expect(() => normaliseSourceRelPath("..\\etc")).toThrow(UnsafeSourcePathError);
  });

  it("rejects empty and double-slash paths", () => {
    expect(() => normaliseSourceRelPath("")).toThrow(UnsafeSourcePathError);
    expect(() => normaliseSourceRelPath("a//b")).toThrow(UnsafeSourcePathError);
  });

  it("rejects NUL bytes", () => {
    expect(() => normaliseSourceRelPath("a\0b")).toThrow(UnsafeSourcePathError);
  });
});

describe("write → read round-trip", () => {
  it("preserves UTF-8 content exactly", async () => {
    const store = createSourceStore(newAdapter());
    const content = "export default function App() { return null; }\n";
    await store.writeSourceFile("todo", "App.tsx", content);
    const got = await store.readSourceFile("todo", "App.tsx");
    expect(got).toBe(content);
  });

  it("creates parent directories on demand", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("todo", "screens/auth/Login.tsx", "// hello");
    expect(await store.readSourceFile("todo", "screens/auth/Login.tsx")).toBe("// hello");
  });

  it("handles unicode and newlines without mangling", async () => {
    const store = createSourceStore(newAdapter());
    const content = "// héllo 世界 🎉\nconst x = '\u00FF';\n";
    await store.writeSourceFile("todo", "i18n.ts", content);
    expect(await store.readSourceFile("todo", "i18n.ts")).toBe(content);
  });

  it("overwriting an existing file replaces it atomically", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("todo", "App.tsx", "v1");
    await store.writeSourceFile("todo", "App.tsx", "v2");
    expect(await store.readSourceFile("todo", "App.tsx")).toBe("v2");
  });
});

describe("read errors — sentinel detection", () => {
  it("throws SourceFileNotFoundError for missing file", async () => {
    const store = createSourceStore(newAdapter());
    let caught: unknown = null;
    try {
      await store.readSourceFile("ghost", "App.tsx");
    } catch (e) {
      caught = e;
    }
    expect(caught).not.toBeNull();
    expect(isSourceFileNotFound(caught)).toBe(true);
    if (caught instanceof SourceFileNotFoundError) {
      expect(caught.slug).toBe("ghost");
      expect(caught.relPath).toBe("App.tsx");
    }
  });

  it("UnsafeSourcePathError fires before any I/O", async () => {
    const store = createSourceStore(newAdapter());
    let caught: unknown = null;
    try {
      await store.readSourceFile("todo", "../etc/passwd");
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(UnsafeSourcePathError);
  });
});

describe("listSourceFiles", () => {
  it("returns [] for an empty project", async () => {
    const store = createSourceStore(newAdapter());
    expect(await store.listSourceFiles("empty")).toEqual([]);
  });

  it("walks recursively and sorts by path", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("rec", "App.tsx", "a");
    await store.writeSourceFile("rec", "screens/Home.tsx", "b");
    await store.writeSourceFile("rec", "screens/auth/Login.tsx", "c");
    const got = await store.listSourceFiles("rec");
    const paths = got.filter((e) => !e.isDirectory).map((e) => e.path);
    // localeCompare puts "auth" before "Home" ('a' < 'H' under
    // standard collation); the contract is "stable + alphabetical",
    // not "case-sensitive ASCII".
    expect(paths).toEqual(["App.tsx", "screens/auth/Login.tsx", "screens/Home.tsx"]);
  });

  it("reports size + modifiedAt for files", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("meta", "size.txt", "hello");
    const got = await store.listSourceFiles("meta");
    const file = got.find((e) => e.path === "size.txt");
    expect(file).toBeDefined();
    if (file) {
      expect(file.size).toBe(5);
      expect(file.modifiedAt).toMatch(/^\d{4}-/);
      expect(file.isDirectory).toBe(false);
    }
  });

  it("hides .tmp files from list output (no leaks of in-flight writes)", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("clean", "real.txt", "real");
    // Simulate an in-flight atomic write by dropping a .tmp file
    // directly into the project src dir — listSourceFiles must skip
    // it.
    const srcDir = path.join(scratch, "phone-projects", "clean", "src");
    fs.writeFileSync(path.join(srcDir, "ghost.tmp"), "in-flight");

    const got = await store.listSourceFiles("clean");
    const paths = got.filter((e) => !e.isDirectory).map((e) => e.path);
    expect(paths).toEqual(["real.txt"]);
  });

  it("leaves no .tmp file behind on a successful write", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("clean", "App.tsx", "x");
    const srcDir = path.join(scratch, "phone-projects", "clean", "src");
    const entries = fs.readdirSync(srcDir);
    expect(entries.some((e) => e.endsWith(".tmp"))).toBe(false);
  });
});

describe("delete operations", () => {
  it("deleteSourceFile removes one file, leaves siblings", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("d", "a.txt", "A");
    await store.writeSourceFile("d", "b.txt", "B");
    await store.deleteSourceFile("d", "a.txt");
    const paths = (await store.listSourceFiles("d"))
      .filter((e) => !e.isDirectory)
      .map((e) => e.path);
    expect(paths).toEqual(["b.txt"]);
  });

  it("deleteSourceFile is idempotent for missing paths", async () => {
    const store = createSourceStore(newAdapter());
    await expect(store.deleteSourceFile("d", "ghost.txt")).resolves.toBeUndefined();
  });

  it("deleteSourceTree nukes the entire src tree", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("nuke", "a.txt", "A");
    await store.writeSourceFile("nuke", "deep/b.txt", "B");
    await store.deleteSourceTree("nuke");
    expect(await store.listSourceFiles("nuke")).toEqual([]);
  });
});

describe("hasSource", () => {
  it("returns false for empty project", async () => {
    const store = createSourceStore(newAdapter());
    expect(await store.hasSource("none")).toBe(false);
  });

  it("returns true after first write", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("yes", "App.tsx", "a");
    expect(await store.hasSource("yes")).toBe(true);
  });

  it("returns false after the only file is deleted", async () => {
    const store = createSourceStore(newAdapter());
    await store.writeSourceFile("toggle", "App.tsx", "a");
    await store.deleteSourceFile("toggle", "App.tsx");
    expect(await store.hasSource("toggle")).toBe(false);
  });
});

describe("slug validation", () => {
  it("rejects empty slugs at every entrypoint", async () => {
    const store = createSourceStore(newAdapter());
    await expect(store.writeSourceFile("", "App.tsx", "x")).rejects.toThrow(UnsafeSourcePathError);
    await expect(store.readSourceFile("", "App.tsx")).rejects.toThrow(UnsafeSourcePathError);
    await expect(store.listSourceFiles("")).rejects.toThrow(UnsafeSourcePathError);
  });

  it("rejects slugs with path separators", async () => {
    const store = createSourceStore(newAdapter());
    await expect(store.writeSourceFile("../etc", "App.tsx", "x")).rejects.toThrow(UnsafeSourcePathError);
    await expect(store.writeSourceFile("a/b", "App.tsx", "x")).rejects.toThrow(UnsafeSourcePathError);
  });

  it("rejects slugs with uppercase or punctuation", async () => {
    const store = createSourceStore(newAdapter());
    await expect(store.writeSourceFile("My App", "App.tsx", "x")).rejects.toThrow(UnsafeSourcePathError);
    await expect(store.writeSourceFile("MyApp", "App.tsx", "x")).rejects.toThrow(UnsafeSourcePathError);
    await expect(store.writeSourceFile("my_app", "App.tsx", "x")).rejects.toThrow(UnsafeSourcePathError);
  });
});
