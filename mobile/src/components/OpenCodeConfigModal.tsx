// OpenCodeConfigModal.tsx — mobile counterpart to the web ToolsView's
// OpenCode section. Drives the same /runner/opencode/config endpoint
// on the connected device, so a phone can configure a Mac mini's
// opencode.json without SSH.
//
// Shows:
//   - Path + exists indicator + diagnostics banner (if any)
//   - Default agent + model fields (editable)
//   - Agents list (build, plan, plus any custom agent.<name> entries)
//   - Providers list with baseURL + API-key edit buttons
//   - Provider preset chips (Z.ai/GLM, Groq, OpenRouter, Together,
//     Local Ollama, Tailscale Ollama, DeepSeek)

import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { quicClient, type OpenCodeConfigSummary, type OpenCodeProviderSummary } from "../lib/quic";
import { useColors } from "../context/ThemeContext";
import { useDevice } from "../context/DeviceContext";

interface Props {
  visible: boolean;
  onClose: () => void;
  startInAddProvider?: boolean;
  /** Device to configure. Omit for the actively-connected box; pass a peer's
   *  deviceId to configure a box you're viewing but not connected to (so the
   *  provider/key/model land on the RIGHT box, not the connected one). */
  target?: string;
}

// Pre-fills for the most common providers. Same set the web
// ToolsView uses; keep them in sync.
const PRESETS: Array<{ label: string; id: string; name: string; baseUrl?: string; hint: string; model?: string; models?: Record<string, unknown> }> = [
  { label: "OpenAI", id: "openai", name: "OpenAI", hint: "GPT-5 family via your OpenAI API key." },
  { label: "Anthropic", id: "anthropic", name: "Anthropic", hint: "Claude models via your Anthropic API key." },
  { label: "Gemini", id: "google", name: "Google Gemini", hint: "Gemini models via GEMINI_API_KEY. OpenCode knows the stock Google provider." },
  {
    label: "Z.ai Coding Plan",
    id: "zai-coding-plan",
    name: "Zai Coding Plan (GLM 4.7)",
    model: "zai-coding-plan/glm-4.7",
    hint: "GLM 4.7 Coding Plan using OpenCode's built-in provider. This is the Hetzner/OpenCode default.",
  },
  { label: "Z.ai API (GLM-4.7)", id: "zai", name: "Zai API (GLM 4.7)", baseUrl: "https://api.zai.ai/v1", model: "zai/glm-4.7", models: { "glm-4.7": { name: "GLM 4.7", tools: true }, "glm-4.7-flash": { name: "GLM 4.7 Flash", tools: true } }, hint: "General z.ai OpenAI-compatible endpoint." },
  { label: "Zhipu OpenAPI (GLM-4)", id: "glm", name: "Zhipu (open.bigmodel.cn)", baseUrl: "https://open.bigmodel.cn/api/paas/v4", hint: "Legacy GLM-4 / GLM-4V via open.bigmodel.cn. Different key from Z.ai Coding Plan." },
  { label: "Groq", id: "groq", name: "Groq", baseUrl: "https://api.groq.com/openai/v1", hint: "Fast Llama / Mixtral / Qwen. Key from console.groq.com." },
  { label: "OpenRouter", id: "openrouter", name: "OpenRouter", baseUrl: "https://openrouter.ai/api/v1", hint: "Aggregator across most models. openrouter.ai." },
  { label: "Together", id: "together", name: "Together AI", baseUrl: "https://api.together.xyz/v1", hint: "Open-weight models. api.together.xyz." },
  { label: "Local Ollama", id: "ollama", name: "Ollama (local)", baseUrl: "http://127.0.0.1:11434/v1", hint: "Local Ollama on the dev box. No API key needed." },
  { label: "Tailscale Ollama", id: "ollama-tailscale", name: "Ollama (Tailscale)", baseUrl: "http://yaver-gpu.tailscale.net:11434/v1", hint: "Edit the host to match your tailnet." },
  { label: "DeepSeek", id: "deepseek", name: "DeepSeek", baseUrl: "https://api.deepseek.com", hint: "DeepSeek-Coder/V3. Key from platform.deepseek.com." },
];

export function OpenCodeConfigModal({ visible, onClose, startInAddProvider = false, target }: Props) {
  const c = useColors();
  // All writes go to `target` (the box being configured), not the connected one.
  const saveConfig = (patch: Parameters<typeof quicClient.saveOpenCodeConfig>[0]) =>
    quicClient.saveOpenCodeConfig(patch, target);
  // Sync the device's primary runner choice to Convex once the user
  // configures a working provider+key. Without this the user has to ALSO
  // tap the runner picker in DeviceDetailsModal to flip the device's
  // primary to opencode — surfaces like the Tasks composer placeholder
  // ("Chat with Codex" vs "Chat with OpenCode") still read the old
  // userSettings.primaryRunnerByDevice value until then. The saved key
  // is the explicit "this is now my coding agent" signal — pin it.
  const { activeDevice, primaryRunnerByDevice, setPrimaryRunnerForDevice } = useDevice();
  const [config, setConfig] = useState<OpenCodeConfigSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [defaultAgent, setDefaultAgent] = useState("");
  const [model, setModel] = useState("");
  const [smallModel, setSmallModel] = useState("");
  const [buildModel, setBuildModel] = useState("");
  const [planModel, setPlanModel] = useState("");
  const [editingProvider, setEditingProvider] = useState<OpenCodeProviderSummary | null>(null);
  const [editBaseUrl, setEditBaseUrl] = useState("");
  const [editApiKey, setEditApiKey] = useState("");
  const [showAdd, setShowAdd] = useState(false);
  const [addId, setAddId] = useState("");
  const [addName, setAddName] = useState("");
  const [addBaseUrl, setAddBaseUrl] = useState("");
  const [addApiKey, setAddApiKey] = useState("");
  const [addModel, setAddModel] = useState("");
  const [addModels, setAddModels] = useState<Record<string, unknown> | undefined>(undefined);
  const [presetHint, setPresetHint] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const cfg = await quicClient.getOpenCodeConfig(target);
      setConfig(cfg);
      if (cfg) {
        setDefaultAgent(cfg.defaultAgent || "");
        setModel(cfg.model || "");
        setSmallModel(cfg.smallModel || "");
        setBuildModel(cfg.buildModel || "");
        setPlanModel(cfg.planModel || "");
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (visible) void load();
  }, [visible, load]);

  useEffect(() => {
    if (!visible) {
      setShowAdd(false);
      setAddId("");
      setAddName("");
      setAddBaseUrl("");
      setAddApiKey("");
      setAddModel("");
      setAddModels(undefined);
      setPresetHint("");
      return;
    }
    if (startInAddProvider) setShowAdd(true);
  }, [visible, startInAddProvider]);

  const saveTopLevel = useCallback(async () => {
    setBusy(true);
    const res = await saveConfig({
      defaultAgent: defaultAgent.trim() || undefined,
      model: model.trim() || undefined,
      smallModel: smallModel.trim() || undefined,
      buildModel: buildModel.trim() || undefined,
      planModel: planModel.trim() || undefined,
    });
    setBusy(false);
    if (!res.ok) {
      Alert.alert("Save failed", res.error || "Unknown error");
      return;
    }
    if (res.config) setConfig(res.config);
    if (activeDevice) {
      // opencode model strings are "<provider>/<model>" (e.g.
      // "zai/glm-4.7"). Surface the provider half to Convex too —
      // without it, web's DevicesView can't infer which catalogue
      // entry to highlight and falls back to OPENCODE_PROVIDER_CATALOGUE[0].
      const m = (res.config?.model || "").trim();
      const slash = m.indexOf("/");
      const providerHint = slash > 0 ? m.slice(0, slash) : "";
      void setPrimaryRunnerForDevice(
        activeDevice.id,
        "opencode",
        m || null,
        res.config?.defaultAgent || null,
        providerHint || null,
      ).catch(() => {});
    }
    Alert.alert("Saved", "OpenCode config updated.");
  }, [defaultAgent, model, smallModel, buildModel, planModel, activeDevice, setPrimaryRunnerForDevice]);

  const saveProviderEdit = useCallback(async () => {
    if (!editingProvider) return;
    const apiKeyTrimmed = editApiKey.trim();
    setBusy(true);
    const res = await saveConfig({
      providers: [
        {
          id: editingProvider.id,
          baseUrl: editBaseUrl.trim() || undefined,
          apiKey: apiKeyTrimmed || undefined,
        },
      ],
    });
    setBusy(false);
    if (!res.ok) {
      Alert.alert("Save failed", res.error || "Unknown error");
      return;
    }
    if (res.config) setConfig(res.config);
    if (activeDevice) {
      void setPrimaryRunnerForDevice(
        activeDevice.id,
        "opencode",
        res.config?.model || null,
        res.config?.defaultAgent || null,
        editingProvider.id,
      ).catch(() => {});
    }
    setEditingProvider(null);
    setEditBaseUrl("");
    setEditApiKey("");
  }, [editingProvider, editBaseUrl, editApiKey, activeDevice, primaryRunnerByDevice, setPrimaryRunnerForDevice]);

  const addProvider = useCallback(async () => {
    if (!addId.trim()) {
      Alert.alert("Provider id required", "Use a short id like 'glm', 'groq', or 'ollama-tailscale'.");
      return;
    }
    const apiKeyTrimmed = addApiKey.trim();
    const modelTrimmed = addModel.trim();
    setBusy(true);
    const res = await saveConfig({
      defaultAgent: modelTrimmed ? "build" : undefined,
      model: modelTrimmed || undefined,
      providers: [
        {
          id: addId.trim(),
          name: addName.trim() || undefined,
          baseUrl: addBaseUrl.trim() || undefined,
          apiKey: apiKeyTrimmed || undefined,
          models: addModels,
        },
      ],
    });
    setBusy(false);
    if (!res.ok) {
      Alert.alert("Save failed", res.error || "Unknown error");
      return;
    }
    if (res.config) setConfig(res.config);
    if (activeDevice) {
      void setPrimaryRunnerForDevice(
        activeDevice.id,
        "opencode",
        res.config?.model || modelTrimmed || null,
        res.config?.defaultAgent || null,
        addId.trim(),
      ).catch(() => {});
    }
    setShowAdd(false);
    setAddId("");
    setAddName("");
    setAddBaseUrl("");
    setAddApiKey("");
    setAddModel("");
    setAddModels(undefined);
    setPresetHint("");
  }, [addId, addName, addBaseUrl, addApiKey, addModel, addModels, activeDevice, primaryRunnerByDevice, setPrimaryRunnerForDevice]);

  const deleteProvider = useCallback(
    (provider: OpenCodeProviderSummary) => {
      Alert.alert(
        "Delete provider?",
        `Remove "${provider.id}" from opencode.json? This won't touch your API key vault entries.`,
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Delete",
            style: "destructive",
            onPress: async () => {
              setBusy(true);
              const res = await saveConfig({
                providers: [{ id: provider.id, delete: true }],
              });
              setBusy(false);
              if (!res.ok) {
                Alert.alert("Delete failed", res.error || "Unknown error");
                return;
              }
              if (res.config) setConfig(res.config);
            },
          },
        ],
      );
    },
    [],
  );

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="formSheet" onRequestClose={onClose}>
      <View style={[styles.container, { backgroundColor: c.bg }]}>
        <View style={[styles.header, { borderBottomColor: c.border }]}>
          <Pressable onPress={onClose} hitSlop={12} style={styles.headerBtn}>
            <Text style={{ color: c.accent, fontSize: 16 }}>Close</Text>
          </Pressable>
          <Text style={[styles.title, { color: c.textPrimary }]}>OpenCode Config</Text>
          <Pressable onPress={load} hitSlop={12} style={styles.headerBtn}>
            <Text style={{ color: c.accent, fontSize: 14 }}>Refresh</Text>
          </Pressable>
        </View>

        <ScrollView contentContainerStyle={{ padding: 16 }}>
          {loading ? (
            <ActivityIndicator color={c.accent} style={{ marginTop: 32 }} />
          ) : !config ? (
            <Text style={[styles.muted, { color: c.textMuted }]}>
              Couldn't load opencode config — make sure a device is connected.
            </Text>
          ) : (
            <>
              <Text style={[styles.muted, { color: c.textMuted, fontFamily: "Menlo", fontSize: 11 }]}>
                Path: {config.path}
              </Text>
              <Text style={[styles.muted, { color: c.textMuted, fontSize: 11 }]}>
                {config.exists ? "✓ file exists on the device" : "(file will be created on first save)"}
              </Text>

              {/* Diagnostics — same shape as web ToolsView. */}
              {config.diagnostics && config.diagnostics.length > 0 ? (
                <View style={[styles.warnCard, { borderColor: "#f59e0b66", backgroundColor: "#f59e0b18" }]}>
                  <Text style={{ color: "#fcd34d", fontSize: 11, fontWeight: "700", marginBottom: 6 }}>
                    ⚠ Configuration issues
                  </Text>
                  {config.diagnostics.map((d, i) => (
                    <Text key={i} style={{ color: "#fde68a", fontSize: 12, marginBottom: 2 }}>• {d}</Text>
                  ))}
                </View>
              ) : null}

              <Section title="Default Agent + Models" color={c.textSecondary}>
                <Field label="Default agent" value={defaultAgent} onChange={setDefaultAgent} placeholder="build or plan" c={c} />
                <Field label="Default model" value={model} onChange={setModel} placeholder="provider/model" c={c} />
                <Field label="Small model" value={smallModel} onChange={setSmallModel} placeholder="provider/model" c={c} />
                <Field label="Build model" value={buildModel} onChange={setBuildModel} placeholder="provider/model" c={c} />
                <Field label="Plan model" value={planModel} onChange={setPlanModel} placeholder="provider/model" c={c} />
                <Pressable
                  onPress={saveTopLevel}
                  disabled={busy}
                  style={[styles.primaryBtn, { backgroundColor: c.accent, opacity: busy ? 0.5 : 1 }]}
                >
                  <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>Save changes</Text>
                </Pressable>
              </Section>

              <Section title={`Agents (${config.agents?.length || 0})`} color={c.textSecondary}>
                {(config.agents || []).map((agent) => (
                  <View
                    key={agent.name}
                    style={[styles.row, { borderColor: c.border }]}
                  >
                    <View style={{ flex: 1 }}>
                      <Text style={{ color: c.textPrimary, fontWeight: "600" }}>
                        {agent.name}{" "}
                        {agent.isBuiltin ? <Text style={{ fontSize: 10, color: c.textMuted }}>· builtin</Text> : <Text style={{ fontSize: 10, color: "#f59e0b" }}>· custom</Text>}
                      </Text>
                      {agent.model ? (
                        <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Menlo" }} numberOfLines={1}>{agent.model}</Text>
                      ) : (
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>(inherits default model)</Text>
                      )}
                    </View>
                  </View>
                ))}
              </Section>

              <Section title={`Providers (${config.providers?.length || 0})`} color={c.textSecondary}>
                {(config.providers || []).map((provider) => (
                  <Pressable
                    key={provider.id}
                    onPress={() => {
                      setEditingProvider(provider);
                      setEditBaseUrl(provider.baseUrl || "");
                      setEditApiKey("");
                    }}
                    style={[styles.row, { borderColor: c.border }]}
                  >
                    <View style={{ flex: 1 }}>
                      <Text style={{ color: c.textPrimary, fontWeight: "600" }}>
                        {provider.name || provider.id} <Text style={{ color: c.textMuted, fontSize: 11 }}>· {provider.id}</Text>
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Menlo" }} numberOfLines={1}>
                        {provider.baseUrl || "(no baseURL)"}
                      </Text>
                    </View>
                    <Pressable hitSlop={8} onPress={() => deleteProvider(provider)}>
                      <Text style={{ color: "#ef4444", fontSize: 16 }}>×</Text>
                    </Pressable>
                  </Pressable>
                ))}
                <Pressable
                  onPress={() => setShowAdd(true)}
                  style={[styles.primaryBtn, { backgroundColor: c.accent }]}
                >
                  <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>+ Add provider</Text>
                </Pressable>
              </Section>
            </>
          )}
        </ScrollView>

        {/* Edit-provider modal */}
        <Modal
          visible={!!editingProvider}
          animationType="slide"
          presentationStyle="formSheet"
          onRequestClose={() => setEditingProvider(null)}
        >
          <View style={[styles.container, { backgroundColor: c.bg }]}>
            <View style={[styles.header, { borderBottomColor: c.border }]}>
              <Pressable onPress={() => setEditingProvider(null)} hitSlop={12} style={styles.headerBtn}>
                <Text style={{ color: c.accent, fontSize: 16 }}>Cancel</Text>
              </Pressable>
              <Text style={[styles.title, { color: c.textPrimary }]}>{editingProvider?.id}</Text>
              <View style={styles.headerBtn} />
            </View>
            <ScrollView contentContainerStyle={{ padding: 16 }}>
              <Field label="Base URL" value={editBaseUrl} onChange={setEditBaseUrl} placeholder="https://… or http://127.0.0.1:11434/v1" c={c} />
              <Field label="API key" value={editApiKey} onChange={setEditApiKey} placeholder="(leave empty to keep existing)" c={c} secret />
              <Pressable
                onPress={saveProviderEdit}
                disabled={busy}
                style={[styles.primaryBtn, { backgroundColor: c.accent, opacity: busy ? 0.5 : 1 }]}
              >
                <Text style={{ color: "#fff", fontWeight: "700" }}>Save provider</Text>
              </Pressable>
            </ScrollView>
          </View>
        </Modal>

        {/* Add-provider modal with presets */}
        <Modal
          visible={showAdd}
          animationType="slide"
          presentationStyle="formSheet"
          onRequestClose={() => setShowAdd(false)}
        >
          <View style={[styles.container, { backgroundColor: c.bg }]}>
            <View style={[styles.header, { borderBottomColor: c.border }]}>
              <Pressable onPress={() => setShowAdd(false)} hitSlop={12} style={styles.headerBtn}>
                <Text style={{ color: c.accent, fontSize: 16 }}>Cancel</Text>
              </Pressable>
              <Text style={[styles.title, { color: c.textPrimary }]}>
                {startInAddProvider ? "Set up OpenCode" : "Add provider"}
              </Text>
              <View style={styles.headerBtn} />
            </View>
            <ScrollView contentContainerStyle={{ padding: 16 }}>
              <Text style={[styles.muted, { color: c.textMuted, marginBottom: 8 }]}>
                {startInAddProvider
                  ? "Pick where OpenCode should route requests on this machine."
                  : "Quick fill:"}
              </Text>
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginBottom: 12 }}>
                {PRESETS.map((p) => (
                  <Pressable
                    key={p.label}
                    onPress={() => {
                      setAddId(p.id);
                      setAddName(p.name);
                      setAddBaseUrl(p.baseUrl || "");
                      setAddModel(p.model || "");
                      setAddModels(p.models);
                      setPresetHint(p.hint);
                    }}
                    style={({ pressed }) => [
                      { paddingHorizontal: 10, paddingVertical: 6, borderRadius: 14, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCardElevated },
                      pressed && { opacity: 0.7 },
                    ]}
                  >
                    <Text style={{ color: c.textSecondary, fontSize: 11 }}>{p.label}</Text>
                  </Pressable>
                ))}
              </View>
              {presetHint ? <Text style={[styles.muted, { color: c.textMuted, fontSize: 11, marginBottom: 8 }]}>{presetHint}</Text> : null}
              <Field label="Provider id" value={addId} onChange={setAddId} placeholder="glm / groq / ollama-tailscale" c={c} />
              <Field label="Base URL" value={addBaseUrl} onChange={setAddBaseUrl} placeholder="https://… or http://127.0.0.1:11434/v1" c={c} />
              <Field label="Default model" value={addModel} onChange={setAddModel} placeholder="provider/model (optional)" c={c} />
              <Field label="API key" value={addApiKey} onChange={setAddApiKey} placeholder="(leave empty for local Ollama)" c={c} secret />
              <Pressable
                onPress={addProvider}
                disabled={busy || !addId.trim()}
                style={[styles.primaryBtn, { backgroundColor: c.accent, opacity: busy || !addId.trim() ? 0.5 : 1 }]}
              >
                <Text style={{ color: "#fff", fontWeight: "700" }}>Save provider</Text>
              </Pressable>
            </ScrollView>
          </View>
        </Modal>
      </View>
    </Modal>
  );
}

function Section({ title, color, children }: { title: string; color: string; children: React.ReactNode }) {
  return (
    <View style={{ marginTop: 24 }}>
      <Text style={{ color, fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 8, textTransform: "uppercase" }}>
        {title}
      </Text>
      {children}
    </View>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  c,
  secret,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  c: ReturnType<typeof useColors>;
  secret?: boolean;
}) {
  return (
    <View style={{ marginBottom: 10 }}>
      <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 4 }}>{label}</Text>
      <TextInput
        value={value}
        onChangeText={onChange}
        placeholder={placeholder}
        placeholderTextColor={c.textMuted}
        secureTextEntry={!!secret}
        autoCapitalize="none"
        autoCorrect={false}
        style={{
          color: c.textPrimary,
          fontSize: 13,
          fontFamily: "Menlo",
          paddingHorizontal: 10,
          paddingVertical: 8,
          borderRadius: 6,
          borderWidth: 1,
          borderColor: c.border,
          backgroundColor: c.bgCardElevated,
        }}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingTop: 16, paddingBottom: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  headerBtn: { minWidth: 56, alignItems: "center" },
  title: { fontSize: 16, fontWeight: "700", flex: 1, textAlign: "center" },
  muted: { fontSize: 12 },
  warnCard: { marginTop: 12, padding: 10, borderRadius: 6, borderWidth: 1 },
  row: { flexDirection: "row", alignItems: "center", paddingVertical: 12, paddingHorizontal: 12, borderRadius: 6, borderWidth: 1, marginBottom: 8 },
  primaryBtn: { paddingVertical: 12, paddingHorizontal: 16, borderRadius: 6, alignItems: "center", marginTop: 12 },
});
