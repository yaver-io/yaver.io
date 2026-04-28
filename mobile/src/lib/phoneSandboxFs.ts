// phoneSandboxFs.ts — the small filesystem adapter the source store
// uses. Defines just the SandboxFsAdapter interface here; production
// RN gets the expo-file-system-backed adapter from
// phoneSandboxFsExpo.ts. Splitting them lets phoneSandboxSource.ts
// run unchanged in mobile-headless tests with an in-memory adapter,
// and avoids dragging expo-file-system into Bun's resolver when we
// only need types.

/** Result of a stat / getInfoAsync call. */
export interface SandboxFsInfo {
  exists: boolean;
  isDirectory: boolean;
  size: number;
  /** Unix epoch seconds, or 0 when the underlying FS doesn't expose mtime. */
  modificationTime: number;
}

/** The filesystem surface phoneSandboxSource needs. Each method
 *  takes a URI string ending with no trailing slash for files, and
 *  with a trailing slash (or no requirement either way) for dirs.
 *  Implementations MUST treat URIs opaquely — the source store
 *  builds them by string concatenation with the documentDirectory. */
export interface SandboxFsAdapter {
  documentDirectory: string;
  getInfo(uri: string): Promise<SandboxFsInfo>;
  readText(uri: string): Promise<string>;
  writeText(uri: string, content: string): Promise<void>;
  remove(uri: string, opts: { idempotent: boolean }): Promise<void>;
  mkdirp(uri: string): Promise<void>;
  readDir(uri: string): Promise<string[]>;
  /** Atomic rename. Implementations that can't rename should
   *  delete-target + write-final + delete-tmp themselves. */
  move(opts: { from: string; to: string }): Promise<void>;
}
