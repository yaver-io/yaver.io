import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  Share,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as Clipboard from "expo-clipboard";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useAuth } from "../../src/context/AuthContext";
import { useDevice } from "../../src/context/DeviceContext";
import { QuicClient, quicClient } from "../../src/lib/quic";
import {
  acceptGuestByCode,
  acceptGuestInvitation,
  findInviteByCode,
  inviteGuest,
  listGuests,
  lookupPublicUser,
  revokeGuest,
  type GuestInfo,
  type InvitationPreview,
  type PublicUserLookup,
} from "../../src/lib/guests";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";

// Guest access — one mobile screen that covers host (my guests) and guest
// (join as guest) flows. Hosts can invite by email OR by public user id,
// optionally pre-scoping the invitation to a subset of their machines.
// Guests see pending invites with the host's proposed scope and can trim it
// further before accepting.

type Tab = "my-guests" | "join";

function tunnelServersForDevice(device: { id: string; name: string; tunnelUrl?: string; publicEndpoints?: string[] }) {
  const seen = new Set<string>();
  const out: Array<{ id: string; url: string; label: string; priority: number }> = [];
  const add = (url: string, priority: number, label: string) => {
    const trimmed = url.trim().replace(/\/+$/, "");
    if (!trimmed || seen.has(trimmed)) return;
    seen.add(trimmed);
    out.push({ id: `tunnel-${device.id}-${out.length}`, url: trimmed, label, priority });
  };
  (device.publicEndpoints ?? []).forEach((url, index) => add(url, index, `${device.name} endpoint #${index + 1}`));
  if (device.tunnelUrl) add(device.tunnelUrl, out.length, `${device.name} shared tunnel`);
  return out.length > 0 ? out : undefined;
}

async function fetchProjectsFromDevice(
  device: {
    id: string;
    name: string;
    host: string;
    port: number;
    lanIps?: string[];
    tunnelUrl?: string;
    publicEndpoints?: string[];
  },
  token: string,
): Promise<string[]> {
  const client = new QuicClient();
  client.setRelayServers(quicClient.getRelayServers().map((relay) => ({ ...relay })));
  try {
    await client.connect(
      device.host,
      device.port,
      token,
      device.id,
      device.lanIps,
      tunnelServersForDevice(device),
    );
    const projects = await client.listProjects().catch(() => []);
    return Array.from(
      new Set(
        projects
          .map((project) => String(project?.name || "").trim())
          .filter(Boolean),
      ),
    ).sort((a, b) => a.localeCompare(b));
  } finally {
    client.disconnect();
  }
}

export default function GuestsScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const tabletContent = useTabletContentStyle("wide");
  const { token } = useAuth();
  const {
    devices,
    activeDevice,
    guestInvitations,
    activeHosts,
    leaveHost,
    acceptGuestInvitation: ctxAcceptPending,
    refreshDevices,
  } = useDevice();
  /** hostEmail of the active host whose "Remove my access" awaits confirm. */
  const [leavingHost, setLeavingHost] = useState<string | null>(null);

  const [mode, setMode] = useState<Tab>("my-guests");
  const [guests, setGuests] = useState<GuestInfo[]>([]);
  const [loading, setLoading] = useState(false);

  // ─── Invite (host side) ──────────────────────────────────────
  const [inviteKind, setInviteKind] = useState<"email" | "user-id">("email");
  const [inviteTarget, setInviteTarget] = useState("");
  const [inviteLookup, setInviteLookup] = useState<PublicUserLookup | null>(null);
  const [inviteLookupErr, setInviteLookupErr] = useState<string | null>(null);
  const [inviteProposedDeviceIds, setInviteProposedDeviceIds] = useState<string[]>([]);
  const [inviteScope, setInviteScope] = useState<"full" | "feedback-only" | "sdk-project">("full");
  // Tester-tier only: let the invited friend improve the app with AI (vibe).
  const [inviteCanVibe, setInviteCanVibe] = useState(false);
  const [inviteProjects, setInviteProjects] = useState<string[]>([]);
  const [inviteProjectChoices, setInviteProjectChoices] = useState<string[]>([]);
  const [inviteProjectsLoading, setInviteProjectsLoading] = useState(false);
  const [inviteProjectsError, setInviteProjectsError] = useState<string | null>(null);
  const [inviteProjectsSource, setInviteProjectsSource] = useState<string | null>(null);
  const [inviting, setInviting] = useState(false);
  const [lastCode, setLastCode] = useState<string | null>(null);
  const [lastTarget, setLastTarget] = useState<string | null>(null);

  // Only show own (non-guest) devices in the picker.
  const ownDevices = useMemo(() => devices.filter((d) => !d.isGuest), [devices]);
  const inviteSelectedDevices = useMemo(
    () => ownDevices.filter((d) => inviteProposedDeviceIds.includes(d.id)),
    [ownDevices, inviteProposedDeviceIds],
  );
  const activeOwnDevice = useMemo(
    () => activeDevice && !activeDevice.isGuest ? activeDevice : null,
    [activeDevice],
  );

  // ─── Join flow (guest side) ──────────────────────────────────
  const [joinCode, setJoinCode] = useState("");
  const [previewing, setPreviewing] = useState(false);
  const [preview, setPreview] = useState<InvitationPreview | null>(null);
  const [previewErr, setPreviewErr] = useState<string | null>(null);
  const [approvedDeviceIds, setApprovedDeviceIds] = useState<string[]>([]);
  const [joining, setJoining] = useState(false);

  // ─── Pending invite approval (guest side) ─────────────────────
  const [approveCtx, setApproveCtx] = useState<{
    inviteId: string;
    hostUserId: string;
    code?: string;
    hostName: string;
    hostEmail: string;
    preview: InvitationPreview | null;
    approvedDeviceIds: string[];
    loading: boolean;
  } | null>(null);

  const loadGuests = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    try {
      setGuests(await listGuests(token));
    } catch {
      /* ignore */
    }
    setLoading(false);
  }, [token]);

  useEffect(() => {
    loadGuests();
  }, [loadGuests]);

  // Debounced user-id lookup whenever the host is inviting by user id.
  useEffect(() => {
    if (inviteKind !== "user-id") {
      setInviteLookup(null);
      setInviteLookupErr(null);
      return;
    }
    const v = inviteTarget.trim();
    if (!token || v.length < 3) {
      setInviteLookup(null);
      setInviteLookupErr(null);
      return;
    }
    let alive = true;
    const t = setTimeout(async () => {
      try {
        const r = await lookupPublicUser(token, v);
        if (!alive) return;
        if (r) {
          setInviteLookup(r);
          setInviteLookupErr(null);
        } else {
          setInviteLookup(null);
          setInviteLookupErr("No Yaver user with that id");
        }
      } catch (e: any) {
        if (!alive) return;
        setInviteLookup(null);
        setInviteLookupErr(e?.message || "Lookup failed");
      }
    }, 400);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [inviteKind, inviteTarget, token]);

  async function invite() {
    if (!token) return;
    const v = inviteTarget.trim();
    if (!v) return;
    setInviting(true);
    try {
      const payload =
        inviteKind === "email"
          ? { email: v, deviceIds: inviteProposedDeviceIds, scope: inviteScope, allowedProjects: inviteProjects, canVibe: inviteScope === "sdk-project" ? inviteCanVibe : undefined }
          : { userId: v, deviceIds: inviteProposedDeviceIds, scope: inviteScope, allowedProjects: inviteProjects, canVibe: inviteScope === "sdk-project" ? inviteCanVibe : undefined };
      const r = await inviteGuest(token, payload);
      setLastCode(r.inviteCode);
      setLastTarget(inviteKind === "email" ? v : inviteLookup?.email || ("user " + v));
      setInviteTarget("");
      setInviteLookup(null);
      setInviteProposedDeviceIds([]);
      setInviteProjects([]);
      setInviteProjectChoices([]);
      setInviteProjectsError(null);
      setInviteProjectsSource(null);
      loadGuests();
    } catch (e: any) {
      Alert.alert("Invite failed", e?.message || String(e));
    }
    setInviting(false);
  }

  async function revoke(g: GuestInfo) {
    Alert.alert(
      "Revoke access?",
      g.email || `user ${g.userId}`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Revoke",
          style: "destructive",
          onPress: async () => {
            if (!token) return;
            try {
              await revokeGuest(token, g.email ? { email: g.email } : { userId: g.userId });
              loadGuests();
            } catch (e: any) {
              Alert.alert("Couldn't Revoke Access", `Yaver couldn't revoke this guest's access. Check your connection and try again.\n\n${e?.message || String(e)}`);
            }
          },
        },
      ],
    );
  }

  async function copyCode(code: string) {
    await Clipboard.setStringAsync(code);
    Alert.alert("Copied", code);
  }

  async function shareInvite(code: string, target?: string) {
    const msg = target
      ? `Hey — here's your Yaver access code for ${target}: ${code}\n\nDownload: https://yaver.io/download · expires in 2 days`
      : `Your Yaver invite code: ${code}`;
    try {
      await Share.share({ message: msg });
    } catch {
      /* noop */
    }
  }

  function toggleProposedDevice(deviceId: string) {
    setInviteProposedDeviceIds((prev) =>
      prev.includes(deviceId) ? prev.filter((x) => x !== deviceId) : [...prev, deviceId],
    );
  }

  function toggleInviteProject(project: string) {
    setInviteProjects((prev) =>
      prev.includes(project) ? prev.filter((item) => item !== project) : [...prev, project],
    );
  }

  async function loadInviteProjects() {
    if (!token || inviteSelectedDevices.length === 0) return;
    setInviteProjectsLoading(true);
    setInviteProjectsError(null);
    setInviteProjectChoices([]);
    setInviteProjects([]);
    setInviteProjectsSource(null);
    try {
      const settled = await Promise.allSettled(
        inviteSelectedDevices.map((device) => fetchProjectsFromDevice(device, token)),
      );
      const merged = new Set<string>();
      let successCount = 0;
      let failureCount = 0;
      for (const result of settled) {
        if (result.status === "fulfilled") {
          successCount += 1;
          for (const project of result.value) merged.add(project);
        } else {
          failureCount += 1;
        }
      }
      const choices = [...merged].sort((a, b) => a.localeCompare(b));
      setInviteProjectChoices(choices);
      setInviteProjectsSource(inviteSelectedDevices.map((device) => device.name).join(", "));
      if (choices.length === 0) {
        setInviteProjectsError("No projects were detected on the selected machine(s).");
      } else if (failureCount > 0 && successCount > 0) {
        setInviteProjectsError("Loaded projects from some selected machines, but at least one machine did not respond.");
      } else if (failureCount > 0) {
        setInviteProjectsError("Could not load projects from the selected machine(s).");
      }
    } catch (e: any) {
      setInviteProjectsError(e?.message || "Failed to load projects");
    }
    setInviteProjectsLoading(false);
  }

  useEffect(() => {
    setInviteProjects([]);
    setInviteProjectChoices([]);
    setInviteProjectsError(null);
    setInviteProjectsSource(null);
  }, [inviteProposedDeviceIds.join("|")]);

  async function previewCode() {
    if (!token || joinCode.trim().length < 4) return;
    setPreviewing(true);
    setPreviewErr(null);
    setPreview(null);
    try {
      const code = joinCode.trim().toUpperCase();
      const p = await findInviteByCode(token, code);
      setPreview(p);
      // Default approved = proposed (or all host devices if nothing was proposed)
      const defaults =
        p.proposedDeviceIds && p.proposedDeviceIds.length > 0
          ? p.proposedDeviceIds
          : p.hostDevices.map((d) => d.deviceId);
      setApprovedDeviceIds(defaults);
    } catch (e: any) {
      setPreviewErr(e?.message || String(e));
    }
    setPreviewing(false);
  }

  async function commitCodeAccept() {
    if (!token || !preview) return;
    setJoining(true);
    try {
      await acceptGuestByCode(token, preview.inviteCode, approvedDeviceIds);
      setJoinCode("");
      setPreview(null);
      setApprovedDeviceIds([]);
      Alert.alert("Joined", "Host machine should now appear in your device list.");
      refreshDevices();
    } catch (e: any) {
      Alert.alert("Couldn't Join", `Yaver couldn't accept the invite. Double-check the code, then check your connection and try again.\n\n${e?.message || String(e)}`);
    }
    setJoining(false);
  }

  function toggleApprovedDevice(deviceId: string) {
    setApprovedDeviceIds((prev) =>
      prev.includes(deviceId) ? prev.filter((x) => x !== deviceId) : [...prev, deviceId],
    );
  }

  async function startApprovePending(inv: typeof guestInvitations[number]) {
    if (!token) return;
    // Open the approval sheet. If we have a code, fetch rich preview so the guest
    // sees the host's devices; otherwise fall back to whatever fields are on the
    // invitation itself.
    let p: InvitationPreview | null = null;
    setApproveCtx({
      inviteId: inv.inviteId || inv._id || "",
      hostUserId: inv.hostUserId,
      code: inv.inviteCode,
      hostName: inv.hostName,
      hostEmail: inv.hostEmail,
      preview: null,
      approvedDeviceIds: inv.proposedDeviceIds ?? [],
      loading: true,
    });
    try {
      if (inv.inviteCode) {
        p = await findInviteByCode(token, inv.inviteCode);
      }
    } catch {
      /* ignore — we'll show the fallback */
    }
    setApproveCtx((curr) =>
      curr
        ? {
            ...curr,
            preview: p,
            approvedDeviceIds:
              p
                ? (p.proposedDeviceIds && p.proposedDeviceIds.length > 0
                    ? p.proposedDeviceIds
                    : p.hostDevices.map((d) => d.deviceId))
                : curr.approvedDeviceIds,
            loading: false,
          }
        : curr,
    );
  }

  function toggleApprovePendingDevice(deviceId: string) {
    setApproveCtx((curr) =>
      curr
        ? {
            ...curr,
            approvedDeviceIds: curr.approvedDeviceIds.includes(deviceId)
              ? curr.approvedDeviceIds.filter((x) => x !== deviceId)
              : [...curr.approvedDeviceIds, deviceId],
          }
        : curr,
    );
  }

  async function commitPendingAccept() {
    if (!approveCtx) return;
    try {
      await ctxAcceptPending(approveCtx.hostUserId, approveCtx.approvedDeviceIds);
      Alert.alert("Joined", `You now have access to ${approveCtx.hostName}'s machine.`);
      setApproveCtx(null);
      refreshDevices();
    } catch (e: any) {
      Alert.alert("Couldn't Join", `Yaver couldn't complete joining this machine. Check your connection and try again.\n\n${e?.message || String(e)}`);
    }
  }

  const sectionLabel = { color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" } as const;

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Guest Access" onBack={() => router.navigate("/(tabs)/more" as any)} style={{ paddingTop: insets.top + 12 }} />

      <View style={[{ flexDirection: "row", padding: 12, gap: 8 }, tabletContent]}>
        <ModeBtn c={c} label="My guests" active={mode === "my-guests"} onPress={() => setMode("my-guests")} />
        <ModeBtn c={c} label="Join as guest" active={mode === "join"} onPress={() => setMode("join")} />
      </View>

      <ScrollView contentContainerStyle={[{ padding: 16, paddingBottom: 40, gap: 12 }, tabletContent]}>
        {mode === "my-guests" ? (
          <>
            <View style={[card(c), { gap: 10 }]}>
              <Text style={sectionLabel as any}>Invite a guest</Text>

              {/* Choose email vs userId */}
              <View style={{ flexDirection: "row", gap: 8 }}>
                <ChipToggle
                  c={c}
                  label="By email"
                  active={inviteKind === "email"}
                  onPress={() => {
                    setInviteKind("email");
                    setInviteTarget("");
                    setInviteLookup(null);
                  }}
                />
                <ChipToggle
                  c={c}
                  label="By user ID"
                  active={inviteKind === "user-id"}
                  onPress={() => {
                    setInviteKind("user-id");
                    setInviteTarget("");
                    setInviteLookup(null);
                  }}
                />
              </View>

              <TextInput
                value={inviteTarget}
                onChangeText={setInviteTarget}
                placeholder={inviteKind === "email" ? "email@example.com" : "user id (ask them to copy from Settings)"}
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                keyboardType={inviteKind === "email" ? "email-address" : "default"}
                autoCorrect={false}
                style={[inputStyle(c), { fontFamily: "Menlo" }]}
              />

              {/* Resolved user preview */}
              {inviteKind === "user-id" && inviteLookup && (
                <Text style={{ color: c.textMuted, fontSize: 12 }}>
                  → {inviteLookup.fullName} ({inviteLookup.email})
                </Text>
              )}
              {inviteKind === "user-id" && inviteLookupErr && (
                <Text style={{ color: "#ef4444", fontSize: 12 }}>{inviteLookupErr}</Text>
              )}

                <View style={{ gap: 6 }}>
                  <Text style={sectionLabel as any}>Access tier</Text>
                  <View style={{ flexDirection: "row", gap: 8 }}>
                  <ChipToggle
                    c={c}
                    label="Yaver full"
                    active={inviteScope === "full"}
                    onPress={() => {
                      setInviteScope("full");
                      setInviteCanVibe(false);
                    }}
                  />
                  <ChipToggle
                    c={c}
                    label="Feedback SDK"
                    active={inviteScope === "feedback-only"}
                    onPress={() => {
                      setInviteScope("feedback-only");
                      setInviteCanVibe(false);
                    }}
                  />
                  <ChipToggle
                    c={c}
                    label="Tester"
                    active={inviteScope === "sdk-project"}
                    onPress={() => setInviteScope("sdk-project")}
                  />
                </View>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Full = tasks, vibing, projects, remote coding. Feedback SDK = feedback, blackbox, voice only. Tester = run your pre-release app on their device + feedback, narrowed to selected projects.
                </Text>

                {/* Tester-only: let the friend improve the app with AI. */}
                {inviteScope === "sdk-project" && (
                  <Pressable
                    onPress={() => setInviteCanVibe((v) => !v)}
                    style={{
                      flexDirection: "row",
                      alignItems: "center",
                      gap: 10,
                      marginTop: 4,
                      padding: 10,
                      borderRadius: 8,
                      borderWidth: 1,
                      borderColor: inviteCanVibe ? c.accent : c.border,
                      backgroundColor: inviteCanVibe ? c.accent + "15" : "transparent",
                    }}
                  >
                    <Text style={{ fontSize: 16 }}>{inviteCanVibe ? "✅" : "⬜️"}</Text>
                    <View style={{ flex: 1 }}>
                      <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                        Let them improve it with AI (vibe)
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        Runs on the selected remote box with scoped guest access. Changes commit straight to your branch.
                      </Text>
                    </View>
                  </Pressable>
                )}
              </View>

              {/* Machine picker */}
              {ownDevices.length > 0 && (
                <View style={{ gap: 6 }}>
                  <Text style={sectionLabel as any}>Share which machines?</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    Leave all unchecked to propose all of your machines. The guest can trim further when they accept.
                  </Text>
                  {activeOwnDevice ? (
                    <Pressable
                      onPress={() => setInviteProposedDeviceIds([activeOwnDevice.id])}
                      style={{
                        alignSelf: "flex-start",
                        paddingHorizontal: 12,
                        paddingVertical: 7,
                        borderRadius: 8,
                        backgroundColor: c.accent + "15",
                        borderWidth: 1,
                        borderColor: c.accent,
                      }}
                    >
                      <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                        Use current remote box: {activeOwnDevice.name}
                      </Text>
                    </Pressable>
                  ) : null}
                  {ownDevices.map((d) => {
                    const selected = inviteProposedDeviceIds.includes(d.id);
                    return (
                      <Pressable
                        key={d.id}
                        onPress={() => toggleProposedDevice(d.id)}
                        style={{
                          flexDirection: "row",
                          alignItems: "center",
                          padding: 8,
                          borderRadius: 8,
                          backgroundColor: selected ? c.accent + "15" : c.bg,
                          borderWidth: 1,
                          borderColor: selected ? c.accent : c.border,
                          gap: 10,
                        }}
                      >
                        <View
                          style={{
                            width: 18,
                            height: 18,
                            borderRadius: 4,
                            borderWidth: 2,
                            borderColor: selected ? c.accent : c.border,
                            backgroundColor: selected ? c.accent : "transparent",
                            alignItems: "center",
                            justifyContent: "center",
                          }}
                        >
                          {selected && <Text style={{ color: "#fff", fontSize: 12 }}>✓</Text>}
                        </View>
                        <View style={{ flex: 1 }}>
                          <Text style={{ color: c.textPrimary, fontSize: 13 }}>{d.name}</Text>
                          <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "Menlo" }}>
                            {d.id} · {d.os}
                          </Text>
                          <Text style={{ color: c.textMuted, fontSize: 10 }}>
                            {inviteProjects.length > 0
                              ? `Project slice: ${inviteProjects.join(", ")}`
                              : inviteProjectChoices.length > 0
                                ? "Projects loaded for this machine. Pick the ones to share below."
                                : "Load this machine's projects to narrow the share by project."}
                          </Text>
                        </View>
                      </Pressable>
                    );
                  })}
                </View>
              )}

              {inviteSelectedDevices.length > 0 && (
                <View style={{ gap: 8 }}>
                  <Text style={sectionLabel as any}>Project slice</Text>
                  <Pressable
                    onPress={loadInviteProjects}
                    disabled={inviteProjectsLoading}
                    style={[actionBtn(c), {
                      backgroundColor: c.accent + "15",
                      borderColor: c.accent,
                      borderWidth: 1,
                      opacity: inviteProjectsLoading ? 0.6 : 1,
                    }]}
                  >
                    {inviteProjectsLoading ? (
                      <ActivityIndicator color={c.accent} />
                    ) : (
                      <Text style={{ color: c.accent, fontWeight: "700" }}>
                        Load projects from selected machine{inviteSelectedDevices.length === 1 ? "" : "s"}
                      </Text>
                    )}
                  </Pressable>
                  {inviteProjectsSource ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Source: {inviteProjectsSource}
                    </Text>
                  ) : null}
                  {inviteProjectsError ? (
                    <Text style={{ color: inviteProjectChoices.length > 0 ? "#f59e0b" : "#ef4444", fontSize: 11 }}>
                      {inviteProjectsError}
                    </Text>
                  ) : (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Selected projects are stored on the invite and enforced as the guest's project slice.
                    </Text>
                  )}
                  {inviteProjectChoices.length > 0 && (
                    <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
                      {inviteProjectChoices.map((project) => (
                        <ChipToggle
                          key={project}
                          c={c}
                          label={project}
                          active={inviteProjects.includes(project)}
                          onPress={() => toggleInviteProject(project)}
                        />
                      ))}
                    </View>
                  )}
                </View>
              )}

              <Pressable
                onPress={invite}
                disabled={inviting || !inviteTarget.trim()}
                style={[actionBtn(c), {
                  backgroundColor: c.accent,
                  opacity: inviting || !inviteTarget.trim() ? 0.5 : 1,
                }]}
              >
                {inviting ? (
                  <ActivityIndicator color="#fff" />
                ) : (
                  <Text style={{ color: "#fff", fontWeight: "700" }}>Send invite</Text>
                )}
              </Pressable>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                Max 5 guests. Codes expire in 2 days. Full guests can code through Yaver but still cannot touch vault, sessions, or raw exec.
              </Text>
            </View>

            {lastCode && (
              <View
                style={[card(c), {
                  backgroundColor: c.accent + "15",
                  borderColor: c.accent,
                  borderWidth: 1,
                  gap: 10,
                }]}
              >
                <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", textTransform: "uppercase" }}>
                  New invite for {lastTarget}
                </Text>
                <Text
                  selectable
                  style={{
                    color: c.textPrimary,
                    fontFamily: "Menlo",
                    fontSize: 26,
                    letterSpacing: 4,
                    fontWeight: "700",
                    textAlign: "center",
                  }}
                >
                  {lastCode}
                </Text>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  <Pressable
                    onPress={() => copyCode(lastCode)}
                    style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 13 }}>Copy code</Text>
                  </Pressable>
                  <Pressable
                    onPress={() => shareInvite(lastCode, lastTarget || undefined)}
                    style={[actionBtn(c), { backgroundColor: c.accent, flex: 1 }]}
                  >
                    <Text style={{ color: "#fff", fontWeight: "700" }}>Share…</Text>
                  </Pressable>
                </View>
              </View>
            )}

            <Text style={[sectionLabel as any, { marginTop: 4 }]}>
              Active guests {guests.length > 0 && `(${guests.length}/5)`}
            </Text>
            {loading && <ActivityIndicator color={c.accent} />}
            {!loading && guests.length === 0 && (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>No active guests.</Text>
            )}
            {guests.map((g, idx) => (
              <View
                key={g.email || g.userId || String(idx)}
                style={[card(c), { flexDirection: "row", alignItems: "center" }]}
              >
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 13 }}>
                    {g.fullName || g.email || `user ${g.userId ?? ""}`}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>
                    {g.email ? g.email + " · " : ""}
                    {g.status}
                    {g.acceptedAt ? ` · granted ${new Date(g.acceptedAt).toLocaleDateString()}` : ""}
                  </Text>
                </View>
                {g.status === "pending" && g.inviteCode && (
                  <Pressable onPress={() => shareInvite(g.inviteCode!, g.email)}>
                    <Text style={{ color: c.accent, fontSize: 12, marginRight: 10 }}>Share</Text>
                  </Pressable>
                )}
                <Pressable onPress={() => revoke(g)}>
                  <Text style={{ color: "#ef4444", fontSize: 12 }}>Revoke</Text>
                </Pressable>
              </View>
            ))}
          </>
        ) : (
          <>
            {guestInvitations && guestInvitations.length > 0 && (
              <View style={[card(c), { gap: 8 }]}>
                <Text style={sectionLabel as any}>Pending invites</Text>
                {guestInvitations.map((inv) => (
                  <View
                    key={inv._id || inv.inviteId || inv.hostUserId}
                    style={{
                      padding: 10,
                      backgroundColor: c.accent + "15",
                      borderRadius: 8,
                      gap: 6,
                    }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 13 }}>
                      From{" "}
                      <Text style={{ fontFamily: "Menlo" }}>
                        {inv.hostEmail || inv.hostName}
                      </Text>
                    </Text>
                    {inv.proposedDeviceIds && inv.proposedDeviceIds.length > 0 && (
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        Scoped to {inv.proposedDeviceIds.length} machine
                        {inv.proposedDeviceIds.length === 1 ? "" : "s"}
                      </Text>
                    )}
                    <Pressable
                      onPress={() => startApprovePending(inv)}
                      style={[actionBtn(c), { backgroundColor: c.accent, paddingVertical: 8 }]}
                    >
                      <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>
                        Review & accept
                      </Text>
                    </Pressable>
                  </View>
                ))}
              </View>
            )}

            {/* Hosts I've already accepted. Mirrors the web dashboard's
                active-hosts list — this is the only place on the phone where a
                share can be dropped by host rather than by tapping one of its
                machines, and the anchor the phone was missing entirely. */}
            {activeHosts && activeHosts.length > 0 && (
              <View style={[card(c), { gap: 8 }]}>
                <Text style={sectionLabel as any}>Sharing with me</Text>
                {activeHosts.map((h) => (
                  <View
                    key={h.hostUserId || h.hostEmail}
                    style={{ padding: 10, backgroundColor: c.bgCard, borderRadius: 8, gap: 6 }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 13 }}>{h.hostName}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Menlo" }}>
                      {h.hostEmail}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Since {new Date(h.grantedAt).toLocaleDateString()}
                      {h.devices && h.devices.length > 0
                        ? ` · ${h.devices.length} machine${h.devices.length === 1 ? "" : "s"}`
                        : ""}
                    </Text>
                    {leavingHost === h.hostEmail ? (
                      <>
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>
                          Remove your access to every machine {h.hostName} shared with you? They
                          can share again later, and you can accept again.
                        </Text>
                        <View style={{ flexDirection: "row", gap: 8 }}>
                          <Pressable
                            onPress={async () => {
                              try {
                                const res = await leaveHost({ hostEmail: h.hostEmail });
                                setLeavingHost(null);
                                Alert.alert(
                                  "Access removed",
                                  `You no longer have access to ${res.hostName}'s machines.`,
                                );
                              } catch (e: any) {
                                Alert.alert("Error", e?.message || "Failed to remove access");
                              }
                            }}
                            style={[actionBtn(c), { backgroundColor: c.error, paddingVertical: 8, flex: 1 }]}
                          >
                            <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>
                              Yes, remove
                            </Text>
                          </Pressable>
                          <Pressable
                            onPress={() => setLeavingHost(null)}
                            style={[actionBtn(c), { paddingVertical: 8, flex: 1 }]}
                          >
                            <Text style={{ color: c.textMuted, fontWeight: "700", fontSize: 13 }}>
                              Cancel
                            </Text>
                          </Pressable>
                        </View>
                      </>
                    ) : (
                      <Pressable
                        onPress={() => setLeavingHost(h.hostEmail)}
                        style={[actionBtn(c), { paddingVertical: 8 }]}
                      >
                        <Text style={{ color: c.error, fontWeight: "700", fontSize: 13 }}>
                          Remove my access
                        </Text>
                      </Pressable>
                    )}
                  </View>
                ))}
              </View>
            )}

            {/* Approval sheet (inline) */}
            {approveCtx && (
              <View
                style={[card(c), {
                  gap: 10,
                  borderColor: c.accent,
                  borderWidth: 1,
                }]}
              >
                <Text style={sectionLabel as any}>
                  Accepting invite from {approveCtx.hostName}
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  {approveCtx.hostEmail}
                </Text>
                {approveCtx.loading ? (
                  <ActivityIndicator color={c.accent} />
                ) : approveCtx.preview && approveCtx.preview.hostDevices.length > 0 ? (
                  <>
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Uncheck any machines you do not want access to.
                    </Text>
                    {approveCtx.preview.hostDevices.map((d) => {
                      const selected = approveCtx.approvedDeviceIds.includes(d.deviceId);
                      return (
                        <Pressable
                          key={d.deviceId}
                          onPress={() => toggleApprovePendingDevice(d.deviceId)}
                          style={{
                            flexDirection: "row",
                            alignItems: "center",
                            padding: 8,
                            borderRadius: 8,
                            backgroundColor: selected ? c.accent + "10" : c.bg,
                            borderWidth: 1,
                            borderColor: selected ? c.accent : c.border,
                            gap: 10,
                          }}
                        >
                          <View
                            style={{
                              width: 18,
                              height: 18,
                              borderRadius: 4,
                              borderWidth: 2,
                              borderColor: selected ? c.accent : c.border,
                              backgroundColor: selected ? c.accent : "transparent",
                              alignItems: "center",
                              justifyContent: "center",
                            }}
                          >
                            {selected && <Text style={{ color: "#fff", fontSize: 12 }}>✓</Text>}
                          </View>
                          <View style={{ flex: 1 }}>
                            <Text style={{ color: c.textPrimary, fontSize: 13 }}>{d.name}</Text>
                            <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "Menlo" }}>
                              {d.deviceId} · {d.platform}
                              {d.proposed ? " · proposed" : ""}
                            </Text>
                          </View>
                        </Pressable>
                      );
                    })}
                  </>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 12 }}>
                    Host has no registered devices yet — you'll see them in your list as they come online.
                  </Text>
                )}
                <View style={{ flexDirection: "row", gap: 8 }}>
                  <Pressable
                    onPress={() => setApproveCtx(null)}
                    style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}
                  >
                    <Text style={{ color: c.textPrimary }}>Cancel</Text>
                  </Pressable>
                  <Pressable
                    onPress={commitPendingAccept}
                    style={[actionBtn(c), { backgroundColor: c.accent, flex: 1 }]}
                  >
                    <Text style={{ color: "#fff", fontWeight: "700" }}>Accept</Text>
                  </Pressable>
                </View>
              </View>
            )}

            <View style={[card(c), { gap: 8 }]}>
              <Text style={sectionLabel as any}>Enter invite code</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                Signed in with a different email than the host invited, or got the code by text? Paste it here.
              </Text>
              <TextInput
                value={joinCode}
                onChangeText={(v) => {
                  setJoinCode(v);
                  setPreview(null);
                  setPreviewErr(null);
                }}
                placeholder="ABC123"
                placeholderTextColor={c.textMuted}
                autoCapitalize="characters"
                autoCorrect={false}
                maxLength={10}
                style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 22, letterSpacing: 4, textAlign: "center" }]}
              />
              {!preview && (
                <Pressable
                  onPress={previewCode}
                  disabled={previewing || joinCode.trim().length < 4}
                  style={[actionBtn(c), {
                    backgroundColor: c.accent,
                    opacity: previewing || joinCode.trim().length < 4 ? 0.5 : 1,
                  }]}
                >
                  {previewing ? (
                    <ActivityIndicator color="#fff" />
                  ) : (
                    <Text style={{ color: "#fff", fontWeight: "700" }}>Preview</Text>
                  )}
                </Pressable>
              )}
              {previewErr && (
                <Text style={{ color: "#ef4444", fontSize: 12 }}>{previewErr}</Text>
              )}
              {preview && (
                <View style={{ gap: 10 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 13 }}>
                    From <Text style={{ fontFamily: "Menlo" }}>{preview.hostName}</Text>{" "}
                    <Text style={{ color: c.textMuted }}>({preview.hostEmail})</Text>
                  </Text>
                  {preview.hostDevices.length > 0 ? (
                    <>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        Accept which machines?
                      </Text>
                      {preview.hostDevices.map((d) => {
                        const selected = approvedDeviceIds.includes(d.deviceId);
                        return (
                          <Pressable
                            key={d.deviceId}
                            onPress={() => toggleApprovedDevice(d.deviceId)}
                            style={{
                              flexDirection: "row",
                              alignItems: "center",
                              padding: 8,
                              borderRadius: 8,
                              backgroundColor: selected ? c.accent + "10" : c.bg,
                              borderWidth: 1,
                              borderColor: selected ? c.accent : c.border,
                              gap: 10,
                            }}
                          >
                            <View
                              style={{
                                width: 18,
                                height: 18,
                                borderRadius: 4,
                                borderWidth: 2,
                                borderColor: selected ? c.accent : c.border,
                                backgroundColor: selected ? c.accent : "transparent",
                                alignItems: "center",
                                justifyContent: "center",
                              }}
                            >
                              {selected && <Text style={{ color: "#fff", fontSize: 12 }}>✓</Text>}
                            </View>
                            <View style={{ flex: 1 }}>
                              <Text style={{ color: c.textPrimary, fontSize: 13 }}>{d.name}</Text>
                              <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "Menlo" }}>
                                {d.deviceId} · {d.platform}
                                {d.proposed ? " · proposed" : ""}
                              </Text>
                            </View>
                          </Pressable>
                        );
                      })}
                    </>
                  ) : (
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>
                      Host has no registered devices yet.
                    </Text>
                  )}
                  <Pressable
                    onPress={commitCodeAccept}
                    disabled={joining}
                    style={[actionBtn(c), { backgroundColor: c.accent, opacity: joining ? 0.5 : 1 }]}
                  >
                    {joining ? (
                      <ActivityIndicator color="#fff" />
                    ) : (
                      <Text style={{ color: "#fff", fontWeight: "700" }}>Accept</Text>
                    )}
                  </Pressable>
                </View>
              )}
            </View>
          </>
        )}
      </ScrollView>
    </View>
  );
}

function ModeBtn({ c, label, active, onPress }: { c: any; label: string; active: boolean; onPress: () => void }) {
  return (
    <Pressable
      onPress={onPress}
      style={{
        flex: 1,
        paddingVertical: 8,
        borderRadius: 8,
        backgroundColor: active ? c.accent + "20" : c.bgCard,
        borderWidth: 1,
        borderColor: active ? c.accent : c.border,
        alignItems: "center",
      }}
    >
      <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 13, fontWeight: "700" }}>
        {label}
      </Text>
    </Pressable>
  );
}

function ChipToggle({ c, label, active, onPress }: { c: any; label: string; active: boolean; onPress: () => void }) {
  return (
    <Pressable
      onPress={onPress}
      style={{
        paddingVertical: 6,
        paddingHorizontal: 12,
        borderRadius: 16,
        backgroundColor: active ? c.accent + "20" : "transparent",
        borderWidth: 1,
        borderColor: active ? c.accent : c.border,
      }}
    >
      <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 12, fontWeight: "600" }}>
        {label}
      </Text>
    </Pressable>
  );
}

function card(c: any) {
  return {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
  } as const;
}

function actionBtn(c: any) {
  return {
    paddingVertical: 10,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
  } as const;
}

function inputStyle(c: any) {
  return {
    backgroundColor: c.bg,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    color: c.textPrimary,
  } as const;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
});
