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
  preferredMode = "web",
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
      {/* Compact mode-switcher bar — merged with the inner view's
          header so the iframe gets ~40px more vertical space. The
          old "Webview / One surface for both…" title block was
          decorative and burned vertical real estate that the
          viewport could use. */}
      <div className="flex flex-shrink-0 items-center gap-2 border-b border-surface-800 bg-surface-950/70 px-3 py-1.5">
        <span className="text-[14px] leading-none">📱</span>
        <div className="inline-flex rounded-md border border-surface-800 bg-surface-900/70 p-0.5">
          <button
            onClick={() => setMode("mobile")}
            className={`rounded px-2.5 py-1 text-[11px] font-medium transition-colors ${
              mode === "mobile"
                ? "bg-emerald-500/15 text-emerald-200"
                : "text-surface-400 hover:text-surface-200"
            }`}
          >
            Mobile App
          </button>
          <button
            onClick={() => setMode("web")}
            className={`rounded px-2.5 py-1 text-[11px] font-medium transition-colors ${
              mode === "web"
                ? "bg-sky-500/15 text-sky-200"
                : "text-surface-400 hover:text-surface-200"
            }`}
          >
            Web App
          </button>
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
