/**
 * Contributor Dogfood mode.
 *
 * The installed native app remains the control plane. Any signed-in user can
 * render a verified Yaver source checkout from their own primary device. The
 * attached page receives a narrow, short-lived capability; normal bearer auth
 * still protects the box; the canonical main branch is protected by the agent.
 */

import React, { useCallback, useEffect, useState } from "react";
import { Alert, DeviceEventEmitter, Linking, Pressable, ScrollView, Switch, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import AttachModeSection from "../../src/components/AttachModeSection";
import { useColors } from "../../src/context/ThemeContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { useAuth } from "../../src/context/AuthContext";
import { useRouteParamsCompat } from "../../src/lib/useRouteParamsCompat";
import { useDogfoodOverlay } from "../../src/context/DogfoodOverlayContext";
import {
  DogfoodNativeMenu,
  getDogfoodEntryIconHidden,
  setDogfoodEntryIconHidden,
} from "../../../sdk/feedback/react-native/src";
import {
  listDogfoodApps,
  listDogfoodCatalog,
  listDogfoodInstallations,
  listDogfoodTesters,
  registerThisDogfoodControlDevice,
  saveDogfoodApp,
  setDogfoodTester,
  setDogfoodInstallationAction,
  type DogfoodAppRow,
  type DogfoodCatalogRow,
  type DogfoodInstallationRow,
  type DogfoodTesterRow,
} from "../../src/lib/dogfoodRegistry";

function ExpandableCard({
  title,
  subtitle,
  countLabel,
  defaultOpen = false,
  c,
  children,
}: {
  title: string;
  subtitle: string;
  countLabel?: string;
  defaultOpen?: boolean;
  c: ReturnType<typeof useColors>;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 16, padding: 16, marginBottom: 14 }}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`${open ? "Hide" : "Show"} ${title}`}
        onPress={() => setOpen((value) => !value)}
        style={{ flexDirection: "row", alignItems: "flex-start", gap: 12 }}
      >
        <View style={{ flex: 1 }}>
          <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700" }}>{title}</Text>
          <Text style={{ color: c.textSecondary, marginTop: 6, lineHeight: 19 }}>{subtitle}</Text>
        </View>
        <View style={{ alignItems: "flex-end", gap: 6 }}>
          {countLabel ? <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>{countLabel}</Text> : null}
          <Text style={{ color: c.textMuted, fontSize: 12 }}>{open ? "Hide" : "Manage"}</Text>
        </View>
      </Pressable>
      {open ? <View style={{ marginTop: 14 }}>{children}</View> : null}
    </View>
  );
}

function DeveloperManagementScreen() {
  const router = useRouter();
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const { token } = useAuth();
  const [apps, setApps] = useState<DogfoodAppRow[]>([]);
  const [catalog, setCatalog] = useState<DogfoodCatalogRow[]>([]);
  const [installations, setInstallations] = useState<DogfoodInstallationRow[]>([]);
  const [testers, setTesters] = useState<DogfoodTesterRow[]>([]);
  const [appId, setAppId] = useState("");
  const [appLabel, setAppLabel] = useState("");
  const [projectSlug, setProjectSlug] = useState("");
  const [testerAppId, setTesterAppId] = useState("");
  const [testerEmail, setTesterEmail] = useState("");
  const [deviceStatus, setDeviceStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const pendingInstallations = installations.filter((row) => row.status === "pending");
  const activeInstallations = installations.filter((row) => row.status === "active");
  const activeTesters = testers.filter((row) => row.status === "active");

  const refresh = useCallback(async () => {
    if (!token) return;
    const [nextApps, nextInstallations, nextCatalog, nextTesters] = await Promise.all([
      listDogfoodApps(token), listDogfoodInstallations(token), listDogfoodCatalog(token), listDogfoodTesters(token),
    ]);
    setApps(nextApps);
    setTesterAppId((current) => current && nextApps.some((app) => app.appId === current) ? current : nextApps[0]?.appId || "");
    setInstallations(nextInstallations);
    setCatalog(nextCatalog);
    setTesters(nextTesters);
  }, [token]);

  useEffect(() => { void refresh().catch(() => {}); }, [refresh]);

  const registerDevice = async () => {
    if (!token || busy) return;
    setBusy(true);
    try {
      const result = await registerThisDogfoodControlDevice(token);
      setDeviceStatus(`Registered · generation ${result.generation}${result.assurance === "browser-dev" ? " · browser test identity" : ""}`);
    } catch (error) {
      Alert.alert("Couldn't register this device", error instanceof Error ? error.message : "Try again.");
    } finally { setBusy(false); }
  };

  const createApp = async () => {
    if (!token || busy) return;
    setBusy(true);
    try {
      await saveDogfoodApp(token, { appId: appId.trim(), label: appLabel.trim(), projectSlug: projectSlug.trim() || undefined, allowedScopes: ["feedback", "blackbox"], enabled: true });
      setAppId(""); setAppLabel(""); setProjectSlug("");
      await refresh();
    } catch (error) {
      Alert.alert("Couldn't enable Dogfood", error instanceof Error ? error.message : "Try again.");
    } finally { setBusy(false); }
  };

  const act = async (row: DogfoodInstallationRow, action: "approve" | "cancel" | "revoke") => {
    if (!token || busy) return;
    setBusy(true);
    try { await setDogfoodInstallationAction(token, row._id, action); await refresh(); }
    catch (error) { Alert.alert("Dogfood device wasn't updated", error instanceof Error ? error.message : "Try again."); }
    finally { setBusy(false); }
  };

  const updateTester = async (email: string, enabled: boolean, app = testerAppId) => {
    if (!token || busy || !app) return;
    setBusy(true);
    try {
      await setDogfoodTester(token, app, email, enabled);
      if (enabled) setTesterEmail("");
      await refresh();
    } catch (error) {
      Alert.alert("Dogfood access wasn't updated", error instanceof Error ? error.message : "Try again.");
    } finally { setBusy(false); }
  };

  const card = { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 16, padding: 16, marginBottom: 14 } as const;
  const input = { color: c.textPrimary, borderColor: c.border, borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10, marginTop: 8 } as const;
  const button = { alignSelf: "flex-start" as const, backgroundColor: c.accent, borderRadius: 10, paddingHorizontal: 14, paddingVertical: 10, marginTop: 12 };

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Developer management" onBack={() => router.navigate("/(tabs)/settings" as any)} />
      <ScrollView contentContainerStyle={[{ padding: 16, paddingBottom: 40 }, tabletContent]}>
        <View style={card}>
          <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "800" }}>Developer management</Text>
          <Text style={{ color: c.textSecondary, marginTop: 6, lineHeight: 19 }}>
            Keep phone UI light. Register this phone as the approval device, then handle app access, installs, and QR handoff from focused management sections.
          </Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 14 }}>
            <View style={{ borderColor: c.border, borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, minWidth: 112 }}>
              <Text style={{ color: c.accent, fontSize: 18, fontWeight: "800" }}>{apps.length}</Text>
              <Text style={{ color: c.textMuted, marginTop: 2, fontSize: 12 }}>Apps</Text>
            </View>
            <View style={{ borderColor: c.border, borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, minWidth: 112 }}>
              <Text style={{ color: c.accent, fontSize: 18, fontWeight: "800" }}>{activeTesters.length}</Text>
              <Text style={{ color: c.textMuted, marginTop: 2, fontSize: 12 }}>Trusted accounts</Text>
            </View>
            <View style={{ borderColor: c.border, borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, minWidth: 112 }}>
              <Text style={{ color: c.accent, fontSize: 18, fontWeight: "800" }}>{pendingInstallations.length}</Text>
              <Text style={{ color: c.textMuted, marginTop: 2, fontSize: 12 }}>Pending installs</Text>
            </View>
          </View>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 14 }}>
            <Pressable accessibilityRole="button" accessibilityLabel="Scan a device or TV QR" style={button} onPress={() => router.push({ pathname: "/approve-device", params: { scan: "1" } })}>
              <Text style={{ color: "white", fontWeight: "700" }}>Scan approval QR</Text>
            </Pressable>
            <Pressable accessibilityRole="button" accessibilityLabel="Claim a Yaver device by QR" style={[button, { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1 }]} onPress={() => router.push("/provision-add")}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Register device</Text>
            </Pressable>
            <Pressable accessibilityRole="button" accessibilityLabel="Open secure handoff" style={[button, { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1 }]} onPress={() => router.push("/secure-handoff")}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Secure handoff</Text>
            </Pressable>
          </View>
          {deviceStatus ? <Text style={{ color: c.textSecondary, marginTop: 10 }}>{deviceStatus}</Text> : null}
        </View>

        <ExpandableCard
          title="This phone"
          subtitle="Register this installation as the approval device. The private key stays on-device; only the public key reaches Yaver."
          countLabel={deviceStatus ? "Ready" : "Setup"}
          defaultOpen
          c={c}
        >
          <Pressable accessibilityRole="button" accessibilityLabel="Register this device for developer management" style={button} onPress={() => void registerDevice()} disabled={busy}>
            <Text style={{ color: "white", fontWeight: "700" }}>{busy ? "Working…" : "Register this phone"}</Text>
          </Pressable>
        </ExpandableCard>

        {catalog.length ? <ExpandableCard
          title="Set up apps on this phone"
          subtitle="Open a supported app, complete Yaver sign-in there, and register that installation for owner approval. No token is placed in the link."
          countLabel={`${catalog.length} ready`}
          c={c}
        >
          {catalog.map((app) => <View key={app.appId} style={{ borderTopColor: c.border, borderTopWidth: 1, marginTop: 14, paddingTop: 12 }}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{app.label}</Text>
            <Text style={{ color: c.textSecondary, marginTop: 3 }}>{app.appId}</Text>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Set up ${app.label} on this phone`}
              style={button}
              onPress={() => void Linking.openURL(app.activationUrl).catch(() => Alert.alert("App unavailable", `Install ${app.label} on this phone, then try again.`))}
            >
              <Text style={{ color: "white", fontWeight: "700" }}>Set up on this phone</Text>
            </Pressable>
          </View>)}
        </ExpandableCard> : null}

        <ExpandableCard
          title="Third-party apps"
		  subtitle="Register an app that embeds DogfoodSettings and DogfoodUsage. Reload Only and Reload + Chat both use Yaver OAuth plus an approved installation; Feedback and BlackBox stay the default scopes."
          countLabel={`${apps.length} apps`}
          c={c}
        >
          <TextInput accessibilityLabel="Dogfood app id" value={appId} onChangeText={setAppId} placeholder="App ID, e.g. io.example.app" placeholderTextColor={c.textMuted} autoCapitalize="none" style={input} />
          <TextInput accessibilityLabel="Dogfood app label" value={appLabel} onChangeText={setAppLabel} placeholder="App name" placeholderTextColor={c.textMuted} style={input} />
          <TextInput accessibilityLabel="Dogfood project slug" value={projectSlug} onChangeText={setProjectSlug} placeholder="Project slug (optional)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={input} />
          <Pressable accessibilityRole="button" accessibilityLabel="Enable third-party app" style={button} onPress={() => void createApp()} disabled={busy || !appId.trim() || !appLabel.trim()}>
            <Text style={{ color: "white", fontWeight: "700" }}>Enable app</Text>
          </Pressable>
          {apps.map((app) => <View key={app._id} style={{ borderTopColor: c.border, borderTopWidth: 1, marginTop: 14, paddingTop: 12 }}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{app.label}</Text>
            <Text style={{ color: c.textSecondary, marginTop: 3 }}>{app.appId} · {app.allowedScopes.join(", ")}</Text>
          </View>)}
        </ExpandableCard>

        {apps.length ? <ExpandableCard
          title="Trusted accounts"
          subtitle="Allow a Yaver account by email for one app. Revoking access also cancels or revokes that account's installations."
          countLabel={`${activeTesters.length} active`}
          c={c}
        >
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 2 }}>
            {apps.map((app) => <Pressable
              key={app.appId}
              accessibilityRole="button"
              accessibilityLabel={`Manage trusted accounts for ${app.label}`}
              onPress={() => setTesterAppId(app.appId)}
              style={{ borderColor: testerAppId === app.appId ? c.accent : c.border, borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 7 }}
            ><Text style={{ color: testerAppId === app.appId ? c.accent : c.textSecondary, fontWeight: "600" }}>{app.label}</Text></Pressable>)}
          </View>
          <TextInput accessibilityLabel="Dogfood tester email" value={testerEmail} onChangeText={setTesterEmail} placeholder="tester@example.com" placeholderTextColor={c.textMuted} autoCapitalize="none" keyboardType="email-address" style={input} />
          <Pressable accessibilityRole="button" accessibilityLabel="Allow trusted account" style={button} onPress={() => void updateTester(testerEmail.trim(), true)} disabled={busy || !testerAppId || !testerEmail.trim()}>
            <Text style={{ color: "white", fontWeight: "700" }}>Allow account</Text>
          </Pressable>
          {testers.filter((row) => row.appId === testerAppId).map((row) => <View key={row._id} style={{ borderTopColor: c.border, borderTopWidth: 1, marginTop: 12, paddingTop: 12 }}>
            <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{row.tester?.name || row.testerEmail}</Text>
            <Text style={{ color: c.textSecondary, marginTop: 3 }}>{row.testerEmail} · {row.status}{row.testerUserId ? " · Yaver account linked" : " · activates after sign-in"}</Text>
            <Pressable accessibilityRole="button" accessibilityLabel={`${row.status === "active" ? "Revoke" : "Restore"} ${row.testerEmail}`} onPress={() => void updateTester(row.testerEmail, row.status !== "active", row.appId)} style={{ paddingVertical: 10 }}>
              <Text style={{ color: row.status === "active" ? c.error : c.accent }}>{row.status === "active" ? "Revoke access" : "Restore access"}</Text>
            </Pressable>
          </View>)}
        </ExpandableCard> : null}

        {installations.length ? <ExpandableCard
          title="Install approvals"
          subtitle="Approve verified requests, cancel enrollment, or revoke active access. Gesture and onboarding details stay here instead of on the landing view."
          countLabel={`${pendingInstallations.length} pending · ${activeInstallations.length} active`}
          c={c}
        >
          {installations.map((row) => <View key={row._id} style={{ borderTopColor: c.border, borderTopWidth: 1, marginTop: 12, paddingTop: 12 }}>
            <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{row.label || row.platform} · {row.appId}</Text>
            <Text style={{ color: c.textSecondary, marginTop: 3 }}>{row.status}{row.proofVerifiedAt ? " · key verified" : " · awaiting key proof"}</Text>
            {row.tester ? <Text style={{ color: c.textSecondary, marginTop: 3 }}>{row.tester.name} · {row.tester.email}</Text> : null}
            {row.status === "active" ? <Text style={{ color: c.textSecondary, marginTop: 3 }}>
              {row.gestureSupported === true
                ? `Three-finger supported · ${row.controlPresentation === "auto" ? "gesture mode" : "Y mode"}`
                : row.gestureSupported === false ? "Y mode · gesture unavailable" : "Control capability not reported yet"}
              {row.controlOnboardingSeenAt ? " · onboarded" : " · onboarding pending"}
            </Text> : null}
            {row.status === "pending" && row.proofVerifiedAt ? <Pressable accessibilityRole="button" accessibilityLabel={`Approve ${row.label || row.platform}`} style={button} onPress={() => void act(row, "approve")}><Text style={{ color: "white", fontWeight: "700" }}>Approve</Text></Pressable> : null}
            {row.status === "pending" ? <Pressable accessibilityRole="button" accessibilityLabel={`Cancel ${row.label || row.platform}`} onPress={() => void act(row, "cancel")} style={{ paddingVertical: 10 }}><Text style={{ color: c.error }}>Cancel enrollment</Text></Pressable> : null}
            {row.status === "active" ? <Pressable accessibilityRole="button" accessibilityLabel={`Revoke ${row.label || row.platform}`} onPress={() => void act(row, "revoke")} style={{ paddingVertical: 10 }}><Text style={{ color: c.error }}>Revoke</Text></Pressable> : null}
          </View>)}
        </ExpandableCard> : null}
      </ScrollView>
    </View>
  );
}

/**
 * Keep the contributor entry surface intentionally tiny. App registration,
 * tester access, and installation approvals are a separate Settings layer;
 * none of that inventory belongs in the box/runner/checkout decision.
 */
export default function DogfoodScreen() {
  const router = useRouter();
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const { management, view } = useRouteParamsCompat<{ management?: string; view?: string }>();
  const runtime = useDogfoodOverlay();
  const [entryIconVisible, setEntryIconVisible] = useState(true);

  useEffect(() => {
    void getDogfoodEntryIconHidden("io.yaver.mobile:native")
      .then((hidden) => setEntryIconVisible(!hidden))
      .catch(() => setEntryIconVisible(true));
  }, []);

  const setEntryIcon = useCallback(async (visible: boolean) => {
    setEntryIconVisible(visible);
    await setDogfoodEntryIconHidden(!visible, "io.yaver.mobile:native");
    DeviceEventEmitter.emit("yaverFeedback:dogfoodEntryIconChanged", {
      visible,
      scope: "io.yaver.mobile:native",
    });
  }, []);

  if (management === "1") return <DeveloperManagementScreen />;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title={view === "settings" ? "Dogfood Settings" : "Dogfood"} onBack={() => view === "settings" ? router.setParams({ view: undefined }) : router.navigate("/(tabs)/more" as any)} />
      <ScrollView contentContainerStyle={[{ padding: 16, paddingBottom: 40 }, tabletContent]}>
        {view === "settings" ? <>
          <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 16, padding: 16, marginBottom: 12 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
              <View style={{ flex: 1 }}>
                <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "800" }}>Show Y over the app</Text>
                <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 17, marginTop: 3 }}>On by default. The Y is the only control placed over the running app.</Text>
              </View>
              <Switch value={entryIconVisible} onValueChange={(value) => void setEntryIcon(value)} />
            </View>
            {runtime.active ? <Pressable
              accessibilityRole="button"
              accessibilityLabel={runtime.busy ? "Stop Dogfood" : "Exit Dogfood"}
              onPress={() => void runtime.end()}
              style={({ pressed }) => ({ minHeight: 44, marginTop: 12, borderRadius: 12, alignItems: "center", justifyContent: "center", borderWidth: 1, borderColor: c.border, opacity: pressed ? 0.6 : 1 })}
            ><Text style={{ color: c.error, fontWeight: "700" }}>{runtime.busy ? "Stop Dogfood" : "Exit Dogfood"}</Text></Pressable> : null}
          </View>
          <Text style={{ color: c.textSecondary, marginBottom: 12, lineHeight: 19 }}>
            Choose the remote box, runner, checkout, and render lane. Reload renders the current working tree without changing Git.
          </Text>
          <AttachModeSection c={c} surface="settings" />
        </> : <DogfoodNativeMenu
          active={runtime.active}
          busy={runtime.busy}
          status={runtime.status}
          usageMode={runtime.request?.usageMode}
          issue={runtime.issue?.message}
          onFixIssue={runtime.issue?.fix ? () => { void runtime.issue?.fix?.(); } : undefined}
          colors={{ card: c.bgCard, border: c.border, text: c.textPrimary, muted: c.textMuted, accent: c.accent, danger: c.error }}
          launchContent={<AttachModeSection c={c} surface="usage" />}
          onReload={() => { void runtime.reload("fast").catch((error) => Alert.alert("Dogfood reload failed", error instanceof Error ? error.message : String(error))); }}
          onExit={() => { void runtime.end(); }}
          onOpenTasks={runtime.goTasks}
          onOpenSettings={() => router.setParams({ view: "settings" })}
        />}
      </ScrollView>
    </View>
  );
}
