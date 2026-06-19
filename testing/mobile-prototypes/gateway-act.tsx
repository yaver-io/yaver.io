// gateway-act.tsx — explicit gateway action surface. Users can preview actions
// (dry-run) and approve/execute them with confirmation. This is for write operations
// like charging an EV, buying tickets, placing orders, etc.
//
// This surface provides structured entry for connector/capability selection, parameter
// entry, and shows the dry-run preview before requiring explicit confirmation.

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
import { runtimeSurfaceClient } from "../src/lib/runtimeSurfaceClient";

type Connector = {
  id: string;
  engine: string;
  surface: string;
  capabilities: Capability[];
};

type Capability = {
  id: string;
  verb: "post" | "put" | "delete" | "get";
  risk: "read" | "low" | "medium" | "high";
  flow: { type: string; answerSchema?: Record<string, unknown> };
  description?: string;
};

type ActState = "idle" | "loading" | "preview" | "confirming" | "executing" | "success" | "error";

export default function GatewayActScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, devices, selectDevice } = useDevice();

  const [connectors, setConnectors] = useState<Connector[]>([]);
  const [selectedConnector, setSelectedConnector] = useState<string | null>(null);
  const [selectedCapability, setSelectedCapability] = useState<string | null>(null);
  const [params, setParams] = useState<Record<string, string>>({});
  const [dryRunResult, setDryRunResult] = useState<string | null>(null);
  const [actId, setActId] = useState<string | null>(null);
  const [actState, setActState] = useState<ActState>("idle");
  const [pickerOpen, setPickerOpen] = useState(false);

  const deviceId = activeDevice?.id || "";
  const target: any = deviceId ? { id: deviceId } : undefined;

  // Load connector list
  const loadConnectors = useCallback(async () => {
    try {
      const data: any = await runtimeSurfaceClient.gatewayQuery(target, "registry", "list");
      if (data.connectors && Array.isArray(data.connectors)) {
        // Filter to only show write-capable connectors (capabilities with write verbs)
        const writeConnectors = data.connectors.filter((c: Connector) =>
          (c.capabilities || []).some((cap: Capability) => cap.verb !== "get"),
        );
        setConnectors(writeConnectors);
      }
    } catch (e) {
      console.error("Failed to load connectors:", e);
    }
  }, [target]);

  // Initialize
  useEffect(() => {
    void loadConnectors();
  }, [loadConnectors]);

  // Dry-run (preview the action)
  const dryRun = useCallback(async () => {
    if (!selectedConnector || !selectedCapability) return;
    setActState("loading");
    setDryRunResult(null);
    setActId(null);

    try {
      const data: any = await runtimeSurfaceClient.gatewayActDryRun(target, selectedConnector, selectedCapability, params);
      setDryRunResult(JSON.stringify(data, null, 2));
      if (data.actId) {
        setActId(data.actId);
        setActState("confirming");
      } else {
        setActState("error");
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setDryRunResult(`Error: ${msg}`);
      setActState("error");
    }
  }, [target, selectedConnector, selectedCapability, params]);

  // Confirm and execute
  const executeAct = useCallback(
    async (answer: string) => {
      if (!actId || !target) return;
      setActState("executing");
      try {
        const data: any = await runtimeSurfaceClient.gatewayActConfirm(target, actId, answer);
        if (answer === "approve") {
          setActState("success");
        } else {
          setActState("error");
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        setDryRunResult(`Error: ${msg}`);
        setActState("error");
      }
    },
    [actId, target],
  );

  const clear = useCallback(() => {
    setSelectedConnector(null);
    setSelectedCapability(null);
    setParams({});
    setDryRunResult(null);
    setActId(null);
    setActState("idle");
  }, []);

  const connector = selectedConnector ? connectors.find((c) => c.id === selectedConnector) : null;
  const capability = selectedCapability && connector ? connector.capabilities.find((cap) => cap.id === selectedCapability) : null;
  const deviceLabel = activeDevice ? (activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name) : "local";

  const riskColor = (risk: string) => {
    switch (risk) {
      case "low":
        return "#34d399";
      case "medium":
        return "#f59e0b";
      case "high":
        return "#ef4444";
      default:
        return c.accent;
    }
  };

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <AppScreenHeader
        title="Gateway Actions"
        onBack={() => router.back()}
        right={
          <Pressable onPress={() => setPickerOpen(true)}>
            <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }} numberOfLines={1}>
              {deviceLabel} ▾
            </Text>
          </Pressable>
        }
      />

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 40, gap: 16 }}>
        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[s.section, { color: c.textPrimary }]}>1. Choose Connector</Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
            Select a connector with write capabilities
          </Text>
          {connectors.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>No write-capable connectors registered.</Text>
          ) : (
            <ScrollView horizontal showsHorizontalScrollIndicator={false}>
              <View style={{ flexDirection: "row", gap: 10 }}>
                {connectors.map((conn) => (
                  <Pressable
                    key={conn.id}
                    onPress={() => {
                      setSelectedConnector(conn.id);
                      setSelectedCapability(null);
                      setParams({});
                      setDryRunResult(null);
                      setActId(null);
                      setActState("idle");
                    }}
                    style={[
                      s.pill,
                      selectedConnector === conn.id
                        ? { backgroundColor: c.accent }
                        : { backgroundColor: c.bgCard, borderColor: c.border },
                    ]}
                  >
                    <Text
                      style={{
                        color: selectedConnector === conn.id ? c.bg : c.textPrimary,
                        fontSize: 13,
                        fontWeight: "600",
                      }}
                    >
                      {conn.id}
                    </Text>
                  </Pressable>
                ))}
              </View>
            </ScrollView>
          )}
        </View>

        {connector ? (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[s.section, { color: c.textPrimary }]}>2. Choose Action</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
              Select a write capability to preview and execute
            </Text>
            {connector.capabilities && connector.capabilities.length > 0 ? (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                {connector.capabilities.map((cap) => (
                  <Pressable
                    key={cap.id}
                    onPress={() => {
                      setSelectedCapability(cap.id);
                      setParams({});
                      setDryRunResult(null);
                      setActId(null);
                      setActState("idle");
                    }}
                    style={[
                      s.pill,
                      selectedCapability === cap.id
                        ? { backgroundColor: c.accent }
                        : { backgroundColor: c.bgCard, borderColor: c.border },
                    ]}
                  >
                    <Text
                      style={{
                        color: selectedCapability === cap.id ? c.bg : c.textPrimary,
                        fontSize: 12,
                        fontWeight: selectedCapability === cap.id ? "700" : "500",
                      }}
                    >
                      {cap.id} ({cap.risk})
                    </Text>
                  </Pressable>
                ))}
              </View>
            ) : (
              <Text style={{ color: c.textMuted, fontSize: 13 }}>No write capabilities available.</Text>
            )}
          </View>
        ) : null}

        {capability ? (
          <>
            <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[s.section, { color: c.textPrimary }]}>3. Parameters</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
                Add action parameters as key=value pairs
              </Text>
              <TextInput
                placeholder='e.g., "timeMin=2024-01-01T09:00" (without quotes)'
                placeholderTextColor={c.textMuted}
                style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
                value={Object.entries(params)
                  .map(([k, v]) => `${k}=${v}`)
                  .join(", ")}
                onChangeText={(text) => {
                  const newParams: Record<string, string> = {};
                  text.split(",").forEach((pair) => {
                    const [k, v] = pair.split("=");
                    if (k && v) newParams[k.trim()] = v.trim();
                  });
                  setParams(newParams);
                }}
              />
            </View>

            <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[s.section, { color: c.textPrimary }]}>4. Risk Assessment</Text>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 8 }}>
                <View style={[s.riskBadge, { backgroundColor: `${riskColor(capability.risk)}22` }]}>
                  <Text style={{ color: riskColor(capability.risk), fontSize: 11, fontWeight: "700" }}>
                    {capability.risk.toUpperCase()}
                  </Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>
                  {" "}
                  {capability.risk === "high"
                    ? "Requires explicit confirmation"
                    : capability.risk === "medium"
                      ? "Requires approval"
                      : "Can auto-execute (dry-run still shown)"}
                </Text>
              </View>
              {capability.description && (
                <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }}>
                  {capability.description}
                </Text>
              )}
            </View>
          </>
        ) : null}

        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Pressable
            onPress={dryRun}
            disabled={!selectedConnector || !selectedCapability || actState === "loading" || actState === "preview"}
            style={[
              s.button,
              {
                backgroundColor: selectedConnector && selectedCapability ? c.accent : c.border,
                opacity: (!selectedConnector || !selectedCapability) ? 0.5 : 1,
              },
            ]}
          >
            {actState === "loading" ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text
                style={{
                  color:
                    selectedConnector && selectedCapability ? c.bg : c.textSecondary,
                  fontSize: 14,
                  fontWeight: "700",
                }}
              >
                Preview Action
              </Text>
            )}
          </Pressable>
        </View>

        {dryRunResult && (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[s.section, { color: c.textPrimary }]}>Preview Result</Text>
            <ScrollView style={{ maxHeight: 250 }}>
              <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: "monospace" }}>
                {dryRunResult}
              </Text>
            </ScrollView>

            {actState === "confirming" && actId && (
              <View style={{ flexDirection: "row", gap: 12, marginTop: 16 }}>
                <Pressable
                  onPress={() => executeAct("approve")}
                  style={[s.btnAction, { flex: 1, backgroundColor: c.accent }]}
                >
                  <Text style={{ color: "#fff", fontSize: 14, fontWeight: "700" }}>Approve & Execute</Text>
                </Pressable>
                <Pressable
                  onPress={() => executeAct("deny")}
                  style={[s.btnAction, { flex: 1, backgroundColor: "#ef444422", borderColor: "#ef444455" }]}
                >
                  <Text style={{ color: "#ef4444", fontSize: 14, fontWeight: "700" }}>Cancel</Text>
                </Pressable>
              </View>
            )}

            {actState === "executing" && (
              <ActivityIndicator color={c.accent} style={{ marginVertical: 16 }} />
            )}

            {(actState === "success" || actState === "error") && (
              <Pressable onPress={clear} style={[s.btnClear, { marginTop: 16, alignSelf: "flex-start", borderWidth: 1, borderColor: c.border }]}>
                <Text style={{ color: c.textMuted, fontSize: 13 }}>Clear</Text>
              </Pressable>
            )}
          </View>
        )}
      </ScrollView>

      {/* Device picker */}
      <Modal visible={pickerOpen} transparent animationType="fade" onRequestClose={() => setPickerOpen(false)}>
        <Pressable style={s.backdrop} onPress={() => setPickerOpen(false)}>
          <Pressable style={[s.pickerCard, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={() => {}}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 10 }}>
              Choose a device
            </Text>
            <ScrollView style={{ maxHeight: 360 }}>
              {devices.map((d) => {
                const isActive = activeDevice?.id === d.id;
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => {
                      setPickerOpen(false);
                      selectDevice(d).catch(() => {});
                    }}
                    style={[s.deviceRow, { borderBottomColor: c.border }]}
                  >
                    <View style={[s.dot, { backgroundColor: d.online ? "#34d399" : "#6b7280" }]} />
                    <Text
                      style={{
                        color: isActive ? c.accent : c.textPrimary,
                        fontSize: 14,
                        fontWeight: isActive ? "700" : "500",
                        flex: 1,
                      }}
                      numberOfLines={1}
                    >
                      {d.alias ? `@${d.alias}` : d.name}
                    </Text>
                    {isActive ? <Text style={{ color: c.accent, fontSize: 11 }}>active</Text> : null}
                  </Pressable>
                );
              })}
              {devices.length === 0 ? <Text style={{ color: c.textMuted, fontSize: 13 }}>No devices.</Text> : null}
            </ScrollView>
          </Pressable>
        </Pressable>
      </Modal>
    </KeyboardAvoidingView>
  );
}

const s = StyleSheet.create({
  root: { flex: 1 },
  card: { borderWidth: 1, borderRadius: 12, padding: 16 },
  section: { fontSize: 15, fontWeight: "700", marginBottom: 8 },
  pill: { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, borderWidth: 1 },
  input: { borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14, minHeight: 44 },
  button: { borderRadius: 10, paddingVertical: 14, alignItems: "center" },
  riskBadge: { paddingHorizontal: 8, paddingVertical: 3, borderRadius: 6 },
  btnAction: { paddingVertical: 12, borderRadius: 10, alignItems: "center", flex: 1 },
  btnClear: { paddingHorizontal: 14, paddingVertical: 8, borderRadius: 8 },
  backdrop: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", alignItems: "center", justifyContent: "center", padding: 24 },
  pickerCard: { width: "100%", maxWidth: 420, borderRadius: 14, borderWidth: 1, padding: 16 },
  deviceRow: { flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  dot: { width: 8, height: 8, borderRadius: 4 },
});
