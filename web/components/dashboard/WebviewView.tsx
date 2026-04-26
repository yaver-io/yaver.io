"use client";

import { useEffect, useState } from "react";
import type { Device } from "@/lib/use-devices";
import PreviewPane from "./PreviewPane";
import { WebReloadView } from "./WebReloadView";

type PreviewTarget = {
  id: string;
  name: string;
};

type WebviewMode = "mobile" | "web";

interface Props {
  connectedDevice: Device | null;
  connState: string;
  preferredMode?: WebviewMode;
  preferredProjectPath?: string | null;
  mobileWorkers: PreviewTarget[];
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
  onReconnect?: () => Promise<void>;
  onRepairRelay?: () => Promise<{ repaired: boolean; reason: string }>;
  connectedDeviceNeedsAuth?: boolean;
  onSwitchAgent?: () => void;
  onTriggerReauth?: (runner: string) => void;
  primaryRunner?: string | null;
}

export default function WebviewView({
  connectedDevice,
  connState,
  preferredMode = "mobile",
  preferredProjectPath,
  mobileWorkers,
  selectedPreviewTarget,
  onSelectPreviewTarget,
  onReconnect,
  onRepairRelay,
  connectedDeviceNeedsAuth,
  onSwitchAgent,
  onTriggerReauth,
  primaryRunner,
}: Props) {
  const [mode, setMode] = useState<WebviewMode>(preferredMode);

  useEffect(() => {
    setMode(preferredMode);
  }, [preferredMode]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b border-surface-800 bg-surface-950/80 px-3 py-2 md:px-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <span className="text-lg">📱</span>
            <div>
              <p className="text-sm font-semibold text-surface-100">Webview</p>
              <p className="text-[11px] text-surface-500">
                One surface for Hot Reload and browser preview.
              </p>
            </div>
          </div>
          <div className="inline-flex rounded-lg border border-surface-800 bg-surface-900/70 p-1">
            <button
              onClick={() => setMode("mobile")}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                mode === "mobile"
                  ? "bg-emerald-500/15 text-emerald-200"
                  : "text-surface-400 hover:text-surface-200"
              }`}
            >
              Hot Reload
            </button>
            <button
              onClick={() => setMode("web")}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                mode === "web"
                  ? "bg-sky-500/15 text-sky-200"
                  : "text-surface-400 hover:text-surface-200"
              }`}
            >
              Web App
            </button>
          </div>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden">
        {mode === "mobile" ? (
          <PreviewPane
            selectedPreviewTarget={selectedPreviewTarget}
            onSelectPreviewTarget={onSelectPreviewTarget}
            mobileWorkers={mobileWorkers}
            preferredProjectPath={preferredProjectPath}
            onReconnect={onReconnect}
            onRepairRelay={onRepairRelay}
            connectedDeviceNeedsAuth={connectedDeviceNeedsAuth}
            onSwitchAgent={onSwitchAgent}
            onTriggerReauth={onTriggerReauth}
            primaryRunner={primaryRunner}
          />
        ) : (
          <WebReloadView
            connectedDevice={connectedDevice}
            connState={connState}
            preferredProjectPath={preferredProjectPath}
            onReconnect={onReconnect}
            onRepairRelay={onRepairRelay}
          />
        )}
      </div>
    </div>
  );
}
