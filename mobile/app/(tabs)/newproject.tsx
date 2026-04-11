import React, { useCallback, useEffect, useState } from "react";
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
  WizardQuestion,
  WizardSession,
  WizardGenerateResult,
} from "../../src/lib/quic";

// Mobile driver for the project_wizard state machine on the
// agent. Walks a non-developer through the same Q&A that
// `yaver new` exposes in the terminal, and ends with a
// "generate" button that materialises the full scaffold on the
// agent's filesystem. The dev then opens the resulting directory
// in their editor and follows SETUP.md for the parts that need
// external signups (OAuth, App Store keys, Cloudflare DNS).

export default function NewProjectScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [session, setSession] = useState<WizardSession | null>(null);
  const [question, setQuestion] = useState<WizardQuestion | null>(null);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<WizardGenerateResult | null>(null);

  const start = useCallback(async () => {
    setLoading(true);
    setError(null);
    setResult(null);
    const res = await quicClient.wizardStart();
    setLoading(false);
    if (!res) {
      setError("Could not start the wizard — is the agent reachable?");
      return;
    }
    setSession(res.session);
    setQuestion(res.question);
    setInput(res.question?.default ?? "");
  }, []);

  useEffect(() => {
    if (connected && !session) start();
  }, [connected, session, start]);

  const submit = useCallback(async () => {
    if (!session || !question) return;
    setLoading(true);
    setError(null);
    const answer = input.trim() || (question.default ?? "");
    const res = await quicClient.wizardAnswer(session.id, question.id, answer);
    setLoading(false);
    if (!res) {
      setError("Could not save that answer — try again.");
      return;
    }
    setSession(res.session);
    setQuestion(res.question);
    setInput(res.question?.default ?? "");
  }, [session, question, input]);

  const generate = useCallback(async () => {
    if (!session) return;
    setLoading(true);
    setError(null);
    const res = await quicClient.wizardGenerate(session.id);
    setLoading(false);
    if (!res || !res.ok) {
      setError("Generation failed — check agent logs.");
      return;
    }
    setResult(res);
  }, [session]);

  const done = question?.kind === "done";
  const confirm = question?.kind === "confirm";

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>New Project</Text>
        <View style={{ width: 50 }} />
      </View>

      {!connected ? (
        <View style={styles.center}>
          <Text style={{ color: c.textMuted }}>Not connected to an agent.</Text>
        </View>
      ) : result ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          <Text style={[styles.bigTitle, { color: c.textPrimary }]}>
            ✓ Project generated
          </Text>
          <Text style={[styles.meta, { color: c.textMuted, marginBottom: 12 }]}>
            {result.directory}
          </Text>
          <Text style={[styles.sectionTitle, { color: c.textPrimary }]}>
            Next steps
          </Text>
          {result.nextSteps.map((s, i) => (
            <Text key={i} style={[styles.bullet, { color: c.textPrimary }]}>
              • {s}
            </Text>
          ))}
          <Text style={[styles.meta, { color: c.textMuted, marginTop: 24 }]}>
            Everything that still needs external signups (OAuth, Cloudflare
            zone, Apple / Play Store keys) is listed in SETUP.md inside the
            generated project.
          </Text>
          <Pressable
            style={[styles.button, { backgroundColor: c.accent, marginTop: 24 }]}
            onPress={() => {
              setResult(null);
              setSession(null);
              start();
            }}
          >
            <Text style={styles.buttonText}>Generate another</Text>
          </Pressable>
        </ScrollView>
      ) : !question || loading ? (
        <View style={styles.center}>
          <ActivityIndicator />
        </View>
      ) : (
        <ScrollView contentContainerStyle={{ padding: 20 }} keyboardShouldPersistTaps="handled">
          {error ? <Text style={{ color: c.error, marginBottom: 12 }}>{error}</Text> : null}

          <Text style={[styles.prompt, { color: c.textPrimary }]}>{question.prompt}</Text>
          {question.help ? (
            <Text style={[styles.help, { color: c.textMuted }]}>{question.help}</Text>
          ) : null}

          {question.kind === "choice" ? (
            <View style={styles.choiceBox}>
              {(question.choices ?? []).map((choice) => (
                <Pressable
                  key={choice}
                  onPress={() => setInput(choice)}
                  style={[
                    styles.choice,
                    {
                      backgroundColor: input === choice ? c.accent : c.bgCard,
                      borderColor: c.border,
                    },
                  ]}
                >
                  <Text
                    style={{
                      color: input === choice ? "#fff" : c.textPrimary,
                      fontWeight: "600",
                    }}
                  >
                    {choice}
                  </Text>
                </Pressable>
              ))}
            </View>
          ) : question.kind === "bool" ? (
            <View style={styles.choiceBox}>
              {["true", "false"].map((choice) => (
                <Pressable
                  key={choice}
                  onPress={() => setInput(choice)}
                  style={[
                    styles.choice,
                    {
                      backgroundColor: input === choice ? c.accent : c.bgCard,
                      borderColor: c.border,
                    },
                  ]}
                >
                  <Text
                    style={{
                      color: input === choice ? "#fff" : c.textPrimary,
                      fontWeight: "600",
                    }}
                  >
                    {choice === "true" ? "Yes" : "No"}
                  </Text>
                </Pressable>
              ))}
            </View>
          ) : done ? (
            <Text style={[styles.help, { color: c.textMuted, marginTop: 20 }]}>
              All questions answered. Tap generate to create the scaffold on
              the agent.
            </Text>
          ) : (
            <TextInput
              value={input}
              onChangeText={setInput}
              placeholder={question.default ?? ""}
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[
                styles.input,
                {
                  color: c.textPrimary,
                  backgroundColor: c.bgInput,
                  borderColor: c.border,
                },
              ]}
            />
          )}

          {done || confirm ? (
            <Pressable
              style={[styles.button, { backgroundColor: c.accent, marginTop: 24 }]}
              onPress={generate}
            >
              <Text style={styles.buttonText}>Generate project</Text>
            </Pressable>
          ) : (
            <Pressable
              style={[styles.button, { backgroundColor: c.accent, marginTop: 24 }]}
              onPress={submit}
            >
              <Text style={styles.buttonText}>Next</Text>
            </Pressable>
          )}
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
  prompt: { fontSize: 20, fontWeight: "700" },
  help: { fontSize: 13, marginTop: 6, marginBottom: 16 },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 14,
    fontSize: 16,
  },
  choiceBox: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 12 },
  choice: {
    paddingVertical: 10,
    paddingHorizontal: 18,
    borderRadius: 9999,
    borderWidth: 1,
  },
  button: {
    paddingVertical: 14,
    borderRadius: 10,
    alignItems: "center",
  },
  buttonText: { color: "#fff", fontWeight: "700" },
  bigTitle: { fontSize: 24, fontWeight: "700", marginBottom: 4 },
  meta: { fontSize: 13 },
  sectionTitle: { fontSize: 16, fontWeight: "700", marginTop: 16, marginBottom: 8 },
  bullet: { fontSize: 14, marginBottom: 6 },
});
