/**
 * Local P2P task caching layer using AsyncStorage.
 *
 * Provides offline-first access to task lists and output so the mobile
 * app can display data even when the desktop agent is unreachable.
 *
 * Requires: @react-native-async-storage/async-storage
 *   npx expo install @react-native-async-storage/async-storage
 */

import AsyncStorage from "@react-native-async-storage/async-storage";
import type { Task } from "./quic";

const KEYS = {
  TASK_LIST: "@yaver/task_list",
  TASK_OUTPUT_PREFIX: "@yaver/task_output/",
  DELETED_TASKS: "@yaver/deleted_tasks",
} as const;

/** Persist the current task list to local storage. */
export async function cacheTaskList(tasks: Task[]): Promise<void> {
  try {
    await AsyncStorage.setItem(KEYS.TASK_LIST, JSON.stringify(tasks));
  } catch {
    // Storage write failures are non-fatal — the data is still in memory.
  }
}

/** Load the cached task list. Returns an empty array when nothing is cached. */
export async function getCachedTaskList(): Promise<Task[]> {
  try {
    const raw = await AsyncStorage.getItem(KEYS.TASK_LIST);
    if (!raw) return [];
    return JSON.parse(raw) as Task[];
  } catch {
    return [];
  }
}

/** Append output lines for a single task to local storage. */
export async function cacheTaskOutput(
  taskId: string,
  output: string[]
): Promise<void> {
  try {
    await AsyncStorage.setItem(
      KEYS.TASK_OUTPUT_PREFIX + taskId,
      JSON.stringify(output)
    );
  } catch {
    // Non-fatal.
  }
}

/** Retrieve cached output for a task. */
export async function getCachedTaskOutput(
  taskId: string
): Promise<string[]> {
  try {
    const raw = await AsyncStorage.getItem(KEYS.TASK_OUTPUT_PREFIX + taskId);
    if (!raw) return [];
    return JSON.parse(raw) as string[];
  } catch {
    return [];
  }
}

/** Mark a task as deleted so it won't reappear after refresh/re-login. */
export async function markTaskDeleted(taskId: string): Promise<void> {
  try {
    const ids = await getDeletedTaskIds();
    ids.add(taskId);
    await AsyncStorage.setItem(KEYS.DELETED_TASKS, JSON.stringify([...ids]));
  } catch {}
}

/** Get the set of deleted task IDs. */
export async function getDeletedTaskIds(): Promise<Set<string>> {
  try {
    const raw = await AsyncStorage.getItem(KEYS.DELETED_TASKS);
    if (!raw) return new Set();
    return new Set(JSON.parse(raw) as string[]);
  } catch {
    return new Set();
  }
}

/** Remove task cache but preserve user-scoped settings (relays, tunnels, etc). */
export async function clearCache(): Promise<void> {
  try {
    const allKeys = await AsyncStorage.getAllKeys();
    // Only remove task cache keys and legacy global keys
    // Preserve: @yaver/u/{userId}/* (user-scoped settings), debug_logs (global)
    const toRemove = allKeys.filter((k) => {
      if (!k.startsWith("@yaver/")) return false;
      if (k.startsWith("@yaver/u/")) return false;         // user-scoped settings
      if (k === "@yaver/debug_logs_enabled") return false;  // global pref
      return true;
    });
    if (toRemove.length > 0) {
      await AsyncStorage.multiRemove(toRemove);
    }
  } catch {
    // Non-fatal.
  }
}

// ── Todo storage ─────────────────────────────────────────────────────

export interface TodoProject {
  id: string;
  name: string;
  createdAt: number;
}

export interface Todo {
  id: string;
  projectId: string;
  title: string;
  notes?: string;
  done: boolean;
  createdAt: number;
  agentStatus?: "pending" | "implementing" | "done" | "failed";
  taskId?: string;
  agentItemId?: string;
}

const TODO_KEYS = {
  PROJECTS: "@yaver/todo_projects",
  TODOS: "@yaver/todos",
} as const;

export async function getTodoProjects(): Promise<TodoProject[]> {
  try {
    const raw = await AsyncStorage.getItem(TODO_KEYS.PROJECTS);
    return raw ? JSON.parse(raw) : [];
  } catch { return []; }
}

export async function saveTodoProjects(projects: TodoProject[]): Promise<void> {
  try { await AsyncStorage.setItem(TODO_KEYS.PROJECTS, JSON.stringify(projects)); } catch {}
}

export async function getTodos(): Promise<Todo[]> {
  try {
    const raw = await AsyncStorage.getItem(TODO_KEYS.TODOS);
    return raw ? JSON.parse(raw) : [];
  } catch { return []; }
}

export async function saveTodos(todos: Todo[]): Promise<void> {
  try { await AsyncStorage.setItem(TODO_KEYS.TODOS, JSON.stringify(todos)); } catch {}
}
