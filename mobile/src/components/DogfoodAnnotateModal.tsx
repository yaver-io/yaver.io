/**
 * DogfoodAnnotateModal — Instagram-style markup over a caught screenshot.
 *
 * Pen-draw (multi-color) + undo on top of the image, a caption with hold-to-talk
 * voice dictation, a PR/Vibe mode switch, and Send-now / Add-to-batch actions.
 * The marked-up image is flattened with react-native-view-shot and returned as
 * base64 so the caller can persist it + attach it to the coding task.
 */

import React from "react";
import {
  ActivityIndicator,
  Dimensions,
  Image,
  Modal,
  PanResponder,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import Svg, { Path } from "react-native-svg";
import ViewShot, { captureRef } from "react-native-view-shot";
import { useColors } from "../context/ThemeContext";
import type { DogfoodMode } from "../lib/dogfoodConfig";
import { startRealtimeTranscribe } from "../lib/speech";

interface Stroke {
  color: string;
  d: string;
}

const PEN_COLORS = ["#ef4444", "#a855f7", "#22c55e", "#f59e0b", "#ffffff"];

export interface DogfoodAnnotateResult {
  base64: string;
  caption: string;
  mode: DogfoodMode;
  send: boolean; // true = send now, false = add to batch
}

export function DogfoodAnnotateModal({
  visible,
  imagePath,
  route,
  breadcrumbs,
  defaultMode,
  vibeAvailable,
  onCancel,
  onConfirm,
}: {
  visible: boolean;
  imagePath: string | null;
  route?: string;
  breadcrumbs?: string;
  defaultMode: DogfoodMode;
  vibeAvailable: boolean;
  onCancel: () => void;
  onConfirm: (result: DogfoodAnnotateResult) => void;
}) {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const shotRef = React.useRef<ViewShot>(null);
  const screen = Dimensions.get("window");

  const [strokes, setStrokes] = React.useState<Stroke[]>([]);
  const [current, setCurrent] = React.useState<Stroke | null>(null);
  const [penColor, setPenColor] = React.useState(PEN_COLORS[0]);
  const [caption, setCaption] = React.useState("");
  const [mode, setMode] = React.useState<DogfoodMode>(defaultMode);
  const [imgSize, setImgSize] = React.useState<{ w: number; h: number } | null>(null);
  const [recording, setRecording] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const sttRef = React.useRef<{ stop: () => Promise<string> } | null>(null);
  const penColorRef = React.useRef(penColor);
  penColorRef.current = penColor;

  // Reset per-open.
  React.useEffect(() => {
    if (visible) {
      setStrokes([]);
      setCurrent(null);
      setCaption("");
      setMode(defaultMode);
      setImgSize(null);
    }
  }, [visible, imagePath, defaultMode]);

  React.useEffect(() => {
    if (!visible || !imagePath) return;
    Image.getSize(
      imagePath.startsWith("file") ? imagePath : `file://${imagePath}`,
      (w, h) => setImgSize({ w, h }),
      () => setImgSize({ w: 1, h: 1.6 }),
    );
  }, [visible, imagePath]);

  const canvasW = Math.min(screen.width - 24, 520);
  const aspect = imgSize ? imgSize.w / imgSize.h : 0.62;
  const maxCanvasH = screen.height * 0.5;
  let canvasH = canvasW / (aspect || 0.62);
  let drawW = canvasW;
  if (canvasH > maxCanvasH) {
    canvasH = maxCanvasH;
    drawW = canvasH * (aspect || 0.62);
  }

  const panResponder = React.useMemo(
    () =>
      PanResponder.create({
        onStartShouldSetPanResponder: () => true,
        onMoveShouldSetPanResponder: () => true,
        onPanResponderGrant: (evt) => {
          const { locationX, locationY } = evt.nativeEvent;
          setCurrent({ color: penColorRef.current, d: `M ${locationX.toFixed(1)} ${locationY.toFixed(1)}` });
        },
        onPanResponderMove: (evt) => {
          const { locationX, locationY } = evt.nativeEvent;
          setCurrent((prev) =>
            prev ? { ...prev, d: `${prev.d} L ${locationX.toFixed(1)} ${locationY.toFixed(1)}` } : prev,
          );
        },
        onPanResponderRelease: () => {
          setCurrent((prev) => {
            if (prev && prev.d.includes("L")) {
              setStrokes((s) => [...s, prev]);
            }
            return null;
          });
        },
      }),
    [],
  );

  const undo = () => setStrokes((s) => s.slice(0, -1));

  const toggleRecord = async () => {
    if (recording) {
      try {
        const final = await sttRef.current?.stop();
        if (typeof final === "string" && final.trim()) {
          setCaption((prev) => (prev.trim() ? `${prev.trim()} ${final.trim()}` : final.trim()));
        }
      } catch {
        // ignore
      }
      sttRef.current = null;
      setRecording(false);
      return;
    }
    try {
      setRecording(true);
      sttRef.current = await startRealtimeTranscribe((partial) => {
        if (partial && partial.trim()) setCaption(partial.trim());
      });
    } catch {
      setRecording(false);
      sttRef.current = null;
    }
  };

  const finish = async (send: boolean) => {
    if (!imagePath || busy) return;
    setBusy(true);
    try {
      if (recording) {
        try {
          await sttRef.current?.stop();
        } catch {
          // ignore
        }
        sttRef.current = null;
        setRecording(false);
      }
      let base64 = "";
      try {
        base64 = await captureRef(shotRef, { format: "jpg", quality: 0.85, result: "base64" });
      } catch {
        base64 = "";
      }
      onConfirm({ base64, caption, mode, send });
    } finally {
      setBusy(false);
    }
  };

  const uri = imagePath
    ? imagePath.startsWith("file")
      ? imagePath
      : `file://${imagePath}`
    : undefined;

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onCancel}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        {/* Header */}
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
            paddingHorizontal: 16,
            paddingTop: Math.max(insets.top, 12) + 6,
            paddingBottom: 10,
            borderBottomWidth: 1,
            borderBottomColor: c.border,
          }}
        >
          <Pressable onPress={onCancel} hitSlop={8} style={{ paddingVertical: 8 }}>
            <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"‹"} Cancel</Text>
          </Pressable>
          <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700" }}>Annotate</Text>
          <Pressable onPress={undo} hitSlop={8} disabled={!strokes.length} style={{ paddingVertical: 8 }}>
            <Text style={{ color: strokes.length ? c.accent : c.textMuted, fontSize: 14, fontWeight: "600" }}>
              Undo
            </Text>
          </Pressable>
        </View>

        <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 28, alignItems: "center" }}>
          {/* Canvas */}
          <View
            style={{
              width: drawW,
              height: canvasH,
              borderRadius: 12,
              overflow: "hidden",
              borderWidth: 1,
              borderColor: c.border,
              backgroundColor: c.bgCard,
            }}
            {...panResponder.panHandlers}
          >
            <ViewShot ref={shotRef} style={{ width: drawW, height: canvasH }}>
              {uri ? (
                <Image source={{ uri }} style={{ width: drawW, height: canvasH }} resizeMode="cover" />
              ) : (
                <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
                  <ActivityIndicator color={c.accent} />
                </View>
              )}
              <Svg style={StyleSheet.absoluteFill} pointerEvents="none">
                {strokes.map((s, i) => (
                  <Path key={i} d={s.d} stroke={s.color} strokeWidth={3} fill="none" strokeLinecap="round" strokeLinejoin="round" />
                ))}
                {current ? (
                  <Path d={current.d} stroke={current.color} strokeWidth={3} fill="none" strokeLinecap="round" strokeLinejoin="round" />
                ) : null}
              </Svg>
            </ViewShot>
          </View>

          {/* Pen colors */}
          <View style={{ flexDirection: "row", gap: 12, marginTop: 14 }}>
            {PEN_COLORS.map((col) => (
              <Pressable
                key={col}
                onPress={() => setPenColor(col)}
                style={{
                  width: 30,
                  height: 30,
                  borderRadius: 15,
                  backgroundColor: col,
                  borderWidth: penColor === col ? 3 : 1,
                  borderColor: penColor === col ? c.accent : c.border,
                }}
              />
            ))}
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
            Draw on the screenshot to point at what should change
          </Text>

          {/* Caption + voice */}
          <View style={{ alignSelf: "stretch", marginTop: 16 }}>
            <View
              style={{
                flexDirection: "row",
                alignItems: "flex-end",
                gap: 8,
                backgroundColor: c.bgInput,
                borderWidth: 1,
                borderColor: c.border,
                borderRadius: 12,
                paddingHorizontal: 12,
                paddingVertical: 8,
              }}
            >
              <TextInput
                value={caption}
                onChangeText={setCaption}
                placeholder="What should change? (e.g. make this tab bar taller)"
                placeholderTextColor={c.textMuted}
                multiline
                style={{ flex: 1, color: c.textPrimary, fontSize: 14, maxHeight: 110, paddingTop: 2 }}
              />
              <Pressable
                onPress={toggleRecord}
                hitSlop={8}
                style={{
                  width: 36,
                  height: 36,
                  borderRadius: 18,
                  alignItems: "center",
                  justifyContent: "center",
                  backgroundColor: recording ? c.error + "22" : c.bgCard,
                  borderWidth: 1,
                  borderColor: recording ? c.error : c.border,
                }}
              >
                <Text style={{ fontSize: 16 }}>{recording ? "■" : "🎤"}</Text>
              </Pressable>
            </View>
            {route ? (
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }} numberOfLines={1}>
                screen: {route}
                {breadcrumbs ? ` · ${breadcrumbs}` : ""}
              </Text>
            ) : null}
          </View>

          {/* Mode switch */}
          <View style={{ alignSelf: "stretch", marginTop: 16 }}>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 8, fontWeight: "600" }}>
              How to apply
            </Text>
            <View style={{ flexDirection: "row", gap: 10 }}>
              <ModeChip
                label="Vibe"
                sub={vibeAvailable ? "edit + reload" : "no box w/ source"}
                active={mode === "vibe"}
                onPress={() => setMode("vibe")}
                c={c}
              />
              <ModeChip
                label="PR"
                sub="open GitHub PR"
                active={mode === "pr"}
                onPress={() => setMode("pr")}
                c={c}
              />
            </View>
          </View>

          {/* Actions */}
          <View style={{ alignSelf: "stretch", flexDirection: "row", gap: 12, marginTop: 20 }}>
            <Pressable
              onPress={() => finish(false)}
              disabled={busy}
              style={({ pressed }) => ({
                flex: 1,
                borderWidth: 1,
                borderColor: c.border,
                paddingVertical: 14,
                borderRadius: 12,
                alignItems: "center",
                opacity: pressed || busy ? 0.7 : 1,
              })}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Add to batch</Text>
            </Pressable>
            <Pressable
              onPress={() => finish(true)}
              disabled={busy}
              style={({ pressed }) => ({
                flex: 1.4,
                backgroundColor: c.accent,
                paddingVertical: 14,
                borderRadius: 12,
                alignItems: "center",
                opacity: pressed || busy ? 0.85 : 1,
              })}
            >
              {busy ? (
                <ActivityIndicator color="#000" />
              ) : (
                <Text style={{ color: "#000", fontWeight: "700" }}>Send to agent</Text>
              )}
            </Pressable>
          </View>
        </ScrollView>
      </View>
    </Modal>
  );
}

function ModeChip({
  label,
  sub,
  active,
  onPress,
  c,
}: {
  label: string;
  sub: string;
  active: boolean;
  onPress: () => void;
  c: ReturnType<typeof useColors>;
}) {
  return (
    <Pressable
      onPress={onPress}
      style={{
        flex: 1,
        paddingVertical: 10,
        paddingHorizontal: 12,
        borderRadius: 12,
        borderWidth: active ? 1.5 : 1,
        borderColor: active ? c.accent : c.border,
        backgroundColor: active ? c.accent + "16" : c.bgCard,
      }}
    >
      <Text style={{ color: active ? c.accent : c.textPrimary, fontWeight: "700", fontSize: 14 }}>{label}</Text>
      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{sub}</Text>
    </Pressable>
  );
}
