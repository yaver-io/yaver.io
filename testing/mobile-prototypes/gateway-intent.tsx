// gateway-intent.tsx — natural language routing via gateway_intent. Users can
// type natural language questions like "check my Google Calendar for today" or "show my GitHub
// repo stars" and get routed to the appropriate connector/capability. This is the
// conversational entry point for all gateway interactions.
//
// This surface uses runtimeSurfaceClient.gatewayIntent which returns routing information
// plus the structured answer when possible. For write operations, it returns a
// dry-run preview (act_id) that must be confirmed via gateway_act_confirm.

import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { runtimeSurfaceClient, type GatewayIntentResult } from "../src/lib/runtimeSurfaceClient";

type IntentState = "idle" | "routing" | "result" | "error" | "confirm";

export default function GatewayIntentScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice } = useDevice();

  const [utterance, setUtterance] = useState("");
  const [intentResult, setIntentResult] = useState<GatewayIntentResult | null>(null);
  const [intentState, setIntentState] = useState<IntentState>("idle");
  const [actId, setActId] = useState<string | null>(null);

  const deviceId = activeDevice?.id || "";
  const target: any = deviceId ? { id: deviceId } : undefined;

  const routeIntent = useCallback(async () => {
    const t = utterance.trim();
    if (!t) return;
    setIntentState("routing");
    setIntentResult(null);
    setActId(null);

    try {
      const result: GatewayIntentResult = await runtimeSurfaceClient.gatewayIntent(target, t);
      setIntentResult(result);
      setIntentState(result.actId ? "confirm" : "result");
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setIntentResult({
        routed: null,
        connector: null,
        capability: null,
        answer: null,
        actId: null,
        error: msg,
      });
      setIntentState("error");
    }
  }, [utterance, target]);

  const confirmAct = useCallback(
    async (answer: string) => {
      if (!actId || !target) return;
      setIntentState("routing");
      try {
        await runtimeSurfaceClient.gatewayActConfirm(target, actId, answer);
        setIntentResult({
          routed: null,
          connector: null,
          capability: null,
          answer: { result: "confirmed" },
          actId: null,
        });
        setIntentState("result");
        setActId(null);
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        setIntentResult({
          routed: null,
          connector: null,
          capability: null,
          answer: null,
          actId: null,
          error: msg,
        });
        setIntentState("error");
      }
    },
    [actId, target],
  );

  const clear = useCallback(() => {
    setUtterance("");
    setIntentResult(null);
    setIntentState("idle");
    setActId(null);
  }, []);

  const showInstructions = !intentResult && utterance.length === 0;

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <AppScreenHeader title="Gateway Intent" onBack={() => router.back()} />

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 40, gap: 16 }}>
        {showInstructions && (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[s.h2, { color: c.textPrimary, marginBottom: 8 }]}>Ask me anything</Text>
            <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 18 }}>
              I'll figure out which connector to use and fetch the answer. Ask about your Google
              Calendar, GitHub repos, or any connected service.
            </Text>
            <View style={{ flexDirection: "row", gap: 8, marginTop: 16, flexWrap: "wrap" }}>
              {[
                "What's on my Google Calendar today?",
                "Show my repo stars",
                "Check my GitHub notifications",
                "Read my last email",
              ].map((example, i) => (
                <Pressable
                  key={i}
                  onPress={() => {
                    setUtterance(example);
                    void routeIntent();
                  }}
                  style={[s.exampleBtn, { backgroundColor: c.bgCard, borderColor: c.border }]}
                >
                  <Text style={{ color: c.textPrimary, fontSize: 12 }}>{example}</Text>
                </Pressable>
              ))}
            </View>
          </View>
        )}

        <TextInput
          value={utterance}
          onChangeText={setUtterance}
          placeholder="What would you like to know?"
          placeholderTextColor={c.textMuted}
          style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
          multiline
          numberOfLines={4}
          textAlignVertical="top"
        />

        {utterance && (
          <Pressable
            onPress={routeIntent}
            disabled={intentState === "routing"}
            style={[
              s.button,
              { backgroundColor: c.accent, opacity: intentState === "routing" ? 0.7 : 1 },
            ]}
          >
            {intentState === "routing" ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text style={{ color: "#fff", fontSize: 15, fontWeight: "700" }}>Send</Text>
            )}
          </Pressable>
        )}

        {intentResult && (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 12 }}>
              <Text style={[s.h2, { color: c.textPrimary }]}>
                {intentState === "confirm" ? "⚠️ Review" : intentState === "result" ? "✓ Result" : "❌ Error"}
              </Text>
              <Pressable onPress={clear} style={[s.clearBtn, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>Clear</Text>
              </Pressable>
            </View>

            {intentState === "routing" && (
              <ActivityIndicator color={c.accent} style={{ marginVertical: 24 }} />
            )}

            {intentState === "confirm" && intentResult.connector && intentResult.capability && (
              <>
                <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 8 }}>
                  This is an{" "}
                  <Text style={{ color: c.textSecondary, fontWeight: "600" }}>
                    {intentResult.capability.verb === "get" ? "read" : "write"}
                  </Text>{" "}
                  operation.{" "}
                  {intentResult.capability.risk !== "read" && (
                    <Text style={{ color: c.textSecondary, fontWeight: "600" }}>
                      Risk: {intentResult.capability.risk}
                    </Text>
                  )}
                </Text>
                <Text style={{ color: c.textMuted, fontSize:13, marginBottom: 8 }}>
                  <Text style={{ color: c.accent, fontWeight: "600" }}>I'm about to:</Text>
                </Text>
                <Text style={[s.actionDesc, { color: c.textPrimary, fontSize: 14, marginBottom: 16 }]}>
                  {intentResult.capability.description || intentState.toString()}
                </Text>

                <View style={{ flexDirection: "row", gap: 12 }}>
                  <Pressable
                    onPress={() => confirmAct("approve")}
                    style={[s.btnSecondary, { flex: 1, backgroundColor: c.accent }]}
                  >
                    <Text style={{ color: "#fff", fontSize: 14, fontWeight: "700" }}>Approve</Text>
                  </Pressable>
                  <Pressable
                    onPress={() => confirmAct("deny")}
                    style={[s.btnSecondary, { flex: 1, backgroundColor: "#ef444422", borderColor: "#ef444455" }]}
                  >
                    <Text style={{ color: "#ef4444", fontSize: 14, fontWeight: "700" }}>Deny</Text>
                  </Pressable>
                </View>
              </>
            )}

            {intentState === "result" && (
              <Text style={[s.answer, { color: c.textPrimary, fontSize: 13 }]}>
                {typeof intentResult.answer === "object"
                  ? JSON.stringify(intentResult.answer, null, 2)
                  : String(intentResult.answer)}
              </Text>
            )}

            {intentState === "error" && (
              <Text style={[s.answer, { color: "#fca5a5", fontSize: 13 }]}>
                {intentResult.error}
              </Text>
            )}
          </View>
        )}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const s = StyleSheet.create({
  root: { flex: 1 },
  card: { borderWidth: 1, borderRadius: 12, padding: 16 },
  h2: { fontSize: 16, fontWeight: "700" },
  input: {
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 14,
    fontSize: 15,
    backgroundColor: c.bgCard,
  },
  button: { borderRadius: 12, paddingVertical: 14, alignItems: "center" },
  exampleBtn: { paddingHorizontal: 14, paddingVertical: 10, borderRadius: 10, borderWidth: 1 },
  clearBtn: { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, borderWidth: 1 },
  actionDesc: { backgroundColor: c.bgCard, padding: 12, borderRadius: 8, fontStyle: "italic" },
  btnSecondary: { paddingVertical: 12, borderRadius: 12, alignItems: "center" },
  answer: { backgroundColor: c.bgCard, padding: 14, borderRadius: 10, fontFamily: "monospace" },
});