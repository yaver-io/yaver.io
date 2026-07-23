// app/connections.tsx — People & Shared Projects. The social graph (address
// book) plus the invite-to-code wrapper: connect with someone, then share a
// repo so they can code with you on your machine or a Yaver Cloud box. A
// non-technical collaborator gets an AI agent, their own branch, and a PR
// flow — no terminal, no tokens.

import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useFocusEffect, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useColors } from "../src/context/ThemeContext";
import type { ThemeColors } from "../src/constants/colors";
import { AppBackButton } from "../src/components/AppBackButton";
import { HIDE_PAID_UI } from "../src/lib/launchFlags";
import {
  listConnections,
  requestConnection,
  acceptConnection,
  removeConnection,
  suggestedConnections,
  type ConnectionsResponse,
  type SuggestedConnection,
} from "../src/lib/connections";
import {
  listProjectShares,
  createProjectShare,
  inviteToProject,
  acceptProjectShare,
  revokeProjectMember,
  archiveProjectShare,
  type OwnedProjectShare,
  type JoinedProjectShare,
} from "../src/lib/projectShares";

type Section = "people" | "projects";

function friendlyError(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  if (/network request failed|failed to fetch|load failed/i.test(msg)) {
    return "Couldn't reach the server. Check your connection.";
  }
  return msg;
}

export default function ConnectionsScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const s = makeStyles(c);

  const [section, setSection] = useState<Section>("people");
  const [conns, setConns] = useState<ConnectionsResponse>({ accepted: [], incoming: [], outgoing: [], blocked: [] });
  const [suggested, setSuggested] = useState<SuggestedConnection[]>([]);
  const [owned, setOwned] = useState<OwnedProjectShare[]>([]);
  const [joined, setJoined] = useState<JoinedProjectShare[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  // people input
  const [target, setTarget] = useState("");
  // projects input
  const [joinCode, setJoinCode] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [slug, setSlug] = useState("");
  const [repoUrl, setRepoUrl] = useState("");
  const [hostKind, setHostKind] = useState<"owner-device" | "managed-cloud">("owner-device");
  const [payer, setPayer] = useState<"owner" | "invitee">("owner");

  const flash = (m: { type: "ok" | "error"; text: string }) => {
    setMsg(m);
    setTimeout(() => setMsg(null), 3500);
  };

  const refresh = useCallback(async () => {
    try {
      const [cn, sg, ps] = await Promise.all([
        listConnections(),
        suggestedConnections().catch(() => []),
        listProjectShares().catch(() => ({ owned: [], joined: [] })),
      ]);
      setConns(cn);
      setSuggested(sg);
      setOwned(ps.owned);
      setJoined(ps.joined);
    } catch (e) {
      flash({ type: "error", text: friendlyError(e) });
    } finally {
      setLoading(false);
    }
  }, []);

  useFocusEffect(useCallback(() => { void refresh(); }, [refresh]));

  async function act(fn: () => Promise<void>, okText: string) {
    setBusy(true);
    try { await fn(); flash({ type: "ok", text: okText }); await refresh(); }
    catch (e) { flash({ type: "error", text: friendlyError(e) }); }
    finally { setBusy(false); }
  }

  async function sendConnect() {
    const q = target.trim();
    if (!q) return;
    await act(async () => {
      const isEmail = q.includes("@");
      const res = await requestConnection(isEmail ? { peerEmail: q, source: "manual" } : { peerUserId: q, source: "manual" });
      flash({ type: "ok", text: res.status === "accepted" ? "Connected!" : "Request sent." });
      setTarget("");
    }, "Done.");
  }

  async function createProject() {
    if (!slug.trim() || !repoUrl.trim()) return;
    await act(async () => {
      await createProjectShare({
        slug: slug.trim(),
        repoUrl: repoUrl.trim(),
        hostKind,
        payer: hostKind === "managed-cloud" ? payer : undefined,
      });
      setShowCreate(false); setSlug(""); setRepoUrl("");
    }, "Project created.");
  }

  async function joinProject() {
    const code = joinCode.trim();
    if (!code) return;
    await act(async () => {
      const res = await acceptProjectShare(code);
      flash({ type: "ok", text: `Joined ${res.slug || "project"} as ${res.role || "member"}.` });
      setJoinCode("");
    }, "Joined.");
  }

  return (
    <KeyboardAvoidingView style={{ flex: 1, backgroundColor: c.bg }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <View style={[s.header, { paddingTop: insets.top + 8 }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={[s.title, { color: c.textPrimary }]}>People & Projects</Text>
        <View style={{ width: 36 }} />
      </View>

      <View style={s.segment}>
        {(["people", "projects"] as Section[]).map((sec) => (
          <Pressable key={sec} onPress={() => setSection(sec)} style={[s.segBtn, section === sec && { backgroundColor: c.bgCard }]}>
            <Text style={{ color: section === sec ? c.textPrimary : c.textMuted, fontSize: 13, fontWeight: "600" }}>
              {sec === "people" ? "People" : "Shared Projects"}
            </Text>
          </Pressable>
        ))}
      </View>

      {msg && (
        <View style={[s.banner, { backgroundColor: msg.type === "ok" ? c.successBg : c.errorBg }]}>
          <Text style={{ color: msg.type === "ok" ? c.success : c.error, fontSize: 12 }}>{msg.text}</Text>
        </View>
      )}

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: insets.bottom + 32 }}>
        {loading ? (
          <ActivityIndicator color={c.accent} style={{ marginTop: 40 }} />
        ) : section === "people" ? (
          <>
            <Text style={[s.label, { color: c.textMuted }]}>Add by email or user id</Text>
            <View style={s.row}>
              <TextInput
                value={target}
                onChangeText={setTarget}
                placeholder="friend@example.com"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                style={[s.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
              />
              <Pressable onPress={sendConnect} disabled={busy || !target.trim()} style={[s.primaryBtn, { backgroundColor: c.accent, opacity: busy || !target.trim() ? 0.4 : 1 }]}>
                <Text style={[s.primaryBtnText, { color: c.textInverse }]}>Connect</Text>
              </Pressable>
            </View>

            {conns.incoming.length > 0 && (
              <Section title={`Requests (${conns.incoming.length})`} c={c} s={s}>
                {conns.incoming.map((p) => (
                  <View key={p.peerUserId} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                    <View style={{ flex: 1 }}>
                      <Text style={[s.name, { color: c.textPrimary }]}>{p.fullName}</Text>
                      <Text style={[s.sub, { color: c.textMuted }]}>{p.email}</Text>
                    </View>
                    <Pressable onPress={() => act(() => acceptConnection(p.peerUserId), "Connected!")} disabled={busy} style={[s.pill, { backgroundColor: c.successBg }]}>
                      <Text style={{ color: c.success, fontSize: 12, fontWeight: "600" }}>Accept</Text>
                    </Pressable>
                    <Pressable onPress={() => act(() => removeConnection(p.peerUserId), "Declined.")} disabled={busy} style={[s.pill, { backgroundColor: c.neutralBg }]}>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>Decline</Text>
                    </Pressable>
                  </View>
                ))}
              </Section>
            )}

            <Section title={`Connections (${conns.accepted.length})`} c={c} s={s} empty={conns.accepted.length === 0 ? "No connections yet." : undefined}>
              {conns.accepted.map((p) => (
                <View key={p.peerUserId} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <View style={{ flex: 1 }}>
                    <Text style={[s.name, { color: c.textPrimary }]}>{p.nickname || p.fullName}</Text>
                    <Text style={[s.sub, { color: c.textMuted }]}>{p.email}</Text>
                  </View>
                  <Pressable onPress={() => act(() => removeConnection(p.peerUserId), "Removed.")} disabled={busy} style={[s.pill, { backgroundColor: c.neutralBg }]}>
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>Remove</Text>
                  </Pressable>
                </View>
              ))}
            </Section>

            {conns.outgoing.length > 0 && (
              <Section title={`Pending (${conns.outgoing.length})`} c={c} s={s}>
                {conns.outgoing.map((p) => (
                  <View key={p.peerUserId} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                    <View style={{ flex: 1 }}>
                      <Text style={[s.name, { color: c.textPrimary }]}>{p.fullName}</Text>
                      <Text style={[s.sub, { color: c.textMuted }]}>Request sent</Text>
                    </View>
                    <Pressable onPress={() => act(() => removeConnection(p.peerUserId), "Cancelled.")} disabled={busy} style={[s.pill, { backgroundColor: c.neutralBg }]}>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>Cancel</Text>
                    </Pressable>
                  </View>
                ))}
              </Section>
            )}

            {suggested.length > 0 && (
              <Section title="Suggested" c={c} s={s}>
                {suggested.map((p) => (
                  <View key={p.userId} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                    <View style={{ flex: 1 }}>
                      <Text style={[s.name, { color: c.textPrimary }]}>{p.fullName}</Text>
                      <Text style={[s.sub, { color: c.textMuted }]}>{p.email} · via {p.source}</Text>
                    </View>
                    <Pressable onPress={() => act(() => requestConnection({ peerUserId: p.userId, source: "suggested" }).then(() => {}), "Request sent.")} disabled={busy} style={[s.pill, { backgroundColor: c.accentSoft }]}>
                      <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Connect</Text>
                    </Pressable>
                  </View>
                ))}
              </Section>
            )}
          </>
        ) : (
          <>
            <View style={s.row}>
              <Pressable onPress={() => setShowCreate((v) => !v)} style={[s.primaryBtn, { backgroundColor: c.accent }]}>
                <Text style={[s.primaryBtnText, { color: c.textInverse }]}>+ Share a project</Text>
              </Pressable>
            </View>
            <View style={s.row}>
              <TextInput
                value={joinCode}
                onChangeText={setJoinCode}
                placeholder="Have a code? Join"
                placeholderTextColor={c.textMuted}
                autoCapitalize="characters"
                style={[s.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
              />
              <Pressable onPress={joinProject} disabled={busy || !joinCode.trim()} style={[s.pill, { backgroundColor: c.bgCard, opacity: busy || !joinCode.trim() ? 0.4 : 1 }]}>
                <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>Join</Text>
              </Pressable>
            </View>

            {showCreate && (
              <View style={[s.createCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <TextInput value={slug} onChangeText={setSlug} placeholder="Project name (acme-app)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border, marginBottom: 8 }]} />
                <TextInput value={repoUrl} onChangeText={setRepoUrl} placeholder="Repo URL (github.com/me/acme-app)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border, marginBottom: 8 }]} />
                <Text style={[s.label, { color: c.textMuted }]}>Where does work run?</Text>
                <View style={[s.row, { marginBottom: 8 }]}>
                  <Choice label="My machine" active={hostKind === "owner-device"} onPress={() => setHostKind("owner-device")} c={c} s={s} />
                  {/* HN-LAUNCH-HIDE-PAID: hide the managed (Yaver-billed)
                      "Yaver Cloud" host option; BYO "My machine" stays. Flip
                      HIDE_PAID_UI in src/lib/launchFlags.ts to restore. */}
                  {!HIDE_PAID_UI && (
                    <Choice label="Yaver Cloud" active={hostKind === "managed-cloud"} onPress={() => setHostKind("managed-cloud")} c={c} s={s} />
                  )}
                </View>
                {!HIDE_PAID_UI && hostKind === "managed-cloud" && (
                  <View style={[s.row, { marginBottom: 8 }]}>
                    <Choice label="I pay" active={payer === "owner"} onPress={() => setPayer("owner")} c={c} s={s} />
                    <Choice label="They pay" active={payer === "invitee"} onPress={() => setPayer("invitee")} c={c} s={s} />
                  </View>
                )}
                <Pressable onPress={createProject} disabled={busy || !slug.trim() || !repoUrl.trim()} style={[s.primaryBtn, { backgroundColor: c.accent, opacity: busy || !slug.trim() || !repoUrl.trim() ? 0.4 : 1 }]}>
                  <Text style={[s.primaryBtnText, { color: c.textInverse }]}>Create</Text>
                </Pressable>
              </View>
            )}

            {owned.length === 0 && joined.length === 0 && (
              <Text style={[s.sub, { color: c.textMuted, marginTop: 16 }]}>No shared projects yet.</Text>
            )}

            {owned.map((p) => (
              <OwnedCard key={p.shareId} share={p} c={c} s={s} busy={busy} act={act} />
            ))}

            {joined.length > 0 && (
              <Section title="Shared with me" c={c} s={s}>
                {joined.map((p) => (
                  <View key={p.shareId} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border, alignItems: "flex-start" }]}>
                    <View style={{ flex: 1 }}>
                      <Text style={[s.name, { color: c.textPrimary }]}>{p.slug} · {p.role}</Text>
                      <Text style={[s.sub, { color: c.textMuted }]}>{p.repoUrl}</Text>
                      <Text style={[s.sub, { color: c.textMuted }]}>from {p.ownerName} · branch {p.branch || "—"}</Text>
                    </View>
                  </View>
                ))}
              </Section>
            )}
          </>
        )}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Section({ title, children, c, s, empty }: { title: string; children?: React.ReactNode; c: ThemeColors; s: any; empty?: string }) {
  return (
    <View style={{ marginTop: 20 }}>
      <Text style={[s.sectionLabel, { color: c.textMuted }]}>{title}</Text>
      {empty ? <Text style={[s.sub, { color: c.textMuted }]}>{empty}</Text> : children}
    </View>
  );
}

const ROLE_BLURB: Record<"dev" | "normie" | "viewer", string> = {
  dev: "Codes, pushes to a feature branch, opens PRs, can deploy.",
  normie: "Codes with AI on their own branch. Cannot deploy.",
  viewer: "Observes only — no code changes.",
};

function Choice({ label, active, onPress, c, s }: { label: string; active: boolean; onPress: () => void; c: ThemeColors; s: any }) {
  return (
    <Pressable onPress={onPress} style={[s.choice, { borderColor: active ? c.accent : c.border, backgroundColor: active ? c.accentSoft : "transparent" }]}>
      <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 13, fontWeight: "600" }}>{label}</Text>
    </Pressable>
  );
}

function OwnedCard({ share, c, s, busy, act }: { share: OwnedProjectShare; c: ThemeColors; s: any; busy: boolean; act: (fn: () => Promise<void>, ok: string) => Promise<void> }) {
  const [invitee, setInvitee] = useState("");
  const [role, setRole] = useState<"dev" | "normie" | "viewer">("normie");
  const others = share.roster.filter((m) => m.role !== "owner");

  async function invite() {
    const q = invitee.trim();
    if (!q) return;
    const isEmail = q.includes("@");
    await act(async () => {
      await inviteToProject({ shareId: share.shareId, ...(isEmail ? { peerEmail: q } : { peerUserId: q }), role });
      setInvitee("");
    }, `Invited as ${role}.`);
  }

  return (
    <View style={[s.createCard, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 16 }]}>
      <View style={{ flexDirection: "row", alignItems: "flex-start" }}>
        <View style={{ flex: 1 }}>
          <Text style={[s.name, { color: c.textPrimary }]}>{share.slug}</Text>
          <Text style={[s.sub, { color: c.textMuted }]}>{share.repoUrl}</Text>
          <Text style={[s.sub, { color: c.textMuted }]}>
            {share.hostKind === "managed-cloud" ? `Yaver Cloud (${share.payer === "invitee" ? "they pay" : "you pay"})` : "Your machine"}
          </Text>
        </View>
        <View style={[s.codePill, { backgroundColor: c.neutralBg }]}>
          <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}>{share.shareCode}</Text>
        </View>
      </View>

      <View style={[s.row, { marginTop: 12 }]}>
        <TextInput value={invitee} onChangeText={setInvitee} placeholder="Invite by email / user id" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]} />
        <Pressable onPress={invite} disabled={busy || !invitee.trim()} style={[s.pill, { backgroundColor: c.accent, opacity: busy || !invitee.trim() ? 0.4 : 1 }]}>
          <Text style={{ color: c.textInverse, fontSize: 13, fontWeight: "600" }}>Invite</Text>
        </Pressable>
      </View>
      <View style={[s.row, { marginTop: 8 }]}>
        {(["normie", "dev", "viewer"] as const).map((r) => (
          <Choice key={r} label={r} active={role === r} onPress={() => setRole(r)} c={c} s={s} />
        ))}
      </View>
      {/* Mirrors web CollabView's ROLE_BLURB. Keep both to what the agent
          actually enforces (desktop/agent/guest_project_role.go) — the role
          picker shipped for months with no enforcement behind it at all. */}
      <Text style={[s.sub, { color: c.textMuted, marginTop: 6 }]}>{ROLE_BLURB[role]}</Text>

      {others.map((m) => (
        <View key={m.userId || m.email} style={[s.memberRow, { borderTopColor: c.borderSubtle }]}>
          <View style={{ flex: 1 }}>
            <Text style={[s.name, { color: c.textPrimary }]}>{m.fullName} · {m.role}</Text>
            <Text style={[s.sub, { color: c.textMuted }]}>{m.status === "invited" ? "Invited — not yet joined" : `branch ${m.branch || "—"}`}</Text>
          </View>
          {m.userId ? (
            <Pressable onPress={() => act(() => revokeProjectMember(share.shareId, m.userId), "Removed.")} disabled={busy} style={[s.pill, { backgroundColor: c.neutralBg }]}>
              <Text style={{ color: c.textMuted, fontSize: 12 }}>Remove</Text>
            </Pressable>
          ) : null}
        </View>
      ))}

      <Pressable onPress={() => act(() => archiveProjectShare(share.shareId), "Archived.")} disabled={busy} style={{ marginTop: 12, alignSelf: "flex-end" }}>
        <Text style={{ color: c.textMuted, fontSize: 12 }}>Archive project</Text>
      </Pressable>
    </View>
  );
}

function makeStyles(c: ThemeColors) {
  return StyleSheet.create({
    header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 12, paddingBottom: 8 },
    title: { fontSize: 17, fontWeight: "700" },
    segment: { flexDirection: "row", marginHorizontal: 16, marginBottom: 8, backgroundColor: c.bgInput, borderRadius: 10, padding: 3 },
    segBtn: { flex: 1, alignItems: "center", paddingVertical: 8, borderRadius: 8 },
    banner: { marginHorizontal: 16, marginBottom: 8, paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8 },
    label: { fontSize: 12, fontWeight: "600", marginBottom: 6 },
    sectionLabel: { fontSize: 11, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 8 },
    row: { flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 4 },
    input: { flex: 1, borderWidth: 1, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14 },
    primaryBtn: { paddingHorizontal: 16, paddingVertical: 10, borderRadius: 8, alignItems: "center" },
    primaryBtnText: { fontSize: 14, fontWeight: "600" },
    pill: { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, marginLeft: 6 },
    card: { flexDirection: "row", alignItems: "center", borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10, marginBottom: 8 },
    createCard: { borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 4 },
    choice: { flex: 1, borderWidth: 1, borderRadius: 8, paddingVertical: 8, alignItems: "center" },
    codePill: { paddingHorizontal: 10, paddingVertical: 6, borderRadius: 8 },
    memberRow: { flexDirection: "row", alignItems: "center", borderTopWidth: 1, paddingTop: 10, marginTop: 10 },
    name: { fontSize: 14, fontWeight: "600" },
    sub: { fontSize: 11, marginTop: 2 },
  });
}
