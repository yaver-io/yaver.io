"use client";

import { useEffect, useMemo, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

type PublisherPlatform = "apple" | "google-play" | "microsoft-store" | "xbox" | "playstation";
type ProgramStatus = "not-started" | "in-progress" | "submitted" | "approved" | "action-required";

type PublisherProfile = {
  entityType?: "individual" | "organization";
  legalName?: string;
  publisherName?: string;
  dunsNumber?: string;
  countryCode?: string;
  addressLine1?: string;
  addressLine2?: string;
  city?: string;
  region?: string;
  postalCode?: string;
  website?: string;
  businessEmail?: string;
  supportEmail?: string;
  phone?: string;
  programs?: Array<{ platform: PublisherPlatform; status: ProgramStatus; updatedAt?: number }>;
};

const PROGRAMS: Array<{ id: PublisherPlatform; label: string; url: string }> = [
  { id: "apple", label: "Apple Developer", url: "https://developer.apple.com/programs/enroll/" },
  { id: "google-play", label: "Google Play", url: "https://play.google.com/console/signup" },
  { id: "microsoft-store", label: "Microsoft Store", url: "https://developer.microsoft.com/microsoft-store/register" },
  { id: "xbox", label: "ID@Xbox", url: "https://www.xbox.com/publish" },
  { id: "playstation", label: "PlayStation Partners", url: "https://partners.playstation.net/" },
];

const EMPTY: PublisherProfile = { entityType: "individual", programs: [] };
const inputClass = "w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2.5 text-sm text-surface-200 placeholder-surface-600 outline-none focus:border-surface-500";

export default function PublisherSetupCard({ token }: { token: string | null }) {
  const [profile, setProfile] = useState<PublisherProfile>(EMPTY);
  const [expanded, setExpanded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    if (!token) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    fetch(`${CONVEX_URL}/settings`, { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" })
      .then(async (res) => {
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
        return data?.settings?.publisherProfile as PublisherProfile | undefined;
      })
      .then((value) => { if (!cancelled && value) setProfile({ ...EMPTY, ...value }); })
      .catch((error) => { if (!cancelled) setMessage(error instanceof Error ? error.message : "Could not load publisher setup."); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [token]);

  const required = useMemo(() => {
    const fields = [profile.legalName, profile.publisherName, profile.countryCode, profile.addressLine1, profile.city, profile.postalCode, profile.website, profile.businessEmail, profile.phone];
    if (profile.entityType === "organization") fields.push(profile.dunsNumber);
    return { complete: fields.filter((value) => !!value?.trim()).length, total: fields.length };
  }, [profile]);

  const update = (field: keyof PublisherProfile, value: string) => setProfile((current) => ({ ...current, [field]: value }));
  const programStatus = (id: PublisherPlatform): ProgramStatus => profile.programs?.find((row) => row.platform === id)?.status ?? "not-started";
  const setProgramStatus = (platform: PublisherPlatform, status: ProgramStatus) => setProfile((current) => ({
    ...current,
    programs: [...(current.programs ?? []).filter((row) => row.platform !== platform), { platform, status }],
  }));

  const save = async () => {
    if (!token) return;
    setSaving(true);
    setMessage(null);
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ publisherProfile: profile }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
      setMessage("Publisher profile saved. Yaver MCP can now reuse it while guiding official enrollment forms.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not save publisher profile.");
    } finally {
      setSaving(false);
    }
  };

  const clear = async () => {
    if (!token) return;
    setSaving(true);
    setMessage(null);
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ publisherProfile: null }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
      setProfile(EMPTY);
      setMessage("Publisher profile cleared.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not clear publisher profile.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="card mb-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h3 className="text-sm font-medium uppercase tracking-wider text-surface-400">Publisher setup</h3>
          <p className="mt-2 max-w-2xl text-xs leading-5 text-surface-500">
            Enter reusable business details once. Yaver can prefill official store forms, but pauses for login, 2FA, identity checks, agreements, payment, tax/bank details, and final submission.
          </p>
          <p className="mt-2 text-xs text-surface-400">Readiness · {required.complete}/{required.total} identity fields</p>
        </div>
        <button type="button" onClick={() => setExpanded((value) => !value)} className="shrink-0 rounded-lg border border-surface-700 px-3 py-2 text-xs text-surface-300 hover:bg-surface-800/60">
          {expanded ? "Close" : loading ? "Loading…" : "Set up"}
        </button>
      </div>

      {expanded ? (
        <div className="mt-5 space-y-5 border-t border-surface-800 pt-5">
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="text-xs text-surface-500">Account type
              <select value={profile.entityType ?? "individual"} onChange={(e) => setProfile((current) => ({ ...current, entityType: e.target.value as PublisherProfile["entityType"], ...(e.target.value === "individual" ? { dunsNumber: "" } : {}) }))} className={`${inputClass} mt-1`}>
                <option value="individual">Individual</option>
                <option value="organization">Organization</option>
              </select>
            </label>
            <label className="text-xs text-surface-500">Legal name<input value={profile.legalName ?? ""} onChange={(e) => update("legalName", e.target.value)} className={`${inputClass} mt-1`} autoComplete="organization" /></label>
            <label className="text-xs text-surface-500">Public publisher name<input value={profile.publisherName ?? ""} onChange={(e) => update("publisherName", e.target.value)} className={`${inputClass} mt-1`} /></label>
            {profile.entityType === "organization" ? <label className="text-xs text-surface-500">D‑U‑N‑S number<input value={profile.dunsNumber ?? ""} onChange={(e) => update("dunsNumber", e.target.value.replace(/\D/g, "").slice(0, 9))} inputMode="numeric" placeholder="9 digits" className={`${inputClass} mt-1`} /></label> : null}
            <label className="text-xs text-surface-500">Business email<input value={profile.businessEmail ?? ""} onChange={(e) => update("businessEmail", e.target.value)} type="email" autoComplete="email" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">Support email<input value={profile.supportEmail ?? ""} onChange={(e) => update("supportEmail", e.target.value)} type="email" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">Website<input value={profile.website ?? ""} onChange={(e) => update("website", e.target.value)} type="url" placeholder="https://" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">Phone<input value={profile.phone ?? ""} onChange={(e) => update("phone", e.target.value)} type="tel" autoComplete="tel" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500 sm:col-span-2">Legal address<input value={profile.addressLine1 ?? ""} onChange={(e) => update("addressLine1", e.target.value)} autoComplete="street-address" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">Address line 2<input value={profile.addressLine2 ?? ""} onChange={(e) => update("addressLine2", e.target.value)} className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">City<input value={profile.city ?? ""} onChange={(e) => update("city", e.target.value)} autoComplete="address-level2" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">State / region<input value={profile.region ?? ""} onChange={(e) => update("region", e.target.value)} autoComplete="address-level1" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">Postal code<input value={profile.postalCode ?? ""} onChange={(e) => update("postalCode", e.target.value)} autoComplete="postal-code" className={`${inputClass} mt-1`} /></label>
            <label className="text-xs text-surface-500">Country code<input value={profile.countryCode ?? ""} onChange={(e) => update("countryCode", e.target.value.toUpperCase().replace(/[^A-Z]/g, "").slice(0, 2))} placeholder="US" autoComplete="country" className={`${inputClass} mt-1`} /></label>
          </div>

          <div>
            <h4 className="text-xs font-medium uppercase tracking-wider text-surface-500">Platform programs</h4>
            <div className="mt-2 space-y-2">
              {PROGRAMS.map((program) => (
                <div key={program.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-surface-800 bg-surface-900/50 px-3 py-2">
                  <span className="min-w-32 flex-1 text-sm text-surface-200">{program.label}</span>
                  <select value={programStatus(program.id)} onChange={(e) => setProgramStatus(program.id, e.target.value as ProgramStatus)} className="rounded-md border border-surface-700 bg-surface-950 px-2 py-1.5 text-xs text-surface-300">
                    <option value="not-started">Not started</option><option value="in-progress">In progress</option><option value="submitted">Submitted</option><option value="approved">Approved</option><option value="action-required">Action required</option>
                  </select>
                  <a href={program.url} target="_blank" rel="noopener noreferrer" className="rounded-md border border-surface-700 px-2.5 py-1.5 text-xs text-surface-300 hover:text-surface-50">Official portal</a>
                </div>
              ))}
            </div>
          </div>

          <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-3 text-xs leading-5 text-amber-700 dark:text-amber-200">
            Yaver does not store store passwords, payment cards, bank/tax identifiers, signing private keys, or recovery codes here. Browser automation fills ordinary fields and stops before legal or money-moving actions.
          </div>
          {message ? <p className="text-xs text-surface-400">{message}</p> : null}
          <div className="flex gap-2">
            <button type="button" onClick={() => void save()} disabled={!token || saving} className="flex-1 rounded-lg bg-surface-100 px-4 py-3 text-sm font-medium text-surface-950 disabled:opacity-40">
              {saving ? "Saving…" : "Save publisher profile"}
            </button>
            <button type="button" onClick={() => void clear()} disabled={!token || saving} className="rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-400 disabled:opacity-40">Clear</button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
