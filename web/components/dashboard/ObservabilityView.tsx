"use client";

import { useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Tab = "schema" | "storage" | "jobs" | "logs" | "cost";

export default function ObservabilityView() {
  const [directory, setDirectory] = useState("");
  const [tab, setTab] = useState<Tab>("schema");

  return (
    <div className="space-y-4">
      <input value={directory} onChange={(e) => setDirectory(e.target.value)}
        placeholder="project directory"
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
      <div className="flex gap-1 border-b border-surface-800 overflow-auto">
        {(["schema", "storage", "jobs", "logs", "cost"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold whitespace-nowrap ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}>
            {t}
          </button>
        ))}
      </div>
      {tab === "schema" && <Schema directory={directory} />}
      {tab === "storage" && <Storage directory={directory} />}
      {tab === "jobs" && <Jobs directory={directory} />}
      {tab === "logs" && <Logs directory={directory} />}
      {tab === "cost" && <Cost directory={directory} />}
    </div>
  );
}

function Schema({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.backendSchema(directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  if (data.error) return <div className="text-xs text-red-400">{data.error}</div>;
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">Backend: {data.backend} · Source: {data.source}</div>
      <div className="grid md:grid-cols-2 gap-3">
        <div className="space-y-2">
          {(data.tables || []).map((t: any) => (
            <div key={t.name} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3">
              <div className="text-sm font-semibold font-mono text-indigo-300">{t.name}</div>
              <div className="mt-1 space-y-0.5">
                {(t.columns || []).map((c: any, i: number) => (
                  <div key={i} className="text-xs font-mono text-surface-400 flex gap-2">
                    <span className="text-surface-200">{c.name}</span>
                    <span className="text-surface-500">{c.type}</span>
                    {c.primaryKey && <span className="text-amber-400">PK</span>}
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
        <div>
          <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">Mermaid ERD</h3>
          <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[600px]">{data.mermaid}</pre>
        </div>
      </div>
    </div>
  );
}

function Storage({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  const [profiles, setProfiles] = useState<any[]>([]);
  const [selectedProfile, setSelectedProfile] = useState<string>("");
  const [sharedPath, setSharedPath] = useState("");
  const [sharedListing, setSharedListing] = useState<any>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [searchHits, setSearchHits] = useState<any[]>([]);
  const [guestConfigs, setGuestConfigs] = useState<any[]>([]);
  const [saveMsg, setSaveMsg] = useState<string | null>(null);
  const [profileForm, setProfileForm] = useState<any>({ type: "local", name: "", path: "", mount_path: "", remote: "", endpoint: "", bucket: "", region: "", username: "", password: "", access_key: "", secret_key: "", notes: "", container_mount_mode: "none", container_path: "" });

  async function refreshProjectStorage() {
    setData(await agentClient.storageList(undefined, directory || undefined));
  }

  async function refreshProfiles() {
    const res = await agentClient.sharedStorageProfiles();
    const next = res.profiles || [];
    setProfiles(next);
    if (!selectedProfile && next[0]?.id) {
      setSelectedProfile(next[0].id);
    }
  }

  async function refreshGuestConfigs() {
    try {
      setGuestConfigs(await agentClient.getGuestConfigs());
    } catch {
      setGuestConfigs([]);
    }
  }

  useEffect(() => { refreshProjectStorage(); }, [directory]);
  useEffect(() => { refreshProfiles(); }, []);
  useEffect(() => { refreshGuestConfigs(); }, []);
  useEffect(() => {
    if (!selectedProfile) {
      setSharedListing(null);
      return;
    }
    (async () => setSharedListing(await agentClient.sharedStorageList(selectedProfile, sharedPath)))();
  }, [selectedProfile, sharedPath]);

  async function saveProfile() {
    setSaveMsg(null);
    try {
      const res = await agentClient.sharedStorageUpsert(profileForm);
      if (!res.error) {
        setProfileForm({ type: "local", name: "", path: "", mount_path: "", remote: "", endpoint: "", bucket: "", region: "", username: "", password: "", access_key: "", secret_key: "", notes: "", container_mount_mode: "none", container_path: "" });
        await refreshProfiles();
      } else {
        const e = String(res.error);
        setSaveMsg(e.length <= 160 ? e : "Couldn't save the profile. Check the fields and try again.");
      }
    } catch {
      setSaveMsg("Couldn't save the profile — the agent may be unreachable.");
    }
  }

  async function removeProfile(id: string) {
    if (!confirm("Delete this shared storage profile?")) return;
    await agentClient.sharedStorageDelete(id);
    if (selectedProfile === id) {
      setSelectedProfile("");
      setSharedPath("");
      setSearchHits([]);
    }
    await refreshProfiles();
  }

  async function runSearch() {
    if (!searchQuery.trim()) {
      setSearchHits([]);
      return;
    }
    const res = await agentClient.sharedStorageSearch(searchQuery, { id: selectedProfile || undefined, path: sharedPath || undefined, limit: 30 });
    setSearchHits(res.hits || []);
  }

  async function toggleGuestStorage(email: string, profileId: string, checked: boolean) {
    const cfg = guestConfigs.find((g: any) => g.guestEmail === email);
    if (!cfg) return;
    const current = new Set<string>(cfg.allowedSharedStorage || []);
    if (checked) current.add(profileId);
    else current.delete(profileId);
    await agentClient.updateGuestConfig({
      email,
      allowedSharedStorage: Array.from(current),
    });
    await refreshGuestConfigs();
  }

  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
        <div className="flex items-center gap-2">
          <div className="text-xs uppercase text-surface-500">Project Storage</div>
          <button onClick={refreshProjectStorage} className="px-2 py-1 text-[11px] rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
        </div>
        <div className="text-xs text-surface-500">Source: {data.source}</div>
        {data.error && <div className="text-xs text-red-400">{data.error}</div>}
        {(data.files || []).map((f: any) => (
          <div key={f.id} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
            <span className="font-mono flex-1 truncate">{f.name}</span>
            <span className="text-surface-500 font-mono">{fmtBytes(f.size)}</span>
            <span className="text-surface-600 text-[10px]">{f.createdAt?.slice(0, 10)}</span>
          </div>
        ))}
      </div>

      <div className="grid gap-4 xl:grid-cols-[320px,1fr]">
        <div className="space-y-3">
          <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
            <div className="flex items-center justify-between">
              <div className="text-xs uppercase text-surface-500">Shared Storage</div>
              <button onClick={refreshProfiles} className="px-2 py-1 text-[11px] rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
            </div>
            {profiles.length === 0 && <div className="text-xs text-surface-500">No NAS/shared storage profiles yet.</div>}
            {profiles.map((p: any) => (
              <div key={p.id} className={`rounded-lg border p-2 ${selectedProfile === p.id ? "border-indigo-500/50 bg-indigo-500/10" : "border-surface-800 bg-surface-900/50"}`}>
                <button onClick={() => { setSelectedProfile(p.id); setSharedPath(""); setSearchHits([]); }} className="w-full text-left">
                  <div className="flex items-center gap-2">
                    <span className={`h-2 w-2 rounded-full ${p.available ? "bg-emerald-400" : "bg-amber-400"}`} />
                    <span className="flex-1 truncate text-sm text-surface-200">{p.name}</span>
                    <span className="text-[10px] uppercase text-surface-500">{p.type}</span>
                  </div>
                  <div className="mt-1 text-[11px] text-surface-500 truncate">{p.resolvedLocation || p.endpoint || p.remote || p.path}</div>
                  <div className="text-[10px] text-surface-600 truncate">{p.status}{p.containerMountMode && p.containerMountMode !== "none" ? ` · container:${p.containerMountMode}` : ""}</div>
                </button>
                <button onClick={() => removeProfile(p.id)} className="mt-2 text-[10px] text-red-400 hover:text-red-300">Delete</button>
              </div>
            ))}
          </div>

          <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
            <div className="text-xs uppercase text-surface-500">Add Profile</div>
            <select value={profileForm.type} onChange={(e) => setProfileForm((s: any) => ({ ...s, type: e.target.value }))} className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
              <option value="local">Local folder</option>
              <option value="smb">SMB / NAS</option>
              <option value="webdav">WebDAV</option>
              <option value="storagebox">SFTP storage box</option>
              <option value="s3">S3-compatible bucket</option>
            </select>
            <input value={profileForm.name} onChange={(e) => setProfileForm((s: any) => ({ ...s, name: e.target.value }))} placeholder="Friendly name" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
            <input
              value={profileForm.path}
              onChange={(e) => setProfileForm((s: any) => ({ ...s, path: e.target.value }))}
              placeholder={profileForm.type === "local" ? "Local path, e.g. /Volumes/NAS" : "Optional root path inside the remote share"}
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200"
            />
            {profileForm.type === "local" && (
              <input value={profileForm.mount_path} onChange={(e) => setProfileForm((s: any) => ({ ...s, mount_path: e.target.value }))} placeholder="Optional mount path override" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
            )}
            {(profileForm.type === "smb" || profileForm.type === "storagebox") && (
              <>
                <input value={profileForm.remote} onChange={(e) => setProfileForm((s: any) => ({ ...s, remote: e.target.value }))} placeholder="//host/share or smb://host/share" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                <input value={profileForm.username} onChange={(e) => setProfileForm((s: any) => ({ ...s, username: e.target.value }))} placeholder="SMB username" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
                <input value={profileForm.password} onChange={(e) => setProfileForm((s: any) => ({ ...s, password: e.target.value }))} placeholder="SMB password" type="password" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
              </>
            )}
            {profileForm.type === "webdav" && (
              <>
                <input value={profileForm.endpoint} onChange={(e) => setProfileForm((s: any) => ({ ...s, endpoint: e.target.value }))} placeholder="https://dav.example.com/remote.php/dav/files/user" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                <input value={profileForm.username} onChange={(e) => setProfileForm((s: any) => ({ ...s, username: e.target.value }))} placeholder="WebDAV username" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
                <input value={profileForm.password} onChange={(e) => setProfileForm((s: any) => ({ ...s, password: e.target.value }))} placeholder="WebDAV password" type="password" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
              </>
            )}
            {profileForm.type === "s3" && (
              <>
                <input value={profileForm.endpoint} onChange={(e) => setProfileForm((s: any) => ({ ...s, endpoint: e.target.value }))} placeholder="Endpoint" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                <input value={profileForm.bucket} onChange={(e) => setProfileForm((s: any) => ({ ...s, bucket: e.target.value }))} placeholder="Bucket" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                <input value={profileForm.region} onChange={(e) => setProfileForm((s: any) => ({ ...s, region: e.target.value }))} placeholder="Region (optional)" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                <input value={profileForm.access_key} onChange={(e) => setProfileForm((s: any) => ({ ...s, access_key: e.target.value }))} placeholder="Access key" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                <input value={profileForm.secret_key} onChange={(e) => setProfileForm((s: any) => ({ ...s, secret_key: e.target.value }))} placeholder="Secret key" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
              </>
            )}
            <select value={profileForm.container_mount_mode} onChange={(e) => setProfileForm((s: any) => ({ ...s, container_mount_mode: e.target.value }))} className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
              <option value="none">No container mount</option>
              <option value="host">Host tasks only</option>
              <option value="guests">Guest tasks only</option>
              <option value="all">Host and guest tasks</option>
            </select>
            <input value={profileForm.container_path} onChange={(e) => setProfileForm((s: any) => ({ ...s, container_path: e.target.value }))} placeholder="Container path, e.g. /mnt/storagebox" className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
            <textarea value={profileForm.notes} onChange={(e) => setProfileForm((s: any) => ({ ...s, notes: e.target.value }))} placeholder="Notes: mount command, users, relay path, etc." rows={3} className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
            <button onClick={saveProfile} className="w-full px-3 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">Save profile</button>
            {saveMsg && <div className="text-xs text-red-400">{saveMsg}</div>}
          </div>

          <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
            <div className="flex items-center justify-between">
              <div className="text-xs uppercase text-surface-500">Guest ACL</div>
              <button onClick={refreshGuestConfigs} className="px-2 py-1 text-[11px] rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
            </div>
            {guestConfigs.length === 0 && <div className="text-xs text-surface-500">No accepted guests with config yet.</div>}
            {guestConfigs.map((cfg: any) => (
              <div key={cfg.guestUserId} className="rounded-lg border border-surface-800 bg-surface-900/50 p-2 space-y-2">
                <div className="text-sm text-surface-200">{cfg.guestEmail}</div>
                <div className="space-y-1">
                  {profiles.map((p: any) => {
                    const checked = (cfg.allowedSharedStorage || []).includes(p.id);
                    return (
                      <label key={`${cfg.guestUserId}-${p.id}`} className="flex items-center gap-2 text-xs text-surface-300">
                        <input type="checkbox" checked={checked} onChange={(e) => toggleGuestStorage(cfg.guestEmail, p.id, e.target.checked)} />
                        <span className="truncate">{p.name}</span>
                      </label>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="space-y-3">
          <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
            <div className="text-xs uppercase text-surface-500">Browse Shared Storage</div>
            {!selectedProfile ? (
              <div className="text-sm text-surface-500">Pick a profile on the left.</div>
            ) : (
              <>
                <div className="flex gap-2">
                  <input value={sharedPath} onChange={(e) => setSharedPath(e.target.value)} placeholder="path inside profile" className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
                  <button onClick={() => setSharedPath(sharedPath.split("/").slice(0, -1).join("/"))} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">Up</button>
                </div>
                {(sharedListing?.entries || []).map((entry: any) => (
                  <button key={entry.path} onClick={() => setSharedPath(entry.isDir ? entry.path : sharedPath)} className="w-full flex items-center gap-3 rounded-lg border border-surface-800 bg-surface-900/50 p-2 text-left text-xs">
                    <span>{entry.isDir ? "DIR" : "FILE"}</span>
                    <span className="flex-1 truncate font-mono">{entry.path}</span>
                    <span className="text-surface-500">{fmtBytes(entry.size || 0)}</span>
                  </button>
                ))}
              </>
            )}
          </div>

          <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
            <div className="text-xs uppercase text-surface-500">Document Search</div>
            <div className="flex gap-2">
              <input value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} onKeyDown={(e) => e.key === "Enter" && runSearch()} placeholder="Search filenames and text documents" className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
              <button onClick={runSearch} className="px-3 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">Search</button>
            </div>
            {searchHits.map((hit: any, i: number) => (
              <div key={`${hit.profileId}-${hit.path}-${i}`} className="rounded-lg border border-surface-800 bg-surface-900/50 p-2 text-xs">
                <div className="flex gap-2">
                  <span className="text-indigo-300">{hit.profileName}</span>
                  <span className="text-surface-500">{hit.matchType}</span>
                  <span className="ml-auto text-surface-500">{fmtBytes(hit.size || 0)}</span>
                </div>
                <div className="mt-1 font-mono text-surface-200 break-all">{hit.path}</div>
                {hit.snippet && <div className="mt-1 text-surface-500">{hit.snippet}</div>}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function Jobs({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.jobsList(directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  return (
    <div className="space-y-2">
      <div className="text-xs text-surface-500">Source: {data.source}</div>
      {(!data.jobs || data.jobs.length === 0) && <div className="text-xs text-surface-500">No scheduled jobs.</div>}
      {(data.jobs || []).map((j: any, i: number) => (
        <div key={i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
          <div className="flex gap-2">
            <span className="font-mono text-indigo-300">{j.name}</span>
            <span className="text-surface-500">{j.kind}</span>
            {j.schedule && <span className="text-surface-400 font-mono">{j.schedule}</span>}
            {j.status && <span className="ml-auto text-surface-500">{j.status}</span>}
          </div>
          {j.target && <div className="text-[10px] font-mono text-surface-500 mt-1 truncate">{j.target}</div>}
          {j.nextRun && <div className="text-[10px] text-surface-600">next: {j.nextRun}</div>}
        </div>
      ))}
    </div>
  );
}

function Logs({ directory }: { directory: string }) {
  const [service, setService] = useState("postgres");
  const [lines, setLines] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  function start() {
    esRef.current?.close();
    setLines([]);
    const es = new EventSource(agentClient.logsSseUrl(service));
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.onmessage = (e) => setLines((ls) => [...ls.slice(-999), e.data]);
    esRef.current = es;
  }

  useEffect(() => () => esRef.current?.close(), []);

  return (
    <div className="space-y-2">
      <div className="flex gap-2 items-center">
        <input value={service} onChange={(e) => setService(e.target.value)}
          placeholder="service name (e.g. postgres)"
          className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
        <button onClick={start} className="px-3 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">Stream</button>
        <span className={`text-xs ${connected ? "text-emerald-400" : "text-surface-500"}`}>{connected ? "●" : "○"}</span>
      </div>
      <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[500px]">{lines.join("\n")}</pre>
    </div>
  );
}

function Cost({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.switchCost(directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  const ests = (data.estimates || []).sort((a: any, b: any) => a.monthly - b.monthly);
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">
        Project usage: DB {fmtBytes((data.usage?.dbSizeMb || 0) * 1024 * 1024)} · Storage {fmtBytes((data.usage?.storageMb || 0) * 1024 * 1024)}
      </div>
      <div className="space-y-1">
        {ests.map((e: any) => (
          <div key={e.target} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-sm">
            <span className={`px-1.5 py-0.5 rounded text-[9px] uppercase ${e.freeTierOk ? "bg-emerald-500/20 text-emerald-300" : "bg-amber-500/20 text-amber-300"}`}>{e.tier}</span>
            <span className="flex-1 font-mono text-surface-200">{e.label}</span>
            <span className="text-surface-300 font-mono">${e.monthly.toFixed(2)}/mo</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(1)} ${units[i]}`;
}
