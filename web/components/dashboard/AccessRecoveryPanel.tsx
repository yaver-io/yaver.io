"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import type { Device } from "@/lib/use-devices";
import {
  enableHostedAccessChannel,
  enrollAccessDevice,
  listAccessBrokers,
  requestYaverSignIn,
  type AccessBroker,
} from "@/lib/accessChannel";
import PendingSignInsPanel from "@/components/dashboard/PendingSignInsPanel";

export default function AccessRecoveryPanel({ token, devices }: { token: string | null; devices: Device[] }) {
  const [brokers, setBrokers] = useState<AccessBroker[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);
  const [approvalKey, setApprovalKey] = useState(0);

  const refresh = useCallback(async () => {
    if (!token) return;
    try {
      setBrokers(await listAccessBrokers(token));
    } catch (error) {
      setMessage({ ok: false, text: error instanceof Error ? error.message : "Could not load remote access." });
    } finally {
      setLoaded(true);
    }
  }, [token]);

  useEffect(() => { void refresh(); }, [refresh]);

  const hosted = brokers.find((broker) => broker.deployment === "yaver-hosted" && broker.enabled);
  const recoveryDevices = useMemo(
    () => devices.filter((device) => device.needsAuth),
    [devices],
  );
  const unprotectedDevices = useMemo(
    () => hosted ? devices.filter((device) => !device.needsAuth && !hosted.deviceIds.includes(device.id)) : [],
    [devices, hosted],
  );

  const enable = async () => {
    if (!token) return;
    setBusy("enable");
    setMessage(null);
    try {
      await enableHostedAccessChannel(token);
      setMessage({ ok: true, text: "Remote access is ready." });
      await refresh();
    } catch (error) {
      setMessage({ ok: false, text: error instanceof Error ? error.message : "Could not enable remote access." });
    } finally {
      setBusy(null);
    }
  };

  const enroll = async (device: Device) => {
    if (!token || !hosted) return;
    setBusy(`enroll:${device.id}`);
    setMessage(null);
    try {
      await enrollAccessDevice(token, hosted.brokerId, device.id);
      setMessage({ ok: true, text: `${device.name} is protected for remote recovery.` });
      await refresh();
    } catch (error) {
      setMessage({ ok: false, text: error instanceof Error ? error.message : `Could not protect ${device.name}.` });
    } finally {
      setBusy(null);
    }
  };

  const recover = async (device: Device) => {
    if (!token || !hosted) return;
    setBusy(`recover:${device.id}`);
    setMessage(null);
    try {
      await requestYaverSignIn(token, hosted.brokerId, device.id);
      setMessage({ ok: true, text: `${device.name} answered. Approve its sign-in below.` });
      setApprovalKey((value) => value + 1);
    } catch (error) {
      setMessage({ ok: false, text: error instanceof Error ? error.message : `Could not reach ${device.name}.` });
    } finally {
      setBusy(null);
    }
  };

  if (!token) return null;

  return (
    <div className="space-y-4">
      <section aria-label="Remote device recovery" className="rounded-2xl border border-cyan-500/25 bg-cyan-500/[0.05] p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-sm font-semibold text-surface-100">Remote access</h2>
            <p className="mt-1 max-w-2xl text-[11px] leading-relaxed text-surface-400">
              Recover a headless box through a payload-minimal Cloudflare channel. Source, terminal data, keystrokes, and account credentials never pass through it.
            </p>
          </div>
          {hosted ? (
            <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1 text-[10px] font-semibold text-emerald-300">Ready</span>
          ) : loaded ? (
            <button
              type="button"
              disabled={busy === "enable"}
              onClick={() => void enable()}
              className="rounded-lg bg-cyan-500 px-3 py-1.5 text-[12px] font-semibold text-surface-950 hover:bg-cyan-400 disabled:opacity-50"
            >
              {busy === "enable" ? "Enabling…" : "Enable remote access"}
            </button>
          ) : (
            <span className="text-[11px] text-surface-500">Checking…</span>
          )}
        </div>

        {hosted && recoveryDevices.length > 0 ? (
          <div className="mt-4 space-y-2">
            {recoveryDevices.map((device) => {
              const enrolled = hosted.deviceIds.includes(device.id);
              const key = `recover:${device.id}`;
              return (
                <div key={device.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-amber-500/25 bg-amber-500/[0.06] px-3 py-2.5">
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-semibold text-surface-100">{device.name}</div>
                    <div className="text-[10px] text-surface-500">
                      {enrolled ? "Agent authentication expired · secure recovery available" : "Not enrolled before it lost authentication"}
                    </div>
                  </div>
                  <button
                    type="button"
                    disabled={!enrolled || busy === key}
                    onClick={() => void recover(device)}
                    title={enrolled ? `Start remote sign-in on ${device.name}` : "Use Reclaim on the device row; protect it after it is signed in"}
                    className="rounded-lg bg-amber-500 px-3 py-1.5 text-[11px] font-semibold text-surface-950 hover:bg-amber-400 disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    {busy === key ? "Contacting…" : enrolled ? "Authenticate remotely" : "Reclaim first"}
                  </button>
                </div>
              );
            })}
          </div>
        ) : hosted ? (
          <p className="mt-3 text-[11px] text-surface-500">No enrolled box currently needs account recovery.</p>
        ) : null}

        {hosted && unprotectedDevices.length > 0 ? (
          <details className="mt-3">
            <summary className="cursor-pointer text-[11px] font-semibold text-surface-400 hover:text-surface-200">
              Protect healthy devices ({unprotectedDevices.length})
            </summary>
            <div className="mt-2 space-y-1.5">
              {unprotectedDevices.map((device) => {
                const key = `enroll:${device.id}`;
                return (
                  <div key={device.id} className="flex items-center gap-3 rounded-lg border border-surface-800 px-3 py-2">
                    <span className="min-w-0 flex-1 truncate text-[12px] text-surface-200">{device.name}</span>
                    <button
                      type="button"
                      disabled={busy === key}
                      onClick={() => void enroll(device)}
                      className="rounded-md border border-surface-700 px-2.5 py-1 text-[10px] font-semibold text-surface-300 hover:border-cyan-500/50 hover:text-cyan-300 disabled:opacity-50"
                    >
                      {busy === key ? "Protecting…" : "Protect"}
                    </button>
                  </div>
                );
              })}
            </div>
          </details>
        ) : null}

        {message ? (
          <p role="status" className={`mt-3 text-[11px] font-medium ${message.ok ? "text-emerald-400" : "text-amber-400"}`}>{message.text}</p>
        ) : null}
      </section>

      <PendingSignInsPanel key={approvalKey} token={token} />
    </div>
  );
}
