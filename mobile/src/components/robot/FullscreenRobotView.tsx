// FullscreenRobotView — an immersive, YouTube-style fullscreen for the robot
// camera with overlaid controls. Architecture:
//   • Full-bleed live frame (landscape-locked, `contain` so the work area is
//     never cropped); screen kept awake while open (you're watching a machine).
//   • Auto-hiding "chrome": a tiny state machine — visible → 4s idle → fade out;
//     tap the camera toggles it; ANY control press keeps it alive (resets timer).
//   • E-STOP is exempt — always visible + pressable even when chrome is hidden.
//     Safety controls never auto-hide.
//   • Controls call back into the parent's existing handlers (no duplicate robot
//     logic); the `frame`/`status` props update live from the parent's polling.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { Animated, Image, Modal, Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import * as ScreenOrientation from "expo-screen-orientation";
import { activateKeepAwakeAsync, deactivateKeepAwake } from "expo-keep-awake";
import type { RobotStatus, VerifyMode } from "../../lib/robotClient";

const OK = "#22c55e";
const DANGER = "#ef4444";
const WARN = "#f59e0b";
const KA_TAG = "robot-fullscreen";
const STEPS = [1, 10, 50];

export function FullscreenRobotView({
  visible,
  onClose,
  frame,
  status,
  busy,
  controlsDisabled,
  hasMotion,
  hasTool,
  hasRotate,
  step,
  setStep,
  verify,
  cycleVerify,
  onJog,
  onHome,
  onTool,
  onRotate,
  onEstop,
  onReset,
}: {
  visible: boolean;
  onClose: () => void;
  frame: string | null;
  status: RobotStatus | null;
  busy?: boolean;
  controlsDisabled?: boolean;
  hasMotion?: boolean;
  hasTool?: boolean;
  hasRotate?: boolean;
  step: number;
  setStep: (n: number) => void;
  verify: VerifyMode;
  cycleVerify: () => void;
  onJog: (axis: "X" | "Y" | "Z", dir: 1 | -1) => void;
  onHome: () => void;
  onTool: (on: boolean) => void;
  onRotate: (turns: number, ccw: boolean) => void;
  onEstop: () => void;
  onReset: () => void;
}) {
  const [chrome, setChrome] = useState(true);
  const fade = useRef(new Animated.Value(1)).current;
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // landscape-lock + keep-awake while open
  useEffect(() => {
    if (!visible) return;
    activateKeepAwakeAsync(KA_TAG).catch(() => {});
    ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.LANDSCAPE).catch(() => {});
    return () => {
      deactivateKeepAwake(KA_TAG);
      ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP).catch(() => {});
    };
  }, [visible]);

  const armHide = useCallback(() => {
    if (hideTimer.current) clearTimeout(hideTimer.current);
    hideTimer.current = setTimeout(() => {
      Animated.timing(fade, { toValue: 0, duration: 400, useNativeDriver: true }).start(() => setChrome(false));
    }, 4000);
  }, [fade]);

  const showChrome = useCallback(() => {
    setChrome(true);
    Animated.timing(fade, { toValue: 1, duration: 150, useNativeDriver: true }).start();
    armHide();
  }, [fade, armHide]);

  useEffect(() => {
    if (visible) showChrome();
    return () => {
      if (hideTimer.current) clearTimeout(hideTimer.current);
    };
  }, [visible, showChrome]);

  // keep chrome alive whenever a control is used
  const withChrome = (fn: () => void) => () => {
    showChrome();
    fn();
  };
  const toggleChrome = () => {
    if (chrome) {
      if (hideTimer.current) clearTimeout(hideTimer.current);
      Animated.timing(fade, { toValue: 0, duration: 250, useNativeDriver: true }).start(() => setChrome(false));
    } else showChrome();
  };

  const pos = status?.position;
  const toolOn = status?.tool === "on";
  const estopped = status?.estopped;

  return (
    <Modal visible={visible} animationType="fade" supportedOrientations={["landscape", "portrait"]} statusBarTranslucent onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: "#000" }}>
        {/* live frame — tap toggles chrome */}
        <Pressable onPress={toggleChrome} style={{ position: "absolute", inset: 0 as any, top: 0, bottom: 0, left: 0, right: 0 }}>
          {frame ? (
            <Image source={{ uri: frame }} style={{ width: "100%", height: "100%" }} resizeMode="contain" />
          ) : (
            <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
              <Ionicons name="videocam-outline" size={40} color="#666" />
              <Text style={{ color: "#888", marginTop: 8 }}>waiting for camera…</Text>
            </View>
          )}
        </Pressable>

        {/* auto-hiding chrome */}
        <Animated.View pointerEvents={chrome ? "box-none" : "none"} style={{ position: "absolute", top: 0, bottom: 0, left: 0, right: 0, opacity: fade }}>
          {/* top bar */}
          <View style={{ position: "absolute", top: 0, left: 0, right: 0, flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 18, paddingTop: 14, paddingBottom: 28, backgroundColor: "rgba(0,0,0,0.45)" }}>
            <Pressable onPress={withChrome(onClose)} hitSlop={14} style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Ionicons name="chevron-down" size={26} color="#fff" />
              <Text style={{ color: "#fff", fontWeight: "700" }}>Exit</Text>
            </Pressable>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              {frame && <View style={{ width: 7, height: 7, borderRadius: 4, backgroundColor: DANGER }} />}
              <Text style={{ color: "#fff", fontWeight: "700" }}>{status?.label || "Robot Cell"}</Text>
            </View>
            <View style={{ flexDirection: "row", gap: 14 }}>
              {hasMotion && <Chip label="Z" v={pos ? pos.z.toFixed(0) : "—"} />}
              {hasMotion && <Chip label="Homed" v={pos?.homed ? "yes" : "no"} good={pos?.homed} />}
              {status?.companion && <Chip label="N·mm" v={status?.targetTorqueNmm ? `${status.targetTorqueNmm}` : "—"} />}
              <Chip label="E-stop" v={estopped ? "TRIP" : "ok"} good={!estopped} />
            </View>
          </View>

          {/* left: jog d-pad (motion) */}
          {hasMotion && (
            <View style={{ position: "absolute", left: 20, top: 0, bottom: 0, justifyContent: "center" }}>
              <View style={{ alignItems: "center", gap: 6 }}>
                <Round icon="arrow-up" onPress={withChrome(() => onJog("Y", 1))} disabled={controlsDisabled} />
                <View style={{ flexDirection: "row", gap: 6 }}>
                  <Round icon="arrow-back" onPress={withChrome(() => onJog("X", -1))} disabled={controlsDisabled} />
                  <Round icon="home" onPress={withChrome(onHome)} disabled={controlsDisabled} accent />
                  <Round icon="arrow-forward" onPress={withChrome(() => onJog("X", 1))} disabled={controlsDisabled} />
                </View>
                <Round icon="arrow-down" onPress={withChrome(() => onJog("Y", -1))} disabled={controlsDisabled} />
                <View style={{ flexDirection: "row", gap: 6, marginTop: 4 }}>
                  <Round small icon="chevron-up" label="Z+" onPress={withChrome(() => onJog("Z", 1))} disabled={controlsDisabled} />
                  <Round small icon="chevron-down" label="Z-" onPress={withChrome(() => onJog("Z", -1))} disabled={controlsDisabled} />
                </View>
              </View>
            </View>
          )}

          {/* right: screwdriver */}
          {(hasTool || hasRotate) && (
            <View style={{ position: "absolute", right: 20, top: 0, bottom: 0, justifyContent: "center", gap: 10 }}>
              {hasTool && (
                <Pressable onPress={withChrome(() => onTool(!toolOn))} disabled={controlsDisabled} style={{ alignItems: "center", backgroundColor: toolOn ? OK + "33" : "rgba(0,0,0,0.5)", borderColor: toolOn ? OK : "#555", borderWidth: 1, borderRadius: 12, paddingHorizontal: 16, paddingVertical: 12, opacity: controlsDisabled ? 0.5 : 1 }}>
                  <Ionicons name="flash" size={20} color={toolOn ? OK : "#fff"} />
                  <Text style={{ color: toolOn ? OK : "#fff", fontWeight: "700", fontSize: 12 }}>{toolOn ? "ON" : "Tool"}</Text>
                </Pressable>
              )}
              {hasRotate && (
                <>
                  <Round icon="reload" onPress={withChrome(() => onRotate(1, false))} disabled={controlsDisabled} accent />
                  <Round icon="reload-outline" onPress={withChrome(() => onRotate(1, true))} disabled={controlsDisabled} />
                </>
              )}
            </View>
          )}

          {/* bottom: step + verify */}
          <View style={{ position: "absolute", bottom: 18, left: 0, right: 0, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8 }}>
            {hasMotion &&
              STEPS.map((s) => (
                <Pressable key={s} onPress={withChrome(() => setStep(s))} style={{ paddingHorizontal: 12, paddingVertical: 6, borderRadius: 10, backgroundColor: step === s ? "#fff" : "rgba(0,0,0,0.5)", borderColor: "#666", borderWidth: 1 }}>
                  <Text style={{ color: step === s ? "#000" : "#fff", fontWeight: "700" }}>{s}mm</Text>
                </Pressable>
              ))}
            <Pressable onPress={withChrome(cycleVerify)} style={{ paddingHorizontal: 12, paddingVertical: 6, borderRadius: 10, backgroundColor: "rgba(0,0,0,0.5)", borderColor: "#666", borderWidth: 1 }}>
              <Text style={{ color: "#fff", fontSize: 12 }}>verify: {verify}</Text>
            </Pressable>
          </View>
        </Animated.View>

        {/* E-STOP / reset — ALWAYS visible (exempt from auto-hide) */}
        <View style={{ position: "absolute", bottom: 18, right: 20, flexDirection: "row", gap: 8 }}>
          {estopped && (
            <Pressable onPress={onReset} style={{ borderColor: "#888", borderWidth: 1, borderRadius: 12, paddingHorizontal: 14, justifyContent: "center" }}>
              <Text style={{ color: "#fff", fontWeight: "700" }}>Reset</Text>
            </Pressable>
          )}
          <Pressable onPress={onEstop} style={{ backgroundColor: DANGER, borderRadius: 12, paddingHorizontal: 22, paddingVertical: 14, alignItems: "center" }}>
            <Text style={{ color: "#fff", fontWeight: "800", letterSpacing: 1 }}>E-STOP</Text>
          </Pressable>
        </View>

        {busy && (
          <View style={{ position: "absolute", top: 0, bottom: 0, left: 0, right: 0, alignItems: "center", justifyContent: "center" }} pointerEvents="none">
            <View style={{ backgroundColor: "rgba(0,0,0,0.55)", borderRadius: 20, paddingHorizontal: 16, paddingVertical: 8 }}>
              <Text style={{ color: WARN, fontWeight: "700" }}>working…</Text>
            </View>
          </View>
        )}
      </View>
    </Modal>
  );
}

function Chip({ label, v, good }: { label: string; v: string; good?: boolean }) {
  return (
    <View style={{ alignItems: "center" }}>
      <Text style={{ color: "#aaa", fontSize: 9 }}>{label}</Text>
      <Text style={{ color: good === undefined ? "#fff" : good ? OK : DANGER, fontSize: 13, fontWeight: "700" }}>{v}</Text>
    </View>
  );
}

function Round({ icon, label, onPress, disabled, accent, small }: { icon: any; label?: string; onPress: () => void; disabled?: boolean; accent?: boolean; small?: boolean }) {
  const d = small ? 44 : 52;
  return (
    <Pressable onPress={onPress} disabled={disabled} style={{ width: label ? d + 14 : d, height: d, borderRadius: d / 2, alignItems: "center", justifyContent: "center", flexDirection: "row", gap: 2, backgroundColor: accent ? "#3b82f6cc" : "rgba(0,0,0,0.5)", borderColor: accent ? "#3b82f6" : "#666", borderWidth: 1, opacity: disabled ? 0.45 : 1 }}>
      <Ionicons name={icon} size={small ? 16 : 20} color="#fff" />
      {label ? <Text style={{ color: "#fff", fontWeight: "700", fontSize: 12 }}>{label}</Text> : null}
    </Pressable>
  );
}
