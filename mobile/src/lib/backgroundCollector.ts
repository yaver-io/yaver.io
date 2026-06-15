// backgroundCollector.ts — periodic on-phone runner for Yaver Task Packages.
//
// Registers an expo-background-fetch task that, on the OS's schedule, runs the
// assigned package against the on-phone agent (127.0.0.1:18080) so the package's
// fetch/MCP work exits THIS phone's own residential IP. Honest about platform:
// Android WorkManager honors ~15-min minimum and survives reboot; iOS
// BGTaskScheduler is opportunistic (the OS decides timing). See
// docs/yaver-task-packages.md §4 (mobile target).

import * as BackgroundFetch from "expo-background-fetch";
import * as TaskManager from "expo-task-manager";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { getToken } from "./auth";

const TASK_NAME = "yaver.package-runner";
const BG_NAME_KEY = "yaver.packages.bg.name"; // package to run
const BG_HOST_KEY = "yaver.packages.bg.host"; // agent host (default 127.0.0.1)
const BG_LAST_KEY = "yaver.packages.bg.last"; // last run status JSON
const MIN_INTERVAL_SECS = 15 * 60;

async function runAssignedPackageOnce(): Promise<boolean> {
  const name = await AsyncStorage.getItem(BG_NAME_KEY);
  if (!name) return false;
  const host = (await AsyncStorage.getItem(BG_HOST_KEY)) || "127.0.0.1";
  const token = await getToken();
  if (!token) return false;
  try {
    const res = await fetch(`http://${host}:18080/ops`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ verb: "package_run", payload: { name }, machine: "local" }),
    });
    const data = await res.json().catch(() => ({}));
    const run = data?.initial?.run ?? {};
    await AsyncStorage.setItem(
      BG_LAST_KEY,
      JSON.stringify({ at: Date.now(), status: run?.status ?? (data?.error ? "error" : "unknown") }),
    );
    return run?.status === "ok";
  } catch {
    await AsyncStorage.setItem(BG_LAST_KEY, JSON.stringify({ at: Date.now(), status: "error" }));
    return false;
  }
}

if (!TaskManager.isTaskDefined(TASK_NAME)) {
  TaskManager.defineTask(TASK_NAME, async () => {
    const ok = await runAssignedPackageOnce();
    return ok
      ? BackgroundFetch.BackgroundFetchResult.NewData
      : BackgroundFetch.BackgroundFetchResult.NoData;
  });
}

export async function enableBackgroundRunner(packageName: string, host = "127.0.0.1"): Promise<void> {
  await AsyncStorage.setItem(BG_NAME_KEY, packageName);
  await AsyncStorage.setItem(BG_HOST_KEY, host);
  await BackgroundFetch.registerTaskAsync(TASK_NAME, {
    minimumInterval: MIN_INTERVAL_SECS,
    stopOnTerminate: false,
    startOnBoot: true,
  });
}

export async function disableBackgroundRunner(): Promise<void> {
  await AsyncStorage.removeItem(BG_NAME_KEY);
  try {
    await BackgroundFetch.unregisterTaskAsync(TASK_NAME);
  } catch {
    // not registered — fine
  }
}

export async function backgroundRunnerStatus(): Promise<{ name: string; last?: { at: number; status: string } }> {
  const name = (await AsyncStorage.getItem(BG_NAME_KEY)) || "";
  const lastRaw = await AsyncStorage.getItem(BG_LAST_KEY);
  let last: { at: number; status: string } | undefined;
  if (lastRaw) {
    try {
      last = JSON.parse(lastRaw);
    } catch {
      last = undefined;
    }
  }
  return { name, last };
}

// runAssignedPackageNow lets the UI trigger one foreground cycle for testing.
export const runAssignedPackageNow = runAssignedPackageOnce;
