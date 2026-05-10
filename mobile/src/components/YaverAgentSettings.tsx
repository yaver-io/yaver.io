/**
 * YaverAgentSettings — provider/model/api-key for the mobile-embedded
 * yaver-agent (the small LLM that handles control-plane tasks like
 * "authenticate primary device", "what runners are authed on the mac
 * mini", "set primary to <alias>").
 *
 * This component reads/writes /yaver-agent/config on the connected
 * agent, which stores the values in vault under project "yaver-agent".
 *
 * Important: this is NOT for coding. Coding tasks run via your runner
 * (Claude Code / Codex / OpenCode) on a real machine. The yaver-agent
 * is only ever invoked for control-plane intents and never sees your
 * code or your Max/Pro OAuth tokens — those flow through native vault
 * P2P sync, not through this LLM.
 */
import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useColors } from "../context/ThemeContext";
import { useDevice } from "../context/DeviceContext";
import {
  quicClient,
  type YaverAgentConfig,
  type YaverAgentProviderDefault,
  type YaverAgentProviderId,
} from "../lib/quic";
import {
  loadYaverAgentLocalConfig,
  pingYaverAgent,
  runYaverAgent,
  saveYaverAgentLocalConfig,
  type YaverAgentRunResult,
} from "../lib/yaverAgentRunner";
import type { YaverAgentToolContext } from "../lib/yaverAgentTools";

interface Props {
  /** Whether the QUIC client is connected. The component still renders when
   * disconnected so users see the explanation, but save is disabled. */
  connected: boolean;
}

interface HelloResult {
  ok: boolean;
  text: string;
  toolCalls: number;
  steps: number;
  latencyMs: number;
  error?: string;
}

const PROVIDER_LABELS: Record<YaverAgentProviderId, string> = {
  glm: "GLM",
  anthropic: "Anthropic",
  openai: "OpenAI",
  openrouter: "OpenRouter",
};

// Providers where a base URL override is meaningful enough to surface.
// glm + anthropic have fixed canonical URLs; openai/openrouter benefit
// from a custom URL (proxies, self-hosted, region pinning).
const BASE_URL_PROVIDERS: YaverAgentProviderId[] = ["openai", "openrouter"];

export function YaverAgentSettings({ connected }: Props) {
  const c = useColors();
  const { devices, primaryDeviceId, secondaryDeviceId, selectDevice } = useDevice();

  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [hello, setHello] = useState<HelloResult | null>(null);

  const [provider, setProvider] = useState<YaverAgentProviderId>("glm");
  const [model, setModel] = useState<string>("");
  const [baseUrl, setBaseUrl] = useState<string>("");
  const [apiKey, setApiKey] = useState<string>("");
  const [hadKey, setHadKey] = useState<boolean>(false);

  const [defaults, setDefaults] = useState<YaverAgentProviderDefault[]>([]);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // Phone-side cache wins for "do we have a key" because the runner
      // also pulls from there. Host vault is the source of truth for
      // metadata when we can reach it.
      const local = await loadYaverAgentLocalConfig();
      if (local) {
        setProvider(local.provider);
        setModel(local.model);
        setBaseUrl(local.baseUrl || "");
        setHadKey(true);
      }
      if (connected) {
        const data = await quicClient.yaverAgentConfigGet();
        const cfg = data.config;
        if (cfg.provider && (cfg.provider as string) !== "") {
          setProvider(cfg.provider as YaverAgentProviderId);
        }
        if (cfg.model) setModel(cfg.model);
        if (cfg.baseUrl) setBaseUrl(cfg.baseUrl);
        // Hadkey is OR — phone-cached or host-vault-stored.
        setHadKey((prev) => prev || !!cfg.hasApiKey);
        setDefaults(data.defaults || []);
      }
    } catch (e: any) {
      setError(e?.message || "failed to load yaver-agent config");
    } finally {
      setLoading(false);
    }
  }, [connected]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const currentDefault = useMemo(
    () => defaults.find((d) => d.provider === provider),
    [defaults, provider],
  );

  const switchProvider = (next: YaverAgentProviderId) => {
    setProvider(next);
    // Reset model + baseUrl to that provider's default when the user
    // switches — saves them from copying GLM's model id into Anthropic.
    const def = defaults.find((d) => d.provider === next);
    setModel(def?.model || "");
    setBaseUrl(def?.baseUrl || "");
    setInfo(null);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    setInfo(null);
    try {
      const trimmedKey = apiKey.trim();
      const trimmedModel = model.trim();
      const trimmedBase = baseUrl.trim();

      // 1) Mirror to phone SecureStore so the runner can fire when no
      //    host device is connected. We do this before the host call
      //    because the host call may fail (e.g. offline) — the local
      //    save still succeeds, which is the point.
      const existingLocal = await loadYaverAgentLocalConfig();
      const localKey = trimmedKey !== "" ? trimmedKey : existingLocal?.apiKey ?? "";
      if (localKey !== "") {
        await saveYaverAgentLocalConfig({
          provider,
          model: trimmedModel || existingLocal?.model || provider,
          baseUrl: trimmedBase || existingLocal?.baseUrl || undefined,
          apiKey: localKey,
        });
        setHadKey(true);
      }

      // 2) Push to host vault when we're connected, so the same config
      //    is available to other surfaces (web, MCP) and survives
      //    re-installs.
      if (connected) {
        const req: Parameters<typeof quicClient.yaverAgentConfigSet>[0] = {
          provider,
          model: trimmedModel,
          baseUrl: trimmedBase,
        };
        if (trimmedKey !== "") req.apiKey = trimmedKey;
        const data = await quicClient.yaverAgentConfigSet(req);
        setHadKey((prev) => prev || !!data.config.hasApiKey);
      }

      setApiKey("");
      setInfo(connected ? "Saved (phone + host vault)." : "Saved on this phone.");
    } catch (e: any) {
      setError(e?.message || "failed to save yaver-agent config");
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    setError(null);
    setInfo(null);
    setHello(null);
    const started = Date.now();
    try {
      // Phase 1: cheap auth ping. Bails early if the key/model is wrong
      // so the user gets a clean error instead of a five-second tool-loop.
      const ping = await pingYaverAgent();
      if (!ping.ok) {
        setError(ping.error || "auth ping failed");
        return;
      }
      // Phase 2: run a real "say hello" through the full tool-use loop.
      // This proves provider + system prompt + tool registry are wired
      // end-to-end. The agent shouldn't need any tools to reply, but if
      // the model decides to call device.list out of curiosity, that
      // works too — useDevice() supplies the context.
      const ctx: YaverAgentToolContext = {
        devices: () => devices,
        primaryDeviceId: () => primaryDeviceId,
        secondaryDeviceId: () => secondaryDeviceId,
        selectDevice: async (deviceId) => {
          const d = devices.find((x) => x.id === deviceId);
          if (d) await selectDevice(d);
        },
      };
      const result: YaverAgentRunResult = await runYaverAgent({
        prompt: "Say hello in one short sentence.",
        ctx,
        maxSteps: 2,
      });
      setHello({
        ok: true,
        text: result.finalText.trim(),
        toolCalls: result.toolCalls.length,
        steps: result.steps,
        latencyMs: Date.now() - started,
      });
    } catch (e) {
      setHello({
        ok: false,
        text: "",
        toolCalls: 0,
        steps: 0,
        latencyMs: Date.now() - started,
        error: e instanceof Error ? e.message : String(e),
      });
    } finally {
      setTesting(false);
    }
  };

  const showBaseUrl = BASE_URL_PROVIDERS.includes(provider);
  const keyStatus = hadKey
    ? apiKey.trim() === ""
      ? "API key on file (leave blank to keep)"
      : "Will replace existing key"
    : apiKey.trim() === ""
    ? "No key on file yet"
    : "New key will be saved";

  return (
    <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <Text style={[styles.title, { color: c.textPrimary }]}>Yaver Agent</Text>
      <Text style={[styles.subtitle, { color: c.textMuted }]}>
        Tiny LLM that runs inside this app for control-plane tasks only —
        device auth, status checks, primary management. It never handles
        your code, and Claude Max tokens never enter it.
      </Text>

      {!connected && (
        <View style={[styles.banner, { backgroundColor: c.warnBg, borderColor: c.warnBorder }]}>
          <Text style={{ color: c.warn, fontSize: 12, lineHeight: 18 }}>
            Connect to a host device to save these settings. They live in
            that device's vault, not on the phone.
          </Text>
        </View>
      )}

      {loading ? (
        <View style={{ paddingVertical: 16, alignItems: "center" }}>
          <ActivityIndicator color={c.textMuted} />
        </View>
      ) : (
        <>
          <Text style={[styles.label, { color: c.textPrimary }]}>Provider</Text>
          <View style={styles.row}>
            {(Object.keys(PROVIDER_LABELS) as YaverAgentProviderId[]).map((id) => {
              const active = provider === id;
              return (
                <Pressable
                  key={id}
                  onPress={() => switchProvider(id)}
                  style={({ pressed }) => [
                    styles.chip,
                    {
                      backgroundColor: active ? c.accent : c.bg,
                      borderColor: active ? c.accent : c.border,
                    },
                    pressed && { opacity: 0.8 },
                  ]}
                >
                  <Text
                    style={{
                      color: active ? "#fff" : c.textPrimary,
                      fontWeight: "600",
                      fontSize: 12,
                    }}
                  >
                    {PROVIDER_LABELS[id]}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          {currentDefault?.note && (
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
              {currentDefault.note}
            </Text>
          )}

          <Text style={[styles.label, { color: c.textPrimary }]}>Model</Text>
          <TextInput
            style={[
              styles.input,
              {
                backgroundColor: c.bgInput,
                color: c.textPrimary,
                borderColor: c.border,
              },
            ]}
            placeholder={currentDefault?.model || "model id"}
            placeholderTextColor={c.textMuted}
            value={model}
            onChangeText={setModel}
            autoCapitalize="none"
            autoCorrect={false}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
            Leave blank to use{" "}
            <Text style={{ color: c.textPrimary }}>{currentDefault?.model || "default"}</Text>.
          </Text>

          {showBaseUrl && (
            <>
              <Text style={[styles.label, { color: c.textPrimary }]}>Base URL (optional)</Text>
              <TextInput
                style={[
                  styles.input,
                  {
                    backgroundColor: c.bgInput,
                    color: c.textPrimary,
                    borderColor: c.border,
                  },
                ]}
                placeholder={currentDefault?.baseUrl || "https://api.example.com/v1"}
                placeholderTextColor={c.textMuted}
                value={baseUrl}
                onChangeText={setBaseUrl}
                autoCapitalize="none"
                autoCorrect={false}
              />
            </>
          )}

          <Text style={[styles.label, { color: c.textPrimary }]}>API Key</Text>
          <TextInput
            style={[
              styles.input,
              {
                backgroundColor: c.bgInput,
                color: c.textPrimary,
                borderColor: c.border,
              },
            ]}
            placeholder={hadKey ? "•••••• (saved)" : "paste provider api key"}
            placeholderTextColor={c.textMuted}
            value={apiKey}
            onChangeText={setApiKey}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>{keyStatus}</Text>

          {error && (
            <Text style={{ color: c.warn, fontSize: 12, marginTop: 10 }}>{error}</Text>
          )}
          {info && (
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 10 }}>{info}</Text>
          )}
          {hello && hello.ok && (
            <View
              style={{
                marginTop: 12,
                padding: 12,
                borderWidth: 1,
                borderColor: c.border,
                borderRadius: 10,
                backgroundColor: c.bgInput,
              }}
            >
              <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600" }}>
                Reply ({hello.latencyMs} ms · {hello.steps} step
                {hello.steps !== 1 ? "s" : ""}
                {hello.toolCalls > 0 ? ` · ${hello.toolCalls} tool call${hello.toolCalls !== 1 ? "s" : ""}` : ""})
              </Text>
              <Text style={{ color: c.textPrimary, fontSize: 13, marginTop: 6, lineHeight: 18 }}>
                {hello.text || "(empty reply)"}
              </Text>
            </View>
          )}
          {hello && !hello.ok && (
            <Text style={{ color: c.warn, fontSize: 12, marginTop: 10 }}>
              Hello test failed: {hello.error}
            </Text>
          )}

          <View style={{ flexDirection: "row", gap: 8, marginTop: 16 }}>
            <Pressable
              onPress={save}
              disabled={saving}
              style={({ pressed }) => [
                styles.button,
                {
                  flex: 1,
                  marginTop: 0,
                  backgroundColor: c.accent,
                  opacity: saving ? 0.5 : pressed ? 0.85 : 1,
                },
              ]}
            >
              <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>
                {saving ? "Saving…" : "Save"}
              </Text>
            </Pressable>
            <Pressable
              onPress={test}
              disabled={testing || !hadKey}
              style={({ pressed }) => [
                styles.button,
                {
                  flex: 1,
                  marginTop: 0,
                  backgroundColor: "transparent",
                  borderWidth: 1,
                  borderColor: c.border,
                  opacity: testing || !hadKey ? 0.5 : pressed ? 0.85 : 1,
                },
              ]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>
                {testing ? "Testing…" : "Test agent"}
              </Text>
            </Pressable>
          </View>

          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10, lineHeight: 16 }}>
            For Anthropic this uses your API key (NOT Max plan) — small models
            like Haiku 4.5 cost cents per session. To use claude-code with
            Max, configure that runner separately above; the runner does
            real work on a host machine and your subscription token stays
            on that machine.
          </Text>
        </>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    borderWidth: 1,
    borderRadius: 12,
    padding: 16,
    marginTop: 8,
  },
  title: { fontWeight: "700", fontSize: 15 },
  subtitle: { fontSize: 12, marginTop: 4, lineHeight: 18 },
  banner: {
    marginTop: 12,
    borderRadius: 8,
    borderWidth: 1,
    padding: 10,
  },
  label: { fontWeight: "600", fontSize: 13, marginTop: 14 },
  row: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 8 },
  chip: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 999,
    borderWidth: 1,
  },
  input: {
    marginTop: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderWidth: 1,
    borderRadius: 8,
    fontSize: 13,
  },
  button: {
    marginTop: 16,
    paddingVertical: 12,
    borderRadius: 10,
    alignItems: "center",
  },
});
