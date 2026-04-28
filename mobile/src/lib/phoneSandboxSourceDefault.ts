// phoneSandboxSourceDefault.ts — production RN binding of the
// source store to expoFsAdapter. Importing this file pulls in
// expo-file-system at module load. mobile-headless tests should
// NOT import this file; they exercise createSourceStore directly
// from ./phoneSandboxSource with an in-memory adapter.

import { expoFsAdapter } from "./phoneSandboxFsExpo";
import { createSourceStore } from "./phoneSandboxSource";

const store = createSourceStore(expoFsAdapter);

export const readSourceFile = store.readSourceFile;
export const writeSourceFile = store.writeSourceFile;
export const deleteSourceFile = store.deleteSourceFile;
export const deleteSourceTree = store.deleteSourceTree;
export const listSourceFiles = store.listSourceFiles;
export const hasSource = store.hasSource;
