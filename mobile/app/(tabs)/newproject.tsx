import React, { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice, type Device } from "../../src/context/DeviceContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { quicClient, type MobileWorkspaceGate, type WizardQuestion, type WizardSession } from "../../src/lib/quic";
import { createLocalPhoneProject } from "../../src/lib/phoneProjects";
import { setPendingVibingProject } from "../../src/lib/vibingStore";
import { spacing, typography } from "../../src/theme/tokens";
import {
  MOBILE_APP_PALETTES,
  MOBILE_APP_GIT_PROVIDERS,
  buildMobileAppBuilderPrompt,
  chooseBuilderRemote,
  projectSlug,
  type MobileAppPalette,
  type MobileAppGitProvider,
} from "../../src/lib/mobileAppBuilderFlow";

type Step = "name" | "device" | "git" | "palette" | "initializing";
const WIZARD_STEPS = ["name", "device", "git", "palette"] as const;

const INITIALIZATION_STEPS = [
  "Creating the Yaver workspace",
  "Adding the iOS and Android app",
  "Initializing Yaver Serverless",
  "Preparing chat and live rendering",
];

export default function NewProjectScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const tabletContent = useTabletContentStyle("regular");
  const {
    activeDevice,
    connectionStatus,
    devices,
    connectedDeviceIds,
    primaryDeviceId,
    selectDevice,
    codingMode,
    setCodingMode,
    primaryRunnerByDevice,
    primaryModelByDevice,
  } = useDevice();

  const [step, setStep] = useState<Step>("name");
  // undefined means the wizard has not applied its recommendation yet;
  // null is the user's explicit "This phone" choice.
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null | undefined>(undefined);
  const [projectName, setProjectName] = useState("");
  const [selectedGitProvider, setSelectedGitProvider] = useState<MobileAppGitProvider>("yaver-git");
  const [selectedPalette, setSelectedPalette] = useState<MobileAppPalette>(
    MOBILE_APP_PALETTES.find((palette) => palette.id === "ocean") ?? MOBILE_APP_PALETTES[0],
  );
  const [choosing, setChoosing] = useState(false);
  const [gitProbeLoading, setGitProbeLoading] = useState(false);
  const [gitGates, setGitGates] = useState<MobileWorkspaceGate[]>([]);
  const [gitProbeError, setGitProbeError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [initializationStage, setInitializationStage] = useState(0);

  const connectedIds = useMemo(() => {
    const ids = new Set(connectedDeviceIds);
    if (connectionStatus === "connected" && activeDevice) ids.add(activeDevice.id);
    return ids;
  }, [activeDevice, connectedDeviceIds, connectionStatus]);

  const recommendedRemote = useMemo(
    () => chooseBuilderRemote(devices, connectedIds, primaryDeviceId, activeDevice?.id),
    [activeDevice?.id, connectedIds, devices, primaryDeviceId],
  );

  useEffect(() => {
    if (selectedDeviceId === null) return;
    if (selectedDeviceId && connectedIds.has(selectedDeviceId)) return;
    setSelectedDeviceId(recommendedRemote?.id ?? null);
  }, [connectedIds, recommendedRemote, selectedDeviceId]);

  const selectedDevice = typeof selectedDeviceId === "string"
    ? devices.find((device) => device.id === selectedDeviceId) ?? null
    : null;
  const connectedRemotes = devices
    .filter((device) => connectedIds.has(device.id))
    .sort((left, right) => {
      if (left.id === recommendedRemote?.id) return -1;
      if (right.id === recommendedRemote?.id) return 1;
      return left.name.localeCompare(right.name);
    });
  const selectedRunner = selectedDevice
    ? primaryRunnerByDevice[selectedDevice.id] || "default runner"
    : "on-device runner";
  const selectedGitGate = gitGates.find((gate) => gate.id === selectedGitProvider);
  const selectedGitReady = !gitProbeLoading && !!selectedGitGate?.ready;
  const stepNumber = step === "initializing" ? WIZARD_STEPS.length : WIZARD_STEPS.indexOf(step) + 1;

  const probeGitIntegrations = async () => {
    if (!selectedDevice) {
      setGitGates([{ id: "yaver-git", code: "mobile_workspace.git.ready", label: "Yaver Git", ready: true, configured: true, detail: "Built in on this phone" }]);
      setGitProbeError(null);
      return;
    }
    setGitProbeLoading(true);
    setGitProbeError(null);
    try {
      const target = selectedDevice.id === activeDevice?.id ? undefined : selectedDevice.id;
      const probe = await quicClient.mobileWorkspaceStatusProbe(target);
      if (!probe.status) {
        throw new Error(probe.reason === "agent-upgrade-required" ? "Update the Yaver agent on this box to test Git integrations." : "The selected box did not answer the Git integration probe.");
      }
      setGitGates(probe.status.gitProviders);
    } catch (cause) {
      setGitGates([]);
      setGitProbeError(cause instanceof Error ? cause.message : "Could not test Git integrations.");
    } finally {
      setGitProbeLoading(false);
    }
  };

  useEffect(() => {
    if (step !== "git") return;
    void probeGitIntegrations();
  }, [step, selectedDevice?.id]);

  const continueFromName = () => {
    if (!projectName.trim()) {
      setError("Give the project a name first.");
      return;
    }
    Keyboard.dismiss();
    setError(null);
    setStep("device");
  };

  const continueFromDevice = async () => {
    setChoosing(true);
    setError(null);
    try {
      if (selectedDevice) {
        if (codingMode === "local-only") await setCodingMode("remote-preferred");
        if (activeDevice?.id !== selectedDevice.id) await selectDevice(selectedDevice);
      } else {
        await setCodingMode("local-only");
      }
      setStep("git");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not select that development location.");
    } finally {
      setChoosing(false);
    }
  };

  const answerWizard = async (sessionId: string, questionId: string, answer: string) => {
    const response = await quicClient.wizardAnswer(sessionId, questionId, answer);
    if (!response) throw new Error(`Project initialization stopped at ${questionId}.`);
    return response;
  };

  const initializeProject = async () => {
    setStep("initializing");
    setInitializationStage(0);
    setError(null);
    try {
      // "This phone" is a real local placement, not a connection to an agent.
      // Sending it through quicClient leaves the transport with an empty host
      // (http://:null) and used to crash the production wizard before it could
      // create anything. Keep the local lane entirely inside the phone sandbox.
      if (!selectedDevice) {
        setInitializationStage(1);
        const project = await createLocalPhoneProject({
          name: projectName.trim(),
          slug: projectSlug(projectName),
          template: "crud",
          app: {
            summary: `${projectName.trim()} mobile app`,
            brand: {
              displayName: projectName.trim(),
              palette: selectedPalette.id,
              primaryColor: selectedPalette.colors[0],
              secondaryColor: selectedPalette.colors[1],
            },
          },
          prompt: buildMobileAppBuilderPrompt(selectedPalette),
        });
        setInitializationStage(4);
        router.replace(`/phone-project/${project.slug}` as any);
        return;
      }

      const started = await quicClient.wizardStart();
      if (!started) throw new Error("The selected box could not start project initialization.");
      let session: WizardSession = started.session;
      let question: WizardQuestion | null = started.question;
      const answers: Record<string, string> = {
        app_name: projectName.trim(),
        slug: projectSlug(projectName),
        description: `${projectName.trim()} mobile app`,
        primary_color: selectedPalette.colors[0],
        secondary_color: selectedPalette.colors[1],
        accent_color: selectedPalette.colors[2],
        surface_color: selectedPalette.colors[3],
        tone: selectedPalette.id === "electric" ? "dark" : "system",
        include_web: "false",
        include_mobile: "true",
        include_backend: "true",
        include_landing: "false",
        backend: "sqlite",
        mobile_stack: "expo-rn",
        git_provider: selectedGitProvider,
        git_visibility: "private",
        git_repo_name: projectSlug(projectName),
      };

      setInitializationStage(1);
      for (const [questionId, answer] of Object.entries(answers)) {
        const response = await answerWizard(session.id, questionId, answer);
        session = response.session;
        question = response.question;
      }

      setInitializationStage(2);
      let guard = 0;
      while (question && question.kind !== "done" && guard < 100) {
        const answer = question.id === "confirm" ? "true" : question.default ?? "";
        const response = await answerWizard(session.id, question.id, answer);
        session = response.session;
        question = response.question;
        guard += 1;
      }
      if (guard >= 100) throw new Error("Project initialization did not reach a finished state.");

      setInitializationStage(3);
      const result = await quicClient.wizardGenerate(session.id);
      if (!result?.ok || !result.directory) throw new Error("The selected box could not create the project.");
      setPendingVibingProject(result.directory);
      setInitializationStage(4);
      router.replace({
        pathname: "/(tabs)/tasks",
        params: {
          dir: result.directory,
          title: projectName.trim(),
          prompt: buildMobileAppBuilderPrompt(selectedPalette, selectedDevice?.name),
          autoSubmit: "1",
          hideInitialPrompt: "1",
          selectProject: "1",
          sessionStartedFrom: "new-application",
        },
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Project initialization failed.");
    }
  };

  return (
    <View style={[styles.screen, { backgroundColor: c.bg }]}>
      <AppScreenHeader
        title="Create an app"
        onBack={() => {
          if (step === "palette") setStep("git");
          else if (step === "git") setStep("device");
          else if (step === "device") setStep("name");
          else if (step !== "initializing") router.navigate("/(tabs)/more" as any);
        }}
        style={{ paddingTop: insets.top + 12 }}
      />

      <KeyboardAvoidingView
        style={styles.keyboardAvoider}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        keyboardVerticalOffset={insets.top}
      >
        <ScrollView
          contentContainerStyle={[styles.content, tabletContent]}
          showsVerticalScrollIndicator={false}
          keyboardDismissMode="interactive"
          keyboardShouldPersistTaps="handled"
        >
        {step !== "initializing" ? (
          <View
            accessibilityRole="progressbar"
            accessibilityLabel={`Step ${stepNumber} of ${WIZARD_STEPS.length}`}
            accessibilityValue={{ min: 1, max: WIZARD_STEPS.length, now: stepNumber }}
            style={styles.progressHeader}
          >
            <Text style={[styles.step, { color: c.textMuted }]}>Step {stepNumber} of {WIZARD_STEPS.length}</Text>
            <View style={styles.progressTrack}>
              {WIZARD_STEPS.map((wizardStep, index) => (
                <View
                  key={wizardStep}
                  style={[
                    styles.progressSegment,
                    { backgroundColor: index < stepNumber ? c.accent : c.border },
                  ]}
                />
              ))}
            </View>
          </View>
        ) : null}

        {step === "name" ? (
          <>
            <Text style={[styles.title, { color: c.textPrimary }]}>Name your project</Text>
            <Text style={[styles.subtitle, { color: c.textMuted }]}>You can change this later.</Text>

            <Text style={[styles.fieldLabel, { color: c.textPrimary }]}>Project name</Text>
            <TextInput
              value={projectName}
              onChangeText={(value) => { setProjectName(value); if (error) setError(null); }}
              placeholder="e.g. Talos"
              placeholderTextColor={c.textMuted}
              autoFocus
              returnKeyType="next"
              onSubmitEditing={continueFromName}
              style={[styles.nameInput, { color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border }]}
            />

            {error ? <Text style={[styles.error, { color: c.error }]}>{error}</Text> : null}

            <Pressable
              onPress={continueFromName}
              style={[styles.primaryButton, { backgroundColor: c.accent }]}
            >
              <Text style={styles.primaryButtonText}>Continue</Text>
            </Pressable>
          </>
        ) : step === "device" ? (
          <>
            <Text style={[styles.title, { color: c.textPrimary }]}>Choose a device</Text>
            <Text style={[styles.subtitle, { color: c.textMuted }]}>This device will build your app.</Text>

            <View style={styles.deviceChoices}>
              {connectedRemotes.map((device) => (
                <RemoteChoice
                  key={device.id}
                  device={device}
                  selected={device.id === selectedDeviceId}
                  recommended={device.id === recommendedRemote?.id}
                  primary={device.id === primaryDeviceId}
                  runner={primaryRunnerByDevice[device.id] || "Default runner"}
                  model={primaryModelByDevice[device.id]}
                  onPress={() => setSelectedDeviceId(device.id)}
                  colors={c}
                />
              ))}
              <Pressable
                accessibilityRole="button"
                accessibilityState={{ selected: selectedDeviceId === null }}
                accessibilityLabel="Build on this phone"
                onPress={() => setSelectedDeviceId(null)}
                style={[
                  styles.choiceCard,
                  {
                    backgroundColor: selectedDeviceId === null ? c.accent + "18" : c.bgCard,
                    borderColor: selectedDeviceId === null ? c.accent : c.border,
                  },
                ]}
              >
                <View style={styles.choiceCopy}>
                  <Text style={[styles.choiceName, { color: c.textPrimary }]}>This phone</Text>
                  <View style={styles.choiceMetaRow}>
                    <View style={[styles.choiceStatusDot, { backgroundColor: c.accent }]} />
                    <Text style={[styles.choiceDetail, { color: c.textMuted }]}>On-device runner</Text>
                  </View>
                </View>
                <Text style={[styles.choiceMark, { color: selectedDeviceId === null ? c.accent : c.textMuted }]}>
                  {selectedDeviceId === null ? "✓" : "○"}
                </Text>
              </Pressable>
            </View>

            {error ? <Text style={[styles.error, { color: c.error }]}>{error}</Text> : null}

            <Pressable
              onPress={() => void continueFromDevice()}
              disabled={choosing}
              style={[styles.primaryButton, { backgroundColor: c.accent, opacity: choosing ? 0.65 : 1 }]}
            >
              {choosing ? <ActivityIndicator color="#fff" /> : <Text style={styles.primaryButtonText}>Continue</Text>}
            </Pressable>
          </>
        ) : step === "git" ? (
          <>
            <Text style={[styles.title, { color: c.textPrimary }]}>Choose Git</Text>
            <Text style={[styles.subtitle, { color: c.textMuted }]}>Pick where to save your code.</Text>

            <View style={styles.gitChoices}>
              {MOBILE_APP_GIT_PROVIDERS.map((provider) => {
                const selected = provider.id === selectedGitProvider;
                const gate = gitGates.find((candidate) => candidate.id === provider.id);
                const status = provider.id === "yaver-git" && !selectedDevice
                  ? "Ready"
                  : gitProbeLoading
                    ? "Testing…"
                    : gate?.ready
                      ? "Verified"
                      : gate
                        ? "Needs setup"
                        : "Not tested";
                return (
                  <Pressable
                    key={provider.id}
                    accessibilityRole="button"
                    accessibilityState={{ selected }}
                    accessibilityLabel={`Use ${provider.name}`}
                    onPress={() => setSelectedGitProvider(provider.id)}
                    style={[
                      styles.gitCard,
                      {
                        backgroundColor: selected ? c.accent + "18" : c.bgCard,
                        borderColor: selected ? c.accent : c.border,
                        borderWidth: selected ? 2 : 1,
                      },
                    ]}
                  >
                    <View style={{ flex: 1 }}>
                      <Text style={[styles.gitName, { color: c.textPrimary }]}>{provider.name} · {status}</Text>
                      <Text style={[styles.gitDetail, { color: c.textMuted }]}>{provider.detail}</Text>
                      {gate?.detail ? <Text style={[styles.gitProbeDetail, { color: gate.ready ? c.success : c.textMuted }]}>{gate.detail}</Text> : null}
                    </View>
                    <Text style={{ color: selected ? c.accent : c.textMuted, fontSize: 20 }}>{selected ? "✓" : "○"}</Text>
                  </Pressable>
                );
              })}
            </View>

            {gitProbeError ? (
              <View>
                <Text style={[styles.error, { color: c.error }]}>{gitProbeError}</Text>
                <Pressable onPress={() => void probeGitIntegrations()} style={styles.quietAction}>
                  <Text style={[styles.quietActionText, { color: c.accent }]}>Retry integration tests</Text>
                </Pressable>
              </View>
            ) : null}

            <Pressable
              onPress={() => setStep("palette")}
              disabled={!selectedGitReady}
              style={[styles.primaryButton, { backgroundColor: c.accent, opacity: selectedGitReady ? 1 : 0.45 }]}
            >
              <Text style={styles.primaryButtonText}>Continue</Text>
            </Pressable>
          </>
        ) : step === "palette" ? (
          <>
            <Text style={[styles.title, { color: c.textPrimary }]}>Choose colors</Text>
            <Text style={[styles.subtitle, { color: c.textMuted }]}>You can change these later.</Text>

            <View style={styles.paletteGrid}>
              {MOBILE_APP_PALETTES.map((palette) => {
                const selected = palette.id === selectedPalette.id;
                return (
                  <Pressable
                    key={palette.id}
                    accessibilityRole="button"
                    accessibilityState={{ selected }}
                    accessibilityLabel={`${palette.name} color palette`}
                    onPress={() => setSelectedPalette(palette)}
                    style={[
                      styles.paletteCard,
                      {
                        backgroundColor: palette.surface,
                        borderColor: selected ? c.accent : c.border,
                        borderWidth: selected ? 2 : 1,
                      },
                    ]}
                  >
                    <View style={styles.swatches}>
                      <View style={styles.labeledSwatch}>
                        <View style={[styles.swatch, { backgroundColor: palette.colors[0] }]} />
                        <Text style={[styles.swatchLabel, { color: palette.muted }]}>Primary</Text>
                      </View>
                      <View style={styles.labeledSwatch}>
                        <View style={[styles.swatch, { backgroundColor: palette.colors[1] }]} />
                        <Text style={[styles.swatchLabel, { color: palette.muted }]}>Secondary</Text>
                      </View>
                      <View style={styles.labeledSwatch}>
                        <View style={[styles.swatch, { backgroundColor: palette.colors[2] }]} />
                        <Text style={[styles.swatchLabel, { color: palette.muted }]}>Accent</Text>
                      </View>
                    </View>
                    <Text style={[styles.paletteName, { color: palette.text }]}>{palette.name}</Text>
                    <Text style={[styles.paletteMood, { color: palette.muted }]}>{palette.mood}</Text>
                    {selected ? (
                      <View style={[styles.selectedBadge, { backgroundColor: c.accent }]}>
                        <Text style={styles.selectedBadgeText}>✓</Text>
                      </View>
                    ) : null}
                  </Pressable>
                );
              })}
            </View>

            <Text style={[styles.finalHint, { color: c.textMuted }]}>Next, describe your app in chat.</Text>

            <Pressable onPress={() => void initializeProject()} style={[styles.primaryButton, { backgroundColor: c.accent }]}>
              <Text style={styles.primaryButtonText}>Create project</Text>
            </Pressable>
          </>
        ) : (
          <View style={styles.initializing}>
            <View style={[styles.initOrb, { backgroundColor: selectedPalette.colors[0] + "22", borderColor: selectedPalette.colors[0] }]}>
              <ActivityIndicator size="large" color={selectedPalette.colors[0]} />
            </View>
            <Text style={[styles.initTitle, { color: c.textPrimary }]}>Initializing {projectName.trim()}</Text>
            <Text style={[styles.initSubtitle, { color: c.textMuted }]}>{selectedDevice ? `On ${selectedDevice.name} with ${selectedRunner}` : "On this phone"}</Text>
            <View style={styles.initSteps}>
              {INITIALIZATION_STEPS.map((label, index) => {
                const done = index < initializationStage;
                const active = index === initializationStage;
                return (
                  <View key={label} style={styles.initRow}>
                    <View style={[styles.initDot, { backgroundColor: done ? c.success : active ? c.accent : c.border }]} />
                    <Text style={{ color: done || active ? c.textPrimary : c.textMuted, fontSize: 14, fontWeight: active ? "800" : "600" }}>{label}</Text>
                  </View>
                );
              })}
            </View>
            {error ? (
              <>
                <Text style={[styles.error, { color: c.error, textAlign: "center" }]}>{error}</Text>
                <Pressable onPress={() => void initializeProject()} style={[styles.primaryButton, { backgroundColor: c.accent, alignSelf: "stretch" }]}>
                  <Text style={styles.primaryButtonText}>Retry initialization</Text>
                </Pressable>
              </>
            ) : null}
          </View>
        )}
        </ScrollView>
      </KeyboardAvoidingView>
    </View>
  );
}

function RemoteChoice({
  device,
  selected,
  recommended,
  primary,
  runner,
  model,
  onPress,
  colors,
}: {
  device: Device;
  selected: boolean;
  recommended: boolean;
  primary: boolean;
  runner: string;
  model?: string;
  onPress: () => void;
  colors: ReturnType<typeof useColors>;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ selected }}
      accessibilityLabel={`Build on ${device.name}`}
      onPress={onPress}
      style={[
        styles.choiceCard,
        {
          backgroundColor: selected ? colors.accent + "18" : colors.bgCard,
          borderColor: selected ? colors.accent : colors.border,
        },
      ]}
    >
      <View style={styles.choiceCopy}>
        <View style={styles.choiceTitleRow}>
          <Text numberOfLines={1} style={[styles.choiceName, { color: colors.textPrimary }]}>{device.name}</Text>
          {recommended ? (
            <View style={[styles.recommendedBadge, { backgroundColor: colors.accent + "18" }]}>
              <Text style={[styles.recommendedText, { color: colors.accent }]}>Recommended</Text>
            </View>
          ) : null}
        </View>
        <View style={styles.choiceMetaRow}>
          <View style={[styles.choiceStatusDot, { backgroundColor: colors.success }]} />
          <Text numberOfLines={1} style={[styles.choiceDetail, { color: colors.textMuted }]}>
            {primary ? "Primary · " : ""}{runner}{model ? ` · ${model}` : ""}
          </Text>
        </View>
      </View>
      <Text style={[styles.choiceMark, { color: selected ? colors.accent : colors.textMuted }]}>{selected ? "✓" : "○"}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1 },
  keyboardAvoider: { flex: 1 },
  content: { width: "100%", alignSelf: "center", padding: spacing.lg, paddingBottom: 56 },
  progressHeader: { marginTop: 6, marginBottom: spacing.lg },
  step: { fontSize: 12, fontWeight: "600", marginBottom: 8 },
  progressTrack: { flexDirection: "row", gap: 6 },
  progressSegment: { flex: 1, height: 3, borderRadius: 2 },
  title: { ...typography.pageTitle, lineHeight: 34, letterSpacing: -0.4 },
  subtitle: { ...typography.body, lineHeight: 20, marginTop: 6, marginBottom: spacing.xxl },
  fieldLabel: { fontSize: 13, fontWeight: "800", marginBottom: 8 },
  nameInput: { borderWidth: 1, borderRadius: 10, minHeight: 50, paddingHorizontal: 14, fontSize: 16, fontWeight: "600" },
  deviceChoices: { gap: 10 },
  choiceCard: { minHeight: 68, borderWidth: 1, borderRadius: 14, paddingHorizontal: spacing.lg, paddingVertical: 14, flexDirection: "row", alignItems: "center", gap: spacing.md },
  choiceCopy: { flex: 1, minWidth: 0 },
  choiceTitleRow: { flexDirection: "row", alignItems: "center", gap: 8 },
  choiceName: { ...typography.cardTitle, flexShrink: 1, fontSize: 15 },
  choiceMetaRow: { flexDirection: "row", alignItems: "center", gap: 6, marginTop: 4 },
  choiceStatusDot: { width: 6, height: 6, borderRadius: 3 },
  choiceDetail: { ...typography.caption, flex: 1, fontSize: 12, lineHeight: 17 },
  choiceMark: { fontSize: 18, fontWeight: "700" },
  recommendedBadge: { borderRadius: 6, paddingHorizontal: 7, paddingVertical: 3 },
  recommendedText: { fontSize: 10, fontWeight: "700" },
  quietAction: { alignSelf: "center", paddingHorizontal: 16, paddingVertical: 16 },
  quietActionText: { fontSize: 13, fontWeight: "700" },
  error: { fontSize: 13, lineHeight: 18, marginTop: 12 },
  primaryButton: { minHeight: 48, borderRadius: 10, alignItems: "center", justifyContent: "center", marginTop: spacing.xxl },
  primaryButtonText: { color: "#fff", ...typography.bodyStrong },
  paletteGrid: { flexDirection: "row", flexWrap: "wrap", gap: 12 },
  gitChoices: { gap: 12 },
  gitCard: { minHeight: 76, borderRadius: 14, paddingHorizontal: spacing.lg, paddingVertical: 14, flexDirection: "row", alignItems: "center", gap: spacing.md },
  gitName: { ...typography.cardTitle, fontSize: 15 },
  gitDetail: { fontSize: 13, lineHeight: 18, marginTop: 4 },
  gitProbeDetail: { fontSize: 11, lineHeight: 16, marginTop: 6, fontWeight: "700" },
  paletteCard: { width: "48%", minHeight: 138, borderRadius: 14, padding: 14, position: "relative" },
  swatches: { flexDirection: "row", gap: 10, marginBottom: 16 },
  labeledSwatch: { alignItems: "center", gap: 4 },
  swatch: { width: 30, height: 30, borderRadius: 15, borderWidth: 2, borderColor: "rgba(255,255,255,0.65)" },
  swatchLabel: { fontSize: 8, fontWeight: "700" },
  paletteName: { fontSize: 15, fontWeight: "700" },
  paletteMood: { fontSize: 12, lineHeight: 17, marginTop: 4 },
  selectedBadge: { position: "absolute", right: 12, top: 12, width: 24, height: 24, borderRadius: 12, alignItems: "center", justifyContent: "center" },
  selectedBadgeText: { color: "#fff", fontSize: 14, fontWeight: "900" },
  finalHint: { fontSize: 13, lineHeight: 18, marginTop: 20 },
  initializing: { flex: 1, minHeight: 560, alignItems: "center", justifyContent: "center", paddingHorizontal: 12 },
  initOrb: { width: 96, height: 96, borderRadius: 48, borderWidth: 1, alignItems: "center", justifyContent: "center", marginBottom: 26 },
  initTitle: { fontSize: 28, fontWeight: "900", textAlign: "center" },
  initSubtitle: { fontSize: 14, marginTop: 8, textAlign: "center" },
  initSteps: { alignSelf: "stretch", marginTop: 36, gap: 18, paddingHorizontal: 24 },
  initRow: { flexDirection: "row", alignItems: "center", gap: 12 },
  initDot: { width: 10, height: 10, borderRadius: 5 },
});
