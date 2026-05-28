import { router } from "expo-router";
import React, { useEffect, useState } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
  Alert,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { submitSurvey, getAiRunners, saveUserSettings, getUserSettings, type AiRunner, type SpeechProvider, type KeyStorage, saveLocalSecret, LOCAL_KEYS, saveKeyStoragePreference } from "../src/lib/auth";
import { customRelaysKey } from "../src/context/DeviceContext";
import { SPEECH_PROVIDERS } from "../src/lib/speech";

const IDENTITIES = [
  { id: "developer", label: "Developer" },
  { id: "business", label: "Business Owner" },
  { id: "student", label: "Student / Academic" },
  { id: "other", label: "Other" },
];

const LANGUAGES = [
  "JavaScript/TypeScript",
  "Python",
  "Go",
  "Rust",
  "Java",
  "C/C++",
  "Ruby",
  "PHP",
  "Swift",
  "Kotlin",
  "C#",
  "Other",
];

const EXPERIENCE_LEVELS = ["Junior", "Mid-Level", "Senior", "Staff/Lead"];

const USE_CASES = [
  "Work / Business",
  "Hobby Projects",
  "Academic / Research",
  "Open Source",
  "Freelance / Consulting",
  "Other",
];

const COMPANY_SIZES = ["Solo", "2-10", "11-50", "51-200", "201-1000", "1000+"];

export default function SurveyScreen() {
  const { token, user, markSurveyCompleted, refreshUser } = useAuth();
  const c = useColors();

  const [page, setPage] = useState(0);
  const [fullName, setFullName] = useState(user?.name ?? "");
  const [identity, setIdentity] = useState<string | null>(null);
  const [languages, setLanguages] = useState<string[]>([]);
  const [experience, setExperience] = useState<string | null>(null);
  const [useCase, setUseCase] = useState<string | null>(null);
  const [companySize, setCompanySize] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [runners, setRunners] = useState<AiRunner[]>([]);
  const [selectedRunner, setSelectedRunner] = useState<string>("claude");
  const [customCommand, setCustomCommand] = useState("");
  const [runnerPresetFromCli, setRunnerPresetFromCli] = useState(false);
  const [speechProvider, setSpeechProvider] = useState<SpeechProvider | null>("on-device");
  const [speechApiKey, setSpeechApiKey] = useState("");
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [verbosity, setVerbosity] = useState(10);
  const [keyStorage, setKeyStorage] = useState<KeyStorage>("local");
  // Relay server
  const [relayUrl, setRelayUrl] = useState("");
  const [relayPassword, setRelayPassword] = useState("");
  const [relayLabel, setRelayLabel] = useState("");
  const [relayOptOut, setRelayOptOut] = useState(false);

  useEffect(() => {
    getAiRunners().then((r) => {
      setRunners(r);
      const defaultRunner = r.find((runner) => runner.isDefault);
      if (defaultRunner) setSelectedRunner(defaultRunner.runnerId);
    });
    // Check if runner was already set from CLI
    if (token) {
      getUserSettings(token).then((s) => {
        if (s.runnerId) {
          setSelectedRunner(s.runnerId);
          setRunnerPresetFromCli(true);
          if (s.customRunnerCommand) setCustomCommand(s.customRunnerCommand);
        }
      }).catch(() => {});
    }
  }, [token]);

  const isDev = identity === "developer";
  const totalPages = isDev ? 8 : 7;

  const toggleLanguage = (lang: string) => {
    setLanguages((prev) =>
      prev.includes(lang) ? prev.filter((l) => l !== lang) : [...prev, lang]
    );
  };

  const finishSurvey = async () => {
    if (!token) return;
    setIsSubmitting(true);
    try {
      await submitSurvey(token, {
        isDeveloper: isDev,
        fullName: fullName.trim() || undefined,
        languages: isDev && languages.length > 0 ? languages : undefined,
        experienceLevel: isDev ? experience ?? undefined : undefined,
        role: identity ?? undefined,
        companySize: companySize ?? undefined,
        useCase: useCase ?? undefined,
      });
      // Save runner + speech preferences to user settings
      const settings: Record<string, any> = { runnerId: selectedRunner };
      if (selectedRunner === "custom" && customCommand.trim()) {
        settings.customRunnerCommand = customCommand.trim();
      }
      settings.speechProvider = speechProvider ?? "on-device";
      settings.ttsEnabled = ttsEnabled;
      settings.verbosity = verbosity;
      // Speech provider keys are local-only. Convex gets provider
      // preferences, never raw STT/TTS API keys.
      await saveKeyStoragePreference("local");
      const providerInfo = SPEECH_PROVIDERS.find((p) => p.id === speechProvider);
      if (providerInfo?.requiresKey && speechApiKey.trim()) {
        await saveLocalSecret(LOCAL_KEYS.speechApiKey, speechApiKey.trim());
      }
      await saveUserSettings(token, settings);
      // Save relay server if configured (skip if user opted out of relay)
      if (!relayOptOut && relayUrl.trim()) {
        const url = relayUrl.trim().replace(/\/+$/, "");
        const host = url.replace(/^https?:\/\//, "").replace(/:\d+$/, "").replace(/\/.*$/, "");
        const relay = {
          id: Date.now().toString(36),
          quicAddr: host + ":4433",
          httpUrl: url,
          region: relayLabel.trim() || "custom",
          priority: 1,
          password: relayPassword.trim() || undefined,
        };
        const relaysKey = customRelaysKey(user?.id);
        await AsyncStorage.setItem(relaysKey, JSON.stringify([relay]));
      }
      markSurveyCompleted();
      await refreshUser();
      router.replace("/(tabs)/tasks");
    } catch {
      Alert.alert("Error", "Failed to submit survey. Please try again.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleNext = () => {
    if (page < totalPages - 1) {
      setPage(page + 1);
    } else {
      finishSurvey();
    }
  };

  const handleBack = () => setPage((p) => Math.max(0, p - 1));

  const isLastPage = page === totalPages - 1;

  // Map visual dot index
  const currentDot = page;
  const dotCount = totalPages;

  const renderNamePage = () => (
    <View style={styles.pageContent}>
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        How can we call you?
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        Let's get to know each other
      </Text>

      <TextInput
        style={[styles.nameInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
        placeholder="Your name"
        placeholderTextColor={c.textMuted}
        value={fullName}
        onChangeText={setFullName}
        autoCapitalize="words"
        autoCorrect={false}
        autoFocus
      />

      <Pressable
        style={({ pressed }) => [
          styles.inlineContinue,
          { backgroundColor: c.textPrimary },
          pressed && { opacity: 0.7 },
          !fullName.trim() && { opacity: 0.4 },
        ]}
        onPress={handleNext}
        disabled={!fullName.trim()}
      >
        <Text style={[styles.nextButtonText, { color: c.bg }]}>Continue</Text>
      </Pressable>
    </View>
  );

  const renderRolePage = () => (
    <View style={styles.pageContent}>
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        What best describes you?
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        Help us personalize your experience
      </Text>

      <View style={styles.identityGrid}>
        {IDENTITIES.map((item) => {
          const selected = identity === item.id;
          return (
            <Pressable
              key={item.id}
              style={({ pressed }) => [
                styles.identityButton,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setIdentity(item.id)}
            >
              <Text
                style={[
                  styles.identityButtonText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {item.label}
              </Text>
            </Pressable>
          );
        })}
      </View>
    </View>
  );

  const renderRunnerPage = () => (
    <ScrollView
      contentContainerStyle={styles.pageContent}
      showsVerticalScrollIndicator={false}
    >
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        Choose your AI agent
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        {runnerPresetFromCli
          ? "Previously set from CLI. You can change it here."
          : "Yaver runs any terminal AI tool on your machine"}
      </Text>

      <View style={styles.identityGrid}>
        {runners.map((runner) => {
          const selected = selectedRunner === runner.runnerId;
          return (
            <Pressable
              key={runner.runnerId}
              style={({ pressed }) => [
                styles.identityButton,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setSelectedRunner(runner.runnerId)}
            >
              <Text
                style={[
                  styles.identityButtonText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {runner.name}
              </Text>
              <Text
                style={[
                  styles.runnerDescText,
                  { color: selected ? "rgba(255,255,255,0.7)" : c.textMuted },
                ]}
              >
                {runner.description}
              </Text>
            </Pressable>
          );
        })}
      </View>

      {selectedRunner === "custom" && (
        <TextInput
          style={[styles.nameInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary, marginTop: 16 }]}
          placeholder="e.g. my-ai-tool --auto {prompt}"
          placeholderTextColor={c.textMuted}
          value={customCommand}
          onChangeText={setCustomCommand}
          autoCapitalize="none"
          autoCorrect={false}
        />
      )}

      <Text style={[styles.runnerHint, { color: c.textMuted }]}>
        You can change this anytime in Settings
      </Text>
    </ScrollView>
  );

  const selectedSpeechProvider = SPEECH_PROVIDERS.find((p) => p.id === speechProvider);

  const renderSpeechPage = () => (
    <ScrollView
      contentContainerStyle={styles.pageContent}
      showsVerticalScrollIndicator={false}
    >
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        Voice input
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        Send tasks by speaking instead of typing
      </Text>

      <View style={styles.identityGrid}>
        {SPEECH_PROVIDERS.map((provider) => {
          const selected = speechProvider === provider.id;
          return (
            <Pressable
              key={provider.id}
              style={({ pressed }) => [
                styles.identityButton,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setSpeechProvider(provider.id)}
            >
              <Text
                style={[
                  styles.identityButtonText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {provider.name}
              </Text>
              <Text
                style={[
                  styles.runnerDescText,
                  { color: selected ? "rgba(255,255,255,0.7)" : c.textMuted },
                ]}
              >
                {provider.description}
              </Text>
            </Pressable>
          );
        })}
      </View>

      {selectedSpeechProvider?.requiresKey && (
        <>
          <TextInput
            style={[styles.nameInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary, marginTop: 16 }]}
            placeholder={selectedSpeechProvider.keyPlaceholder ?? "API Key"}
            placeholderTextColor={c.textMuted}
            value={speechApiKey}
            onChangeText={setSpeechApiKey}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
          />
          <Text style={[{ fontSize: 11, color: c.textMuted, marginTop: -12, marginBottom: 12, paddingHorizontal: 4 }]}>
            {selectedSpeechProvider.keyHint}
          </Text>

          {/* Key storage preference */}
          <View style={{ flexDirection: "row", gap: 8, marginBottom: 8 }}>
            <Pressable
              style={({ pressed }) => [
                {
                  flex: 1, paddingVertical: 8, paddingHorizontal: 10,
                  borderRadius: 8, borderWidth: 1, alignItems: "center",
                  backgroundColor: keyStorage === "local" ? c.accent : c.bgCard,
                  borderColor: keyStorage === "local" ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setKeyStorage("local")}
            >
              <Text style={{ color: keyStorage === "local" ? "#fff" : c.textPrimary, fontWeight: "600", fontSize: 12 }}>
                Device only
              </Text>
              <Text style={{ color: keyStorage === "local" ? "rgba(255,255,255,0.7)" : c.textMuted, fontSize: 10, marginTop: 1 }}>
                Stored in Keychain
              </Text>
            </Pressable>
            <Pressable
              style={({ pressed }) => [
                {
                  flex: 1, paddingVertical: 8, paddingHorizontal: 10,
                  borderRadius: 8, borderWidth: 1, alignItems: "center",
                  backgroundColor: c.bgCard,
                  borderColor: c.border,
                  opacity: 0.55,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setKeyStorage("local")}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 12 }}>
                Cloud disabled
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 1 }}>
                Keys stay local
              </Text>
            </Pressable>
          </View>
        </>
      )}

      <Pressable
        style={[styles.optionButton, {
          backgroundColor: ttsEnabled ? c.accent : c.bgCard,
          borderColor: ttsEnabled ? c.accent : c.border,
          marginTop: 16,
          flexDirection: "row",
          alignItems: "center",
          justifyContent: "space-between",
        }]}
        onPress={() => setTtsEnabled(!ttsEnabled)}
      >
        <View style={{ flex: 1 }}>
          <Text style={[styles.optionText, { color: ttsEnabled ? "#fff" : c.textPrimary }]}>
            Read responses aloud
          </Text>
          <Text style={[{ fontSize: 11, color: ttsEnabled ? "rgba(255,255,255,0.7)" : c.textMuted, marginTop: 2 }]}>
            Uses device text-to-speech
          </Text>
        </View>
      </Pressable>
    </ScrollView>
  );

  const renderVerbosityPage = () => (
    <View style={styles.pageContent}>
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        Response detail
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        How verbose should AI responses be?
      </Text>

      <View style={{
        marginTop: 20, paddingVertical: 20, paddingHorizontal: 20,
        borderRadius: 16, borderWidth: 1,
        backgroundColor: c.bgCard, borderColor: c.border,
      }}>
        <Text style={{ color: c.accent, fontWeight: "700", fontSize: 48, textAlign: "center", marginBottom: 4 }}>
          {verbosity}
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", marginBottom: 20 }}>
          {verbosity <= 2 ? "Minimal — just confirm what was done"
            : verbosity <= 4 ? "Brief — summarize in a few sentences"
            : verbosity <= 6 ? "Moderate — key changes and reasoning"
            : verbosity <= 8 ? "Detailed — code changes and explanations"
            : "Full — everything: diffs, reasoning, alternatives"}
        </Text>
        <View style={{ flexDirection: "row", gap: 4, alignItems: "center" }}>
          {Array.from({ length: 11 }).map((_, i) => (
            <Pressable
              key={i}
              onPress={() => setVerbosity(i)}
              style={{
                flex: 1, height: 36, borderRadius: 8,
                backgroundColor: i <= verbosity ? c.accent : c.bg,
                borderWidth: 1,
                borderColor: i <= verbosity ? c.accent : c.border,
                alignItems: "center", justifyContent: "center",
              }}
            >
              {i === verbosity && (
                <Text style={{ color: "#fff", fontSize: 10, fontWeight: "700" }}>{i}</Text>
              )}
            </Pressable>
          ))}
        </View>
        <View style={{ flexDirection: "row", justifyContent: "space-between", marginTop: 6 }}>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>Minimal</Text>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>Full</Text>
        </View>
      </View>
    </View>
  );

  const renderRelayPage = () => (
    <ScrollView
      contentContainerStyle={styles.pageContent}
      showsVerticalScrollIndicator={false}
      keyboardShouldPersistTaps="handled"
    >
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        Relay server
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        For connecting outside your home Wi-Fi
      </Text>

      {/* Free relay included */}
      <View style={{
        padding: 14, borderRadius: 12, borderWidth: 1,
        backgroundColor: relayOptOut ? c.bgCard : c.accent + "18",
        borderColor: relayOptOut ? c.border : c.accent,
        marginBottom: 16, marginTop: 8,
      }}>
        <View style={{ flexDirection: "row", alignItems: "center", marginBottom: 4 }}>
          <Text style={{ fontSize: 15, fontWeight: "600", color: relayOptOut ? c.textMuted : c.textPrimary, flex: 1 }}>
            Free relay server included
          </Text>
          <Text style={{ fontSize: 11, fontWeight: "600", color: relayOptOut ? c.textMuted : c.accent }}>
            {relayOptOut ? "DISABLED" : "ACTIVE"}
          </Text>
        </View>
        <Text style={{ fontSize: 13, color: relayOptOut ? c.textMuted : c.textSecondary }}>
          public.yaver.io (EU) — no setup needed
        </Text>
      </View>

      {/* Opt-out toggle */}
      <Pressable
        style={[styles.optionButton, {
          backgroundColor: relayOptOut ? c.accent : c.bgCard,
          borderColor: relayOptOut ? c.accent : c.border,
          marginBottom: 20,
          flexDirection: "row",
          alignItems: "center",
          justifyContent: "space-between",
        }]}
        onPress={() => setRelayOptOut(!relayOptOut)}
      >
        <View style={{ flex: 1 }}>
          <Text style={[styles.optionText, { color: relayOptOut ? "#fff" : c.textPrimary }]}>
            I don't need a relay (LAN/Tailscale only)
          </Text>
          <Text style={[{ fontSize: 11, color: relayOptOut ? "rgba(255,255,255,0.7)" : c.textMuted, marginTop: 2 }]}>
            Skip if you're always on the same Wi-Fi or use Tailscale
          </Text>
        </View>
      </Pressable>

      {/* Custom relay option */}
      {!relayOptOut && (
        <>
          <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>
            ADD CUSTOM RELAY INSTEAD
          </Text>

          <TextInput
            style={[styles.nameInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
            placeholder="URL, e.g. https://relay.example.com"
            placeholderTextColor={c.textMuted}
            value={relayUrl}
            onChangeText={setRelayUrl}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
          />

          <TextInput
            style={[styles.nameInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
            placeholder="Password (optional)"
            placeholderTextColor={c.textMuted}
            value={relayPassword}
            onChangeText={setRelayPassword}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
          />

          <TextInput
            style={[styles.nameInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
            placeholder="Label, e.g. home, office (optional)"
            placeholderTextColor={c.textMuted}
            value={relayLabel}
            onChangeText={setRelayLabel}
            autoCapitalize="none"
            autoCorrect={false}
          />
        </>
      )}
    </ScrollView>
  );

  const renderPage1Dev = () => (
    <ScrollView
      contentContainerStyle={styles.pageContent}
      showsVerticalScrollIndicator={false}
    >
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        Your tech stack
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        Select all that apply
      </Text>
      <View style={styles.chipContainer}>
        {LANGUAGES.map((lang) => {
          const selected = languages.includes(lang);
          return (
            <Pressable
              key={lang}
              style={({ pressed }) => [
                styles.chip,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => toggleLanguage(lang)}
            >
              <Text
                style={[
                  styles.chipText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {lang}
              </Text>
            </Pressable>
          );
        })}
      </View>

      <Text
        style={[styles.sectionLabel, { color: c.textSecondary, marginTop: 28 }]}
      >
        EXPERIENCE
      </Text>
      <View style={styles.optionGroup}>
        {EXPERIENCE_LEVELS.map((level) => {
          const selected = experience === level;
          return (
            <Pressable
              key={level}
              style={({ pressed }) => [
                styles.optionButton,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setExperience(level)}
            >
              <Text
                style={[
                  styles.optionText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {level}
              </Text>
            </Pressable>
          );
        })}
      </View>
    </ScrollView>
  );

  const renderUseCasePage = () => (
    <ScrollView
      contentContainerStyle={styles.pageContent}
      showsVerticalScrollIndicator={false}
    >
      <Text style={[styles.pageTitle, { color: c.textPrimary }]}>
        How will you use Yaver?
      </Text>
      <Text style={[styles.pageSubtitle, { color: c.textSecondary }]}>
        Almost done
      </Text>

      <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>
        PRIMARY USE
      </Text>
      <View style={styles.optionGroup}>
        {USE_CASES.map((uc) => {
          const selected = useCase === uc;
          return (
            <Pressable
              key={uc}
              style={({ pressed }) => [
                styles.optionButton,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setUseCase(uc)}
            >
              <Text
                style={[
                  styles.optionText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {uc}
              </Text>
            </Pressable>
          );
        })}
      </View>

      <Text
        style={[styles.sectionLabel, { color: c.textSecondary, marginTop: 28 }]}
      >
        TEAM SIZE
      </Text>
      <View style={styles.companySizeGrid}>
        {COMPANY_SIZES.map((size) => {
          const selected = companySize === size;
          return (
            <Pressable
              key={size}
              style={({ pressed }) => [
                styles.companySizeButton,
                {
                  backgroundColor: selected ? c.accent : c.bgCard,
                  borderColor: selected ? c.accent : c.border,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => setCompanySize(size)}
            >
              <Text
                style={[
                  styles.optionText,
                  { color: selected ? "#fff" : c.textPrimary },
                ]}
              >
                {size}
              </Text>
            </Pressable>
          );
        })}
      </View>
    </ScrollView>
  );

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]}>
    <KeyboardAvoidingView
      style={{ flex: 1 }}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      keyboardVerticalOffset={Platform.OS === "ios" ? 50 : 0}
    >
      {/* Progress dots */}
      <View style={styles.dotsContainer}>
        {Array.from({ length: dotCount }).map((_, i) => (
          <View
            key={i}
            style={[
              styles.dot,
              {
                width: i === currentDot ? 24 : 16,
                backgroundColor:
                  i === currentDot
                    ? c.textPrimary
                    : i < currentDot
                      ? c.textSecondary
                      : c.border,
              },
            ]}
          />
        ))}
      </View>

      {/* Page content */}
      <View style={styles.contentArea}>
        {page === 0 && renderNamePage()}
        {page === 1 && renderRolePage()}
        {page === 2 && renderRunnerPage()}
        {page === 3 && renderSpeechPage()}
        {page === 4 && renderVerbosityPage()}
        {page === 5 && renderRelayPage()}
        {page === 6 && isDev && renderPage1Dev()}
        {((page === 6 && !isDev) || (page === 7 && isDev)) &&
          renderUseCasePage()}
      </View>

      {/* Bottom buttons — hidden on name page (page 0) since it has inline Continue */}
      {page > 0 && <View style={styles.bottomButtons}>
        {page > 0 ? (
          <Pressable
            style={({ pressed }) => [
              styles.backButton,
              { borderColor: c.border },
              pressed && { opacity: 0.7 },
            ]}
            onPress={handleBack}
          >
            <Text style={[styles.backButtonText, { color: c.textSecondary }]}>
              Back
            </Text>
          </Pressable>
        ) : (
          <View />
        )}

        <Pressable
          style={({ pressed }) => [
            styles.nextButton,
            { backgroundColor: c.textPrimary },
            pressed && { opacity: 0.7 },
            (isSubmitting || (page === 0 && !fullName.trim()) || (page === 1 && identity === null) || (page === 2 && selectedRunner === "custom" && !customCommand.trim())) && {
              opacity: 0.4,
            },
          ]}
          onPress={handleNext}
          disabled={isSubmitting || (page === 0 && !fullName.trim()) || (page === 1 && identity === null) || (page === 2 && selectedRunner === "custom" && !customCommand.trim())}
        >
          <Text style={[styles.nextButtonText, { color: c.bg }]}>
            {isSubmitting ? "..." : isLastPage ? "Finish" : "Continue"}
          </Text>
        </Pressable>
      </View>}

      {/* Only show skip after relay page (page 5) has been passed */}
      {page >= 6 && (
        <Pressable
          style={({ pressed }) => [pressed && { opacity: 0.7 }]}
          onPress={finishSurvey}
          disabled={isSubmitting}
        >
          <Text
            style={[
              styles.skipText,
              { color: c.textMuted },
              isSubmitting && { opacity: 0.4 },
            ]}
          >
            Skip for now
          </Text>
        </Pressable>
      )}
    </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1 },
  dotsContainer: {
    flexDirection: "row",
    justifyContent: "center",
    alignItems: "center",
    gap: 8,
    paddingTop: 16,
    paddingBottom: 8,
  },
  dot: {
    height: 6,
    borderRadius: 3,
  },
  contentArea: {
    flex: 1,
  },
  pageContent: {
    paddingHorizontal: 24,
    paddingTop: 32,
  },
  pageTitle: {
    fontSize: 22,
    fontWeight: "600",
    marginBottom: 6,
    textAlign: "center",
    letterSpacing: -0.3,
  },
  pageSubtitle: {
    fontSize: 14,
    marginBottom: 28,
    textAlign: "center",
  },
  sectionLabel: {
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 1,
    marginBottom: 12,
  },
  nameInput: {
    borderWidth: 1,
    borderRadius: 12,
    paddingVertical: 14,
    paddingHorizontal: 16,
    fontSize: 15,
    marginBottom: 20,
  },
  inlineContinue: {
    alignSelf: "flex-end",
    paddingHorizontal: 28,
    paddingVertical: 12,
    borderRadius: 10,
  },
  identityGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 12,
  },
  identityButton: {
    width: "47%",
    paddingVertical: 28,
    borderRadius: 14,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  identityButtonText: {
    fontSize: 15,
    fontWeight: "600",
  },
  runnerDescText: {
    fontSize: 11,
    marginTop: 4,
    textAlign: "center",
  },
  runnerHint: {
    fontSize: 12,
    textAlign: "center",
    marginTop: 20,
  },
  chipContainer: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  chip: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 20,
    borderWidth: 1,
  },
  chipText: {
    fontSize: 13,
    fontWeight: "500",
  },
  optionGroup: {
    gap: 8,
  },
  optionButton: {
    paddingVertical: 12,
    paddingHorizontal: 14,
    borderRadius: 10,
    borderWidth: 1,
  },
  optionText: {
    fontSize: 14,
    fontWeight: "500",
  },
  companySizeGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  companySizeButton: {
    paddingVertical: 12,
    paddingHorizontal: 14,
    borderRadius: 10,
    borderWidth: 1,
    width: "31%",
    alignItems: "center",
  },
  bottomButtons: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingHorizontal: 24,
    paddingVertical: 12,
  },
  backButton: {
    paddingHorizontal: 20,
    paddingVertical: 12,
    borderRadius: 10,
    borderWidth: 1,
  },
  backButtonText: {
    fontSize: 14,
    fontWeight: "500",
  },
  nextButton: {
    paddingHorizontal: 28,
    paddingVertical: 12,
    borderRadius: 10,
  },
  nextButtonText: {
    fontSize: 14,
    fontWeight: "600",
  },
  skipText: {
    fontSize: 13,
    fontWeight: "500",
    textAlign: "center",
    paddingBottom: 12,
  },
});
