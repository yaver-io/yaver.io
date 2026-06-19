// gateway-query.tsx — read-only gateway queries surface. Users can ask
// connectors questions without triggering writes. Example: Google Calendar "next_event?",
// GitHub "repo status", etc. Tokens auto-refresh; human gates only fire on first
// access or on token expiry.
//
// This surface exposes the gateway_query ops verb through runtimeSurfaceClient,
// providing a phone-friendly UI for connector-capability browsing and querying.

import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
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
  verb: "get";
  risk: "read";
  flow: { type: "api"; method: string; path?: string; answerSchema: Record<string, unknown> };
  description?: string;
};

type QueryState = "idle" | "querying" | "success" | "error";

export default function GatewayQueryScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, devices, selectDevice } = useDevice();

  const [connectors, setConnectors] = useState<Connector[]>([]);
  const [selectedConnector, setSelectedConnector] = useState<string | null>(null);
  const [selectedCapability, setSelectedCapability] = useState<string | null>(null);
  const [params, setParams] = useState<Record<string, string>>({});
  const [queryResult, setQueryResult] = useState<string | null>(null);
  const [queryState, setQueryState] = useState<QueryState>("idle");
  const [loading, setLoading] = useState(true);
  const [pickerOpen, setPickerOpen] = useState(false);

  const deviceId = activeDevice?.id || "";
  const target: any = deviceId ? { id: deviceId } : undefined;

  // Load connector list
  const loadConnectors = useCallback(async () => {
    try {
      const data: any = await runtimeSurfaceClient.gatewayQuery(target, "registry", "list");
      if (data.connectors && Array.isArray(data.connectors)) {
        setConnectors(data.connectors);
      }
    } catch (e) {
      console.error("Failed to load connectors:", e);
    } finally {
      setLoading(false);
    }
  }, [target]);

  // Load connector capabilities
  const loadCapabilities = useCallback(
    async (connectorId: string) => {
      try {
        const data: any = await runtimeSurfaceClient.gatewayQuery(target, connectorId, "capabilities");
        if (data.capabilities && Array.isArray(data.capabilities)) {
          setConnectors((prev) => prev.map((c) => (c.id === connectorId ? { ...c, capabilities: data.capabilities } : c)));
        }
      } catch (e) {
        console.error("Failed to load capabilities:", e);
      }
    },
    [target],
  );

  // Execute query
  const executeQuery = useCallback(async () => {
    if (!selectedConnector || !selectedCapability) return;
    setQueryState("querying");
    setQueryResult(null);
    try {
      const data: any = await runtimeSurfaceClient.gatewayQuery(target, selectedConnector, selectedCapability, params);
      setQueryResult(JSON.stringify(data, null, 2));
      setQueryState("success");
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setQueryResult(`Error: ${msg}`);
      setQueryState("error");
    }
  }, [target, selectedConnector, selectedCapability, params]);

  // Initialize
  useEffect(() => {
    setLoading(true);
    void loadConnectors();
  }, [loadConnectors]);

  // When connector is selected, load its capabilities if needed
  useEffect(() => {
    if (selectedConnector) {
      const conn = connectors.find((c) => c.id === selectedConnector);
      if (conn && (!conn.capabilities || conn.capabilities.length === 0)) {
        void loadCapabilities(selectedConnector);
      }
    }
  }, [selectedConnector, connectors, loadCapabilities]);

  const connector = selectedConnector ? connectors.find((c) => c.id === selectedConnector) : null;
  const capability = selectedCapability && connector ? connector.capabilities.find((cap) => cap.id === selectedCapability) : null;

  const deviceLabel = activeDevice ? (activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name) : "local";

  return (
    <View style={[s.root, { backgroundColor: c.bg }]}>
      <AppScreenHeader
        title="Gateway Queries"
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
            Select a connector to query (read-only access only)
          </Text>
          {loading ? (
            <ActivityIndicator color={c.accent} />
          ) : connectors.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>No connectors registered.</Text>
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
                      setQueryResult(null);
                      setQueryState("idle");
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
            <Text style={[s.section, { color: c.textPrimary }]}>2. Choose Capability</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
              Select a read-only capability to query
            </Text>
            {connector.capabilities && connector.capabilities.length > 0 ? (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                {connector.capabilities.map((cap) => (
                  <Pressable
                    key={cap.id}
                    onPress={() => {
                      setSelectedCapability(cap.id);
                      setParams({});
                      setQueryResult(null);
                      setQueryState("idle");
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
                      {cap.id}
                    </Text>
                  </Pressable>
                ))}
              </View>
            ) : (
              <Text style={{ color: c.textMuted, fontSize: 13 }}>No capabilities available.</Text>
            )}
          </View>
        ) : null}

        {capability ? (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[s.section, { color: c.textPrimary }]}>3. Parameters (optional)</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
              Add query parameters as key=value pairs
            </Text>
            <TextInput
              placeholder='e.g., "timeMin=2024-01-01T09:00" (without quotes)'
              placeholderTextColor={c.textMuted}
              style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
              value={Object.entries(params)
                .map(([k, v]) => `${k}=${v}`)
                .join(", ")}
              onChangeText={(text) => {
                // Parse simple key=value pairs
                const newParams: Record<string, string> = {};
                text.split(",").forEach((pair) => {
                  const [k, v] = pair.split("=");
                  if (k && v) newParams[k.trim()] = v.trim();
                });
                setParams(newParams);
              }}
            />
          </View>
        ) : null}

        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Pressable
            onPress={executeQuery}
            disabled={!selectedConnector || !selectedCapability || queryState === "querying"}
            style={[
              s.button,
              { backgroundColor: c.accent, opacity: (!selectedConnector || !selectedCapability) ? 0.5 : 1 },
            ]}
          >
            {queryState === "querying" ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text style={{ color: "#fff", fontSize: 14, fontWeight: "700" }}>Query Connector</Text>
            )}
          </Pressable>
        </View>

        {queryResult && (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", marginBottom: 8 }}>
              <Text style={[s.section, { color: c.textPrimary }]}>Result</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                {queryState === "success" ? "✓" : "✗"}
              </Text>
            </View>
            <ScrollView style={{ maxHeight: 300 }}>
              <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: "monospace" }}>
                {queryResult}
              </Text>
            </ScrollView>
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
    </View>
  );
}

const s = StyleSheet.create({
  root: { flex: 1 },
  card: { borderWidth: 1, borderRadius: 12, padding: 16 },
  section: { fontSize: 15, fontWeight: "700", marginBottom: 8 },
  pill: { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, borderWidth: 1 },
  input: { borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14, minHeight: 44 },
  button: { borderRadius: 10, paddingVertical: 12, alignItems: "center" },
  backdrop: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", alignItems: "center", justifyContent: "center", padding: 24 },
  pickerCard: { width: "100%", maxWidth: 420, borderRadius: 14, borderWidth: 1, padding: 16 },
  deviceRow: { flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  dot: { width: 8, height: 8, borderRadius: 4 },
});
