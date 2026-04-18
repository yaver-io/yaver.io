import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import type {
  WizardGenerateResult,
  WizardQuestion,
  WizardSession,
} from "../../src/lib/quic";

const QUESTION_SECTIONS: Record<string, string> = {
  app_name: "Identity",
  slug: "Identity",
  description: "Identity",
  tagline: "Identity",
  app_template: "Product",
  supported_languages: "Product",
  domain: "Brand",
  primary_color: "Palette",
  secondary_color: "Palette",
  accent_color: "Palette",
  surface_color: "Palette",
  tone: "Palette",
  include_web: "Surfaces",
  include_mobile: "Surfaces",
  include_backend: "Surfaces",
  include_landing: "Surfaces",
  web_framework: "Stack",
  web_host: "Stack",
  backend: "Stack",
  mobile_stack: "Stack",
  mobile_nav_style: "Mobile UI",
  mobile_nav_count: "Mobile UI",
  mobile_nav_labels: "Mobile UI",
  design_source: "References",
  design_reference_url: "References",
  design_notes: "References",
  oauth_apple: "Auth",
  oauth_google: "Auth",
  oauth_microsoft: "Auth",
  oauth_email: "Auth",
  payments: "Business",
  ios_bundle_id: "Release",
  android_package: "Release",
  apple_team_id: "Release",
  play_service_account: "Release",
  cloudflare_zone: "Release",
  git_provider: "Repo",
  git_visibility: "Repo",
  git_org: "Repo",
  git_repo_name: "Repo",
  confirm: "Ready",
};

const QUICK_COLOR_SWATCHS = ["#4F46E5", "#0EA5E9", "#14B8A6", "#F97316", "#F59E0B", "#111827"];

function isQuestionVisible(question: WizardQuestion, answers: Record<string, string>) {
  const mobileOn = answers.include_mobile === "true";
  const webOn = answers.include_web === "true" || answers.include_landing === "true";
  const backendOn = answers.include_backend === "true";
  const anyAuth = mobileOn || webOn || backendOn;

  if ((question.id === "web_framework" || question.id === "web_host") && !webOn) return false;
  if (
    [
      "mobile_stack",
      "mobile_nav_style",
      "mobile_nav_count",
      "mobile_nav_labels",
      "ios_bundle_id",
      "android_package",
      "apple_team_id",
      "play_service_account",
    ].includes(question.id) &&
    !mobileOn
  ) {
    return false;
  }
  if (question.id === "backend" && !backendOn) return false;
  if (["oauth_apple", "oauth_google", "oauth_microsoft", "oauth_email"].includes(question.id) && !anyAuth) {
    return false;
  }
  if (question.id === "cloudflare_zone" && answers.web_host !== "cloudflare") return false;
  if (question.id === "payments" && !webOn && !mobileOn) return false;
  if (["git_visibility", "git_org", "git_repo_name"].includes(question.id) && answers.git_provider === "none") {
    return false;
  }
  if (question.id === "design_reference_url" && (!answers.design_source || answers.design_source === "prompt-only")) {
    return false;
  }
  return question.kind !== "done";
}

function formatChoice(choice: string) {
  return choice
    .split(/[-_]/g)
    .filter(Boolean)
    .map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
    .join(" ");
}

function buildInitialInput(question: WizardQuestion | null) {
  return question?.default ?? "";
}

export default function NewProjectScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus, devices, selectDevice } = useDevice();
  const connected = connectionStatus === "connected";
  const connecting = connectionStatus === "connecting";

  const [session, setSession] = useState<WizardSession | null>(null);
  const [question, setQuestion] = useState<WizardQuestion | null>(null);
  const [questions, setQuestions] = useState<WizardQuestion[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [skipping, setSkipping] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<WizardGenerateResult | null>(null);

  const answers = session?.answers ?? {};

  const start = useCallback(async () => {
    setLoading(true);
    setError(null);
    setResult(null);
    const [startRes, allQuestions] = await Promise.all([
      quicClient.wizardStart(),
      quicClient.wizardQuestions(),
    ]);
    setLoading(false);
    if (!startRes) {
      setError("Could not start the wizard. The agent may be offline.");
      return;
    }
    setQuestions(allQuestions ?? []);
    setSession(startRes.session);
    setQuestion(startRes.question);
    setInput(buildInitialInput(startRes.question));
  }, []);

  useEffect(() => {
    if (connected && !session && !result) {
      void start();
    }
  }, [connected, result, session, start]);

  const visibleQuestions = useMemo(
    () => questions.filter((item) => isQuestionVisible(item, answers)),
    [answers, questions]
  );

  const currentIndex = useMemo(() => {
    if (!question) return 0;
    const idx = visibleQuestions.findIndex((item) => item.id === question.id);
    return idx >= 0 ? idx : visibleQuestions.length;
  }, [question, visibleQuestions]);

  const progressTotal = Math.max(visibleQuestions.length, 1);
  const progressValue = Math.min(currentIndex / progressTotal, 1);

  const submitAnswer = useCallback(
    async (answer: string) => {
      if (!session || !question) return null;
      const res = await quicClient.wizardAnswer(session.id, question.id, answer);
      if (!res) return null;
      setSession(res.session);
      setQuestion(res.question);
      setInput(buildInitialInput(res.question));
      return res;
    },
    [question, session]
  );

  const handleNext = useCallback(async () => {
    if (!question) return;
    setLoading(true);
    setError(null);

    let ok = true;
    if (question.kind === "confirm") {
      ok = !!(await submitAnswer(input.trim() || question.default || "true"));
      if (ok && session) {
        const gen = await quicClient.wizardGenerate(session.id);
        if (!gen || !gen.ok) ok = false;
        else setResult(gen);
      }
    } else if (question.kind === "done") {
      if (!session) ok = false;
      else {
        const gen = await quicClient.wizardGenerate(session.id);
        if (!gen || !gen.ok) ok = false;
        else setResult(gen);
      }
    } else {
      ok = !!(await submitAnswer(input.trim() || question.default || ""));
    }

    setLoading(false);
    if (!ok) {
      setError(question.kind === "confirm" || question.kind === "done" ? "Generation failed. Check agent logs and retry." : "Could not save that answer.");
    }
  }, [input, question, session, submitAnswer]);

  const skipThis = useCallback(async () => {
    if (!question) return;
    setLoading(true);
    setError(null);
    const res = await submitAnswer(question.default ?? "");
    setLoading(false);
    if (!res) setError("Could not skip this step.");
  }, [question, submitAnswer]);

  const useDefaultsForRest = useCallback(async () => {
    if (!session || !question) return;
    setSkipping(true);
    setError(null);

    let currentQuestion: WizardQuestion | null = question;
    let currentSession: WizardSession | null = session;

    while (currentQuestion && currentQuestion.kind !== "done") {
      const answer = currentQuestion.id === "confirm" ? "true" : currentQuestion.default ?? "";
      const res = await quicClient.wizardAnswer(currentSession.id, currentQuestion.id, answer);
      if (!res) {
        setSkipping(false);
        setError("Could not fast-forward the wizard.");
        return;
      }
      currentSession = res.session;
      currentQuestion = res.question;
    }

    setSession(currentSession);
    setQuestion(currentQuestion);
    setInput(buildInitialInput(currentQuestion));
    setSkipping(false);
  }, [question, session]);

  const summary = useMemo(() => {
    const items: string[] = [];
    if (answers.app_template) items.push(formatChoice(answers.app_template));
    if (answers.supported_languages) items.push(answers.supported_languages);
    if (answers.mobile_nav_labels) items.push(answers.mobile_nav_labels);
    if (answers.design_source && answers.design_source !== "prompt-only") items.push(formatChoice(answers.design_source));
    return items.slice(0, 4);
  }, [answers]);

  const renderField = () => {
    if (!question) return null;

    if (question.kind === "choice" || question.kind === "bool") {
      const choices = question.kind === "bool" ? ["true", "false"] : question.choices ?? [];
      return (
        <View style={styles.choiceGrid}>
          {choices.map((choice) => {
            const selected = input === choice;
            const label = question.kind === "bool" ? (choice === "true" ? "Yes" : "No") : formatChoice(choice);
            return (
              <Pressable
                key={choice}
                onPress={() => setInput(choice)}
                style={[
                  styles.choiceCard,
                  {
                    backgroundColor: selected ? c.accent + "18" : c.bgCard,
                    borderColor: selected ? c.accent : c.border,
                  },
                ]}
              >
                <Text style={[styles.choiceLabel, { color: c.textPrimary }]}>{label}</Text>
                <Text style={[styles.choiceValue, { color: selected ? c.accent : c.textMuted }]}>
                  {choice}
                </Text>
              </Pressable>
            );
          })}
        </View>
      );
    }

    const multiline = ["description", "design_notes", "mobile_nav_labels", "supported_languages"].includes(question.id);
    return (
      <View style={{ gap: 12 }}>
        <TextInput
          value={input}
          onChangeText={setInput}
          placeholder={question.default ?? ""}
          placeholderTextColor={c.textMuted}
          autoCapitalize={question.id.includes("color") ? "characters" : "none"}
          autoCorrect={false}
          multiline={multiline}
          style={[
            styles.input,
            {
              minHeight: multiline ? 120 : 56,
              color: c.textPrimary,
              backgroundColor: c.bgInput,
              borderColor: c.border,
              textAlignVertical: multiline ? "top" : "center",
            },
          ]}
        />
        {question.kind === "color" ? (
          <View style={styles.swatchRow}>
            {QUICK_COLOR_SWATCHS.map((swatch) => (
              <Pressable
                key={swatch}
                onPress={() => setInput(swatch)}
                style={[
                  styles.swatch,
                  {
                    backgroundColor: swatch,
                    borderColor: input === swatch ? c.textPrimary : "rgba(255,255,255,0.14)",
                  },
                ]}
              />
            ))}
          </View>
        ) : null}
      </View>
    );
  };

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Mobile App Builder</Text>
        <View style={{ width: 50 }} />
      </View>

      {!connected ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "800", marginBottom: 8 }}>
            Connect a dev machine
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 14, lineHeight: 21, marginBottom: 18 }}>
            The wizard runs on your paired agent, then generates the monorepo scaffold there. Build from the phone, generate on the machine.
          </Text>
          {connecting ? <ActivityIndicator style={{ marginBottom: 16 }} /> : null}
          {devices.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>
              No devices are registered yet. Run `brew install yaver && yaver auth && yaver serve` on your Mac.
            </Text>
          ) : (
            <View style={{ gap: 10 }}>
              {devices.map((device) => (
                <Pressable
                  key={device.id}
                  onPress={() => selectDevice(device)}
                  style={[
                    styles.deviceCard,
                    { backgroundColor: c.bgCard, borderColor: c.border },
                  ]}
                >
                  <View
                    style={{
                      width: 10,
                      height: 10,
                      borderRadius: 5,
                      backgroundColor: device.online ? "#22c55e" : c.textMuted,
                    }}
                  />
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>{device.name}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>{device.os}</Text>
                  </View>
                  <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Connect</Text>
                </Pressable>
              ))}
            </View>
          )}
        </ScrollView>
      ) : result ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          <View style={[styles.hero, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.heroEyebrow, { color: c.accent }]}>Project generated</Text>
            <Text style={[styles.heroTitle, { color: c.textPrimary }]}>{result.directory}</Text>
            <Text style={[styles.heroBody, { color: c.textMuted }]}>
              The scaffold includes the palette, template, mobile navigation, and OAuth stubs selected in the wizard.
            </Text>
          </View>

          <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Next steps</Text>
            {result.nextSteps.map((step, index) => (
              <Text key={index} style={[styles.stepText, { color: c.textPrimary }]}>
                • {step}
              </Text>
            ))}
          </View>

          <Pressable
            style={[styles.primaryButton, { backgroundColor: c.accent }]}
            onPress={() => {
              setSession(null);
              setQuestion(null);
              setResult(null);
              setInput("");
              void start();
            }}
          >
            <Text style={styles.primaryButtonText}>Generate another</Text>
          </Pressable>
        </ScrollView>
      ) : loading && !question ? (
        <View style={styles.center}>
          <ActivityIndicator />
          <Text style={{ color: c.textMuted, marginTop: 12 }}>Loading builder…</Text>
        </View>
      ) : !question ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          {error ? <Text style={{ color: c.error, marginBottom: 12 }}>{error}</Text> : null}
          <Text style={{ color: c.textMuted, marginBottom: 16 }}>
            Could not start the project wizard. The connected agent may be too old for this flow.
          </Text>
          <Pressable onPress={() => void start()} style={[styles.primaryButton, { backgroundColor: c.accent }]}>
            <Text style={styles.primaryButtonText}>Retry</Text>
          </Pressable>
        </ScrollView>
      ) : (
        <ScrollView contentContainerStyle={{ padding: 20, paddingBottom: 36 }} keyboardShouldPersistTaps="handled">
          <View style={[styles.hero, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.heroEyebrow, { color: c.accent }]}>
              {QUESTION_SECTIONS[question.id] ?? "Builder"} · Step {Math.min(currentIndex + 1, progressTotal)} / {progressTotal}
            </Text>
            <Text style={[styles.heroTitle, { color: c.textPrimary }]}>Phone-first monorepo setup</Text>
            <Text style={[styles.heroBody, { color: c.textMuted }]}>
              Capture template, palette, languages, navigation, references, and auth now. Anything optional can be skipped or defaulted.
            </Text>
            <View style={[styles.progressTrack, { backgroundColor: c.border }]}>
              <View style={[styles.progressFill, { backgroundColor: c.accent, width: `${progressValue * 100}%` }]} />
            </View>
            {summary.length ? (
              <View style={styles.summaryRow}>
                {summary.map((item) => (
                  <View key={item} style={[styles.summaryPill, { backgroundColor: c.accent + "14" }]}>
                    <Text style={[styles.summaryPillText, { color: c.textPrimary }]}>{item}</Text>
                  </View>
                ))}
              </View>
            ) : null}
          </View>

          {error ? <Text style={{ color: c.error, marginBottom: 12 }}>{error}</Text> : null}

          <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.panelTitle, { color: c.textPrimary }]}>{question.prompt}</Text>
            {question.help ? <Text style={[styles.panelBody, { color: c.textMuted }]}>{question.help}</Text> : null}
            <View style={{ marginTop: 18 }}>{renderField()}</View>
          </View>

          <View style={styles.actions}>
            {question.kind !== "confirm" && question.kind !== "done" ? (
              <Pressable
                style={[styles.secondaryButton, { borderColor: c.border, backgroundColor: c.bgCard }]}
                onPress={() => void skipThis()}
                disabled={loading || skipping}
              >
                <Text style={[styles.secondaryButtonText, { color: c.textPrimary }]}>Skip this</Text>
              </Pressable>
            ) : null}
            <Pressable
              style={[styles.secondaryButton, { borderColor: c.border, backgroundColor: c.bgCard }]}
              onPress={() => void useDefaultsForRest()}
              disabled={loading || skipping}
            >
              <Text style={[styles.secondaryButtonText, { color: c.textPrimary }]}>
                {skipping ? "Skipping…" : "Use defaults for rest"}
              </Text>
            </Pressable>
            <Pressable
              style={[styles.primaryButton, { backgroundColor: c.accent, opacity: loading ? 0.6 : 1 }]}
              onPress={() => void handleNext()}
              disabled={loading || skipping}
            >
              <Text style={styles.primaryButtonText}>
                {loading ? "Working…" : question.kind === "confirm" || question.kind === "done" ? "Generate project" : "Continue"}
              </Text>
            </Pressable>
          </View>
        </ScrollView>
      )}
    </View>
  );
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
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  hero: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
    marginBottom: 16,
  },
  heroEyebrow: { fontSize: 12, fontWeight: "800", letterSpacing: 0.6, textTransform: "uppercase" },
  heroTitle: { fontSize: 24, fontWeight: "800", marginTop: 10 },
  heroBody: { fontSize: 14, lineHeight: 21, marginTop: 8 },
  progressTrack: { height: 8, borderRadius: 999, overflow: "hidden", marginTop: 16 },
  progressFill: { height: "100%", borderRadius: 999 },
  summaryRow: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 14 },
  summaryPill: { borderRadius: 999, paddingHorizontal: 12, paddingVertical: 7 },
  summaryPillText: { fontSize: 12, fontWeight: "700" },
  panel: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
  },
  panelTitle: { fontSize: 21, fontWeight: "800" },
  panelBody: { fontSize: 14, lineHeight: 21, marginTop: 8 },
  input: {
    borderWidth: 1,
    borderRadius: 18,
    paddingHorizontal: 14,
    paddingVertical: 14,
    fontSize: 16,
  },
  choiceGrid: { gap: 10 },
  choiceCard: {
    borderWidth: 1,
    borderRadius: 18,
    padding: 14,
  },
  choiceLabel: { fontSize: 16, fontWeight: "700" },
  choiceValue: { fontSize: 12, marginTop: 6 },
  swatchRow: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  swatch: {
    width: 36,
    height: 36,
    borderRadius: 18,
    borderWidth: 2,
  },
  actions: { gap: 10, marginTop: 16 },
  primaryButton: {
    borderRadius: 18,
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 15,
  },
  primaryButtonText: { color: "#fff", fontSize: 15, fontWeight: "800" },
  secondaryButton: {
    borderWidth: 1,
    borderRadius: 18,
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 14,
  },
  secondaryButtonText: { fontSize: 14, fontWeight: "700" },
  deviceCard: {
    borderWidth: 1,
    borderRadius: 16,
    padding: 14,
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  stepText: { fontSize: 14, lineHeight: 21, marginTop: 8 },
});
