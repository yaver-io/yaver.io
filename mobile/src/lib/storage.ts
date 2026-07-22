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
  TASK_TURNS_PREFIX: "@yaver/task_turns/",
  TASK_TURNS_INDEX: "@yaver/task_turns_index",
  DELETED_TASKS: "@yaver/deleted_tasks",
} as const;

// Bound the persisted conversation cache. A phone can accumulate thousands of
// tasks; caching every transcript forever would bloat AsyncStorage without
// bound. We keep the most-recently-opened MAX_CACHED_TURN_TASKS threads, and
// skip any single transcript larger than MAX_TURNS_BYTES (a runaway task can
// hold 100k+ output lines — that belongs in a detail fetch, not the cache).
const MAX_CACHED_TURN_TASKS = 60;
const MAX_TURNS_BYTES = 256 * 1024;

/** Pure LRU step for the turns-cache index: move `taskId` to the front and
 *  evict everything past `cap`. Split out so the bounding is unit-tested
 *  without a real AsyncStorage. Never mutates `prev`. */
export function nextTurnsCacheIndex(
  prev: string[],
  taskId: string,
  cap: number = MAX_CACHED_TURN_TASKS,
): { index: string[]; evicted: string[] } {
  const moved = [taskId, ...prev.filter((id) => id !== taskId)];
  if (moved.length <= cap) return { index: moved, evicted: [] };
  return { index: moved.slice(0, cap), evicted: moved.slice(cap) };
}

/** Cache one task's full conversation turns for instant re-open + offline view.
 *  LRU-bounded: opening/refreshing a task moves it to the front and evicts the
 *  oldest beyond the cap. Best-effort — a write failure just means the next
 *  open re-fetches from the agent. */
export async function cacheTaskTurns(
  taskId: string,
  turns: unknown[],
): Promise<void> {
  if (!taskId || !Array.isArray(turns) || turns.length === 0) return;
  try {
    const payload = JSON.stringify(turns);
    if (payload.length > MAX_TURNS_BYTES) return; // too big to cache sanely
    await AsyncStorage.setItem(KEYS.TASK_TURNS_PREFIX + taskId, payload);

    let prevIndex: string[] = [];
    try {
      const raw = await AsyncStorage.getItem(KEYS.TASK_TURNS_INDEX);
      if (raw) prevIndex = JSON.parse(raw) as string[];
    } catch { prevIndex = []; }
    const { index, evicted } = nextTurnsCacheIndex(prevIndex, taskId);
    if (evicted.length > 0) {
      await AsyncStorage.multiRemove(evicted.map((id) => KEYS.TASK_TURNS_PREFIX + id));
    }
    await AsyncStorage.setItem(KEYS.TASK_TURNS_INDEX, JSON.stringify(index));
  } catch {
    // Non-fatal — cache is an accelerator, not the source of truth.
  }
}

/** Load a task's cached turns, or null if none. Shape is validated by the
 *  caller against its ConversationTurn type. */
export async function getCachedTaskTurns(taskId: string): Promise<unknown[] | null> {
  if (!taskId) return null;
  try {
    const raw = await AsyncStorage.getItem(KEYS.TASK_TURNS_PREFIX + taskId);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

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
