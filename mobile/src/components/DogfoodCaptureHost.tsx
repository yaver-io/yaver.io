/**
 * DogfoodCaptureHost — mounted once at the app root. When dogfood mode is on,
 * it listens for caught screenshots (from anywhere in the app) and pops the
 * annotate modal, then either dispatches the item to the agent (Send now) or
 * stages it in the thread (Add to batch).
 *
 * Lives at the root (not the Dogfood screen) so "screenshot anywhere → caught"
 * works regardless of which tab the user is on.
 */

import React from "react";
import { Alert } from "react-native";
import { useAuth } from "../context/AuthContext";
import { useDevice } from "../context/DeviceContext";
import { onDogfoodScreenshot, type DogfoodShot } from "../lib/dogfoodCapture";
import { snapshotBreadcrumbs, formatBreadcrumbs } from "../lib/dogfoodBreadcrumbs";
import {
  loadDogfoodConfig,
  type DogfoodConfig,
  type DogfoodMode,
} from "../lib/dogfoodConfig";
import {
  dispatchDogfoodItems,
  stageDogfoodItem,
  updateDogfoodItem,
} from "../lib/dogfoodThread";
import { DogfoodAnnotateModal, type DogfoodAnnotateResult } from "./DogfoodAnnotateModal";

interface Pending extends DogfoodShot {
  breadcrumbs?: string;
}

export function DogfoodCaptureHost() {
  const { user } = useAuth();
  const { activeDevice, connectionStatus } = useDevice();
  const [pending, setPending] = React.useState<Pending | null>(null);
  const [config, setConfig] = React.useState<DogfoodConfig | null>(null);

  React.useEffect(() => {
    void loadDogfoodConfig(user?.id).then(setConfig);
  }, [user?.id]);

  React.useEffect(() => {
    return onDogfoodScreenshot((shot) => {
      setPending({ ...shot, breadcrumbs: formatBreadcrumbs(snapshotBreadcrumbs()) || undefined });
    });
  }, []);

  const repoDir = config?.repoDir?.trim() || "";
  const connected = connectionStatus === "connected" && !!activeDevice;
  const vibeAvailable = !!repoDir && connected;
  // Auto-pick: prefer the user's last mode, but fall back to PR when Vibe
  // isn't possible (no repo dir / no connected box).
  const defaultMode: DogfoodMode = vibeAvailable ? config?.mode ?? "vibe" : "pr";

  const handleConfirm = React.useCallback(
    async (result: DogfoodAnnotateResult) => {
      const shot = pending;
      setPending(null);
      if (!shot) return;
      const item = await stageDogfoodItem({
        shotPath: shot.path,
        base64: result.base64,
        caption: result.caption,
        mode: result.mode,
        route: shot.route,
        breadcrumbs: shot.breadcrumbs,
      });

      if (!result.send) return; // staged for batch

      if (result.mode === "vibe" && !vibeAvailable) {
        await updateDogfoodItem(item.id, { status: "failed", error: "No connected box with the Yaver source." });
        Alert.alert("Can't vibe yet", "Set the repo dir in Dogfood settings and connect a box, or switch to PR mode.");
        return;
      }
      if (!activeDevice?.id) {
        await updateDogfoodItem(item.id, { status: "failed", error: "No connected box." });
        Alert.alert("No box connected", "Connect a remote box first, then resend from the Dogfood thread.");
        return;
      }
      await updateDogfoodItem(item.id, { status: "sent" });
      const res = await dispatchDogfoodItems({
        items: [{ ...item, status: "sent" }],
        mode: result.mode,
        deviceId: activeDevice.id,
        deviceName: activeDevice.name,
        repoDir,
        basePrompt: config?.prompt ?? "",
        runner: config?.runner ?? "claude-code",
      });
      if (!res.ok) {
        Alert.alert("Couldn't send", res.error || "Failed to reach the agent.");
      }
    },
    [pending, activeDevice?.id, activeDevice?.name, vibeAvailable, repoDir, config],
  );

  return (
    <DogfoodAnnotateModal
      visible={!!pending}
      imagePath={pending?.path ?? null}
      route={pending?.route}
      breadcrumbs={pending?.breadcrumbs}
      defaultMode={defaultMode}
      vibeAvailable={vibeAvailable}
      onCancel={() => setPending(null)}
      onConfirm={handleConfirm}
    />
  );
}
