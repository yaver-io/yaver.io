// Org admin — SSO. Live OIDC config form (load + save + test + clear).
// SAML tab is honest: "not implemented, use OIDC or contact." Solo-dev
// safety: no config row = no SSO button on /auth, exact pre-OIDC
// behavior.
"use client";

import { useEffect, useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { ConfirmDestructive } from "@/components/admin/ConfirmDestructive";
import { useAdminFetch } from "@/components/admin/useAdminFetch";
import { useToast } from "@/components/admin/Toaster";
import { CONVEX_URL } from "@/lib/constants";
import { ShieldCheck, ShieldX, KeyRound, FileText, Send } from "@/components/admin/icons";

type Tab = "oidc" | "saml";

type OidcConfig = {
  _id: string;
  enabled: boolean;
  issuerUrl: string;
  clientId: string;
  tenant: string;
  hasClientSecret: boolean;
  authorizationEndpoint: string | null;
  tokenEndpoint: string | null;
  userinfoEndpoint: string | null;
  jwksUri: string | null;
  discoveredAt: number | null;
  updatedAt: number;
};

function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;
  const ls = localStorage.getItem("yaver_auth_token");
  if (ls) return ls;
  for (const cookie of document.cookie.split(";")) {
    const [name, value] = cookie.trim().split("=");
    if (name === "yaver_session" || name === "yaver_auth_token") return value || null;
  }
  return null;
}

export default function AdminSso() {
  const [tab, setTab] = useState<Tab>("oidc");
  const { data, error, loading, refresh } = useAdminFetch<{ config: OidcConfig | null }>(
    "/admin/sso/oidc",
  );
  const toast = useToast();

  const [enabled, setEnabled] = useState(false);
  const [issuer, setIssuer] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [tenant, setTenant] = useState("");
  const [hydrated, setHydrated] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; status: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);

  useEffect(() => {
    if (!data || hydrated) return;
    const c = data.config;
    if (c) {
      setEnabled(c.enabled);
      setIssuer(c.issuerUrl);
      setClientId(c.clientId);
      setTenant(c.tenant ?? "");
      // Never echo back the secret — only the boolean "set or not."
    }
    setHydrated(true);
  }, [data, hydrated]);

  async function test() {
    if (!issuer.trim()) {
      setTestResult({ ok: false, status: "Issuer URL is required." });
      return;
    }
    setTesting(true);
    setTestResult(null);
    const token = getStoredToken();
    if (!token) {
      setTesting(false);
      return;
    }
    try {
      const res = await fetch(`${CONVEX_URL}/admin/sso/oidc/test`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ issuerUrl: issuer }),
      });
      if (!res.ok) {
        const t = await res.text().catch(() => "");
        throw new Error(`${res.status} ${t || res.statusText}`);
      }
      const json = await res.json();
      setTestResult({ ok: !!json.ok, status: json.status || "" });
    } catch (err: any) {
      setTestResult({ ok: false, status: String(err?.message || err) });
    } finally {
      setTesting(false);
    }
  }

  async function save() {
    if (!issuer.trim() || !clientId.trim()) {
      toast.push({ tone: "danger", title: "Issuer URL and client ID are required." });
      return;
    }
    if (!data?.config && !clientSecret.trim()) {
      toast.push({ tone: "danger", title: "Client secret required on first save." });
      return;
    }
    setSaving(true);
    const token = getStoredToken();
    if (!token) {
      setSaving(false);
      return;
    }
    try {
      const res = await fetch(`${CONVEX_URL}/admin/sso/oidc`, {
        method: "PUT",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          enabled,
          issuerUrl: issuer,
          clientId,
          clientSecret,
          tenant: tenant.trim() || undefined,
        }),
      });
      if (!res.ok) {
        const t = await res.text().catch(() => "");
        throw new Error(`${res.status} ${t || res.statusText}`);
      }
      const json = await res.json();
      toast.push({
        tone: "success",
        title: enabled ? "OIDC saved & enabled" : "OIDC saved (disabled)",
        body: json.discovered ? "Discovery endpoints refreshed." : "Discovery skipped.",
      });
      setClientSecret("");
      refresh();
    } catch (err: any) {
      toast.push({ tone: "danger", title: "Save failed", body: String(err?.message || err) });
    } finally {
      setSaving(false);
    }
  }

  async function clearConfig() {
    const token = getStoredToken();
    if (!token) return;
    const res = await fetch(`${CONVEX_URL}/admin/sso/oidc`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) {
      const t = await res.text().catch(() => "");
      throw new Error(`${res.status} ${t || res.statusText}`);
    }
    setEnabled(false);
    setIssuer("");
    setClientId("");
    setClientSecret("");
    setTenant("");
    setHydrated(false);
    toast.push({ tone: "warning", title: "OIDC config cleared" });
    refresh();
  }

  return (
    <AdminShell
      pageTitle="Single sign-on"
      pageSubtitle="Hook this deployment into your org's identity provider. The OIDC tab is live; SAML is on the roadmap."
    >
      <div className="mb-3 inline-flex rounded-md border border-surface-800 bg-surface-900 p-0.5">
        {[
          { id: "oidc", label: "OIDC", icon: KeyRound },
          { id: "saml", label: "SAML 2.0", icon: FileText },
        ].map((t) => {
          const active = tab === t.id;
          const Icon = t.icon;
          return (
            <button
              key={t.id}
              onClick={() => setTab(t.id as Tab)}
              className={`inline-flex items-center gap-1.5 rounded px-3 py-1.5 text-[12px] transition-colors ${
                active
                  ? "bg-warning-soft text-warning-softFg"
                  : "text-surface-300 hover:text-surface-100"
              }`}
            >
              <Icon className="h-3.5 w-3.5" />
              {t.label}
            </button>
          );
        })}
      </div>

      {tab === "oidc" && (
        <div className="space-y-4">
          {error && (
            <div className="rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
              Failed to load: {error}
            </div>
          )}
          {loading && !data && (
            <div className="text-[12px] text-surface-400">Loading current config…</div>
          )}

          <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
            <div className="flex items-start justify-between gap-3">
              <h2 className="flex items-center gap-2 text-[13px] font-semibold text-surface-100">
                <ShieldCheck className="h-4 w-4 text-warning" />
                Generic OIDC issuer
              </h2>
              <label className="flex cursor-pointer items-center gap-2 text-[12px] text-surface-200">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => setEnabled(e.target.checked)}
                  className="h-4 w-4 accent-warning"
                />
                Enabled
              </label>
            </div>
            <p className="mt-1 text-[12px] text-surface-400">
              Works with Keycloak, Auth0, Okta-as-OIDC, Microsoft Entra ID (tenant-scoped),
              Google Workspace, and any provider that exposes a standard{" "}
              <span className="font-mono">/.well-known/openid-configuration</span>.
              Solo-dev safety: when Enabled is off, the company-SSO button never renders
              on <span className="font-mono">/auth</span>, exact pre-OIDC behavior.
            </p>

            <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field
                label="Issuer URL"
                value={issuer}
                onChange={setIssuer}
                placeholder="https://login.example.com/realms/eng"
                mono
              />
              <Field
                label="Tenant restriction (optional)"
                value={tenant}
                onChange={setTenant}
                placeholder="eng.example.com"
                mono
                hint="Email domain or tenant id; refuses sign-in from any other tenant."
              />
              <Field
                label="Client ID"
                value={clientId}
                onChange={setClientId}
                placeholder="yaver-onprem"
                mono
              />
              <Field
                label={
                  data?.config?.hasClientSecret
                    ? "Client secret (leave blank to keep current)"
                    : "Client secret"
                }
                value={clientSecret}
                onChange={setClientSecret}
                placeholder={data?.config?.hasClientSecret ? "(unchanged)" : "(required)"}
                mono
                type="password"
              />
            </div>

            <div className="mt-4 flex flex-wrap items-center gap-2">
              <button
                onClick={test}
                disabled={testing || !issuer.trim()}
                className="inline-flex items-center gap-1.5 rounded border border-surface-700 px-2.5 py-1.5 text-[12px] text-surface-200 hover:border-surface-500 hover:text-surface-100 disabled:opacity-40"
              >
                <Send className="h-3.5 w-3.5" />
                {testing ? "Testing…" : "Test connection"}
              </button>
              <button
                onClick={save}
                disabled={saving}
                className="inline-flex items-center gap-1.5 rounded bg-warning px-2.5 py-1.5 text-[12px] font-medium text-warning-fg disabled:opacity-40"
              >
                {saving ? "Saving…" : "Save OIDC config"}
              </button>
              {data?.config && (
                <button
                  onClick={() => setConfirmClear(true)}
                  className="ml-auto rounded border border-danger/40 bg-danger-soft px-2.5 py-1.5 text-[12px] font-medium text-danger-softFg hover:opacity-90"
                >
                  Clear config
                </button>
              )}
            </div>

            {testResult && (
              <div
                className={`mt-3 rounded border p-2 text-[12px] ${
                  testResult.ok
                    ? "border-success/40 bg-success-soft text-success-softFg"
                    : "border-danger/40 bg-danger-soft text-danger-softFg"
                }`}
              >
                {testResult.status}
              </div>
            )}
          </section>

          <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
            <h3 className="text-[12px] font-semibold uppercase tracking-wider text-surface-400">
              Redirect URI to register at your IdP
            </h3>
            <div className="mt-2 break-all rounded border border-surface-800 bg-surface-950 p-2 font-mono text-[12px] text-surface-200">
              {typeof window !== "undefined"
                ? `${window.location.origin}/auth/oidc/callback`
                : "/auth/oidc/callback"}
            </div>
            <p className="mt-2 text-[11px] text-surface-400">
              Must be exact. PKCE + state are mandatory; the backend stores ephemeral
              attempt rows for 10 min then prunes them.
            </p>
          </section>

          {data?.config && (
            <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
              <h3 className="text-[12px] font-semibold uppercase tracking-wider text-surface-400">
                Discovered endpoints
              </h3>
              <dl className="mt-2 grid grid-cols-1 gap-x-4 gap-y-1 text-[12px] sm:grid-cols-[160px_1fr]">
                {[
                  ["Authorization", data.config.authorizationEndpoint],
                  ["Token", data.config.tokenEndpoint],
                  ["Userinfo", data.config.userinfoEndpoint],
                  ["JWKS", data.config.jwksUri],
                ].map(([label, value]) => (
                  <div key={label as string} className="contents">
                    <dt className="text-surface-400">{label}</dt>
                    <dd className="break-all font-mono text-surface-200">{value || "—"}</dd>
                  </div>
                ))}
              </dl>
              {data.config.discoveredAt && (
                <p className="mt-2 text-[11px] text-surface-400">
                  Last discovery: {new Date(data.config.discoveredAt).toISOString()}
                </p>
              )}
            </section>
          )}
        </div>
      )}

      {tab === "saml" && (
        <section className="rounded-md border border-surface-800 bg-surface-900 p-6">
          <div className="flex items-start gap-3">
            <div className="rounded border border-warning/40 bg-warning-soft p-1.5 text-warning-softFg">
              <ShieldX className="h-4 w-4" />
            </div>
            <div className="min-w-0">
              <h2 className="text-[14px] font-semibold text-surface-100">
                SAML 2.0 is not implemented
              </h2>
              <p className="mt-2 max-w-2xl text-[13px] leading-relaxed text-surface-300">
                Yaver does not yet have a SAML handler. There is no IdP metadata parser,
                signed-assertion validator, or attribute mapper in the backend. Marketing
                copy that claimed otherwise has been removed.
              </p>
              <p className="mt-3 max-w-2xl text-[13px] leading-relaxed text-surface-300">
                For enterprise SSO today the recommended path is the OIDC tab — most
                modern IdPs (Entra ID, Okta, Auth0, Keycloak, Ping) expose an OIDC
                surface in addition to SAML.
              </p>
              <p className="mt-3 max-w-2xl text-[13px] leading-relaxed text-surface-300">
                If your org cannot use OIDC and SAML is a hard requirement, write to{" "}
                <a
                  href="mailto:kivanc.cakmak@simkab.com"
                  className="font-medium text-warning-softFg underline-offset-2 hover:underline"
                >
                  kivanc.cakmak@simkab.com
                </a>{" "}
                so it can be sequenced.
              </p>
            </div>
          </div>
        </section>
      )}

      {confirmClear && (
        <ConfirmDestructive
          open
          title="Clear OIDC configuration"
          body={
            <span>
              Deletes the saved config row. The company-SSO button vanishes from{" "}
              <span className="font-mono">/auth</span> immediately. Users with existing
              <span className="font-mono"> provider=oidc</span> accounts can no longer
              sign in until you reconfigure. Audit-logged.
            </span>
          }
          confirmLabel="Clear OIDC"
          confirmPhrase="clear"
          destructive
          onClose={() => setConfirmClear(false)}
          onConfirm={async () => {
            await clearConfig();
          }}
        />
      )}
    </AdminShell>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  mono = false,
  type = "text",
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  type?: string;
  hint?: string;
}) {
  return (
    <label className="block">
      <span className="block text-[11px] font-medium uppercase tracking-wider text-surface-400">
        {label}
      </span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={`mt-1 w-full rounded border border-surface-700 bg-surface-950 px-2 py-1.5 text-[13px] text-surface-100 outline-none focus:border-warning ${
          mono ? "font-mono" : ""
        }`}
      />
      {hint && <span className="mt-1 block text-[11px] text-surface-400">{hint}</span>}
    </label>
  );
}
