// MeshEnableProgress.tsx — the prominent "what's happening" overlay shown while
// a box (or the whole fleet) is being brought onto the mesh. Enabling runs two
// slow remote calls back-to-back (stage agent self-update ~30s, then /mesh/up
// ~25s); a small inline row label wasn't enough signal, so this is a real
// animated stepper anchored over the screen:
//
//   ① Updating agent      ⟳ active · ✓ done
//   ② Bringing mesh up    ◦ pending · ⟳ active
//
// Driven entirely by the current MeshEnablePhase — no extra state to keep in
// sync. The active step spins, completed steps flip to a green check, and a top
// progress bar slides updating → bringing-up.

import React, { useEffect, useRef, useState } from "react";
import { ActivityIndicator, Animated, Easing, Text, View } from "react-native";
import { useColors } from "../../context/ThemeContext";
import { type MeshEnablePhase } from "../../lib/meshControl";
import { CheckIcon } from "./MeshIcons";

const GREEN = "#34d399";

export type MeshEnableProgressInfo = {
  /** Headline — "Enabling magara" or "Enabling mesh on 5 machines". */
  title: string;
  /** Secondary line — for the fleet run: "Machine 2 of 5 · magara". */
  subtitle?: string;
  phase?: MeshEnablePhase;
};

const STEPS: { key: MeshEnablePhase; label: string; hint: string }[] = [
  { key: "updating", label: "Updating agent", hint: "Staging the latest signed binary" },
  { key: "bringing-up", label: "Bringing mesh up", hint: "Keypair · control plane · data plane" },
];

type StepState = "pending" | "active" | "done";

function stepState(step: MeshEnablePhase, phase?: MeshEnablePhase): StepState {
  const order: MeshEnablePhase[] = ["updating", "bringing-up"];
  const at = phase ? order.indexOf(phase) : -1;
  const me = order.indexOf(step);
  if (at < 0) return "pending";
  if (me < at) return "done";
  if (me === at) return "active";
  return "pending";
}

export function MeshEnableProgress({ info }: { info: MeshEnableProgressInfo | null }) {
  const c = useColors();
  // Keep rendering the last info through the exit animation so the card doesn't
  // pop out the instant the enable resolves.
  const [shown, setShown] = useState<MeshEnableProgressInfo | null>(null);
  const fade = useRef(new Animated.Value(0)).current;
  const rise = useRef(new Animated.Value(24)).current;
  const bar = useRef(new Animated.Value(0)).current;

  useEffect(() => {
    if (info) {
      setShown(info);
      Animated.parallel([
        Animated.timing(fade, { toValue: 1, duration: 200, easing: Easing.out(Easing.cubic), useNativeDriver: true }),
        Animated.timing(rise, { toValue: 0, duration: 240, easing: Easing.out(Easing.cubic), useNativeDriver: true }),
      ]).start();
    } else if (shown) {
      Animated.parallel([
        Animated.timing(fade, { toValue: 0, duration: 160, useNativeDriver: true }),
        Animated.timing(rise, { toValue: 24, duration: 160, useNativeDriver: true }),
      ]).start(() => {
        setShown(null);
        bar.setValue(0);
      });
    }
  }, [info]);

  // Slide the progress bar to the active step's fraction.
  useEffect(() => {
    const phase = info?.phase;
    const target = phase === "bringing-up" ? 0.92 : phase === "updating" ? 0.46 : 0.08;
    Animated.timing(bar, { toValue: target, duration: 400, easing: Easing.out(Easing.cubic), useNativeDriver: false }).start();
  }, [info?.phase]);

  if (!shown) return null;
  const phase = info?.phase ?? shown.phase;

  return (
    <Animated.View
      style={{
        position: "absolute",
        left: 0,
        right: 0,
        top: 0,
        bottom: 0,
        alignItems: "center",
        justifyContent: "flex-end",
        paddingHorizontal: 16,
        paddingBottom: 28,
        backgroundColor: "rgba(0,0,0,0.45)",
        opacity: fade,
      }}
    >
      <Animated.View
        style={{
          width: "100%",
          maxWidth: 440,
          borderRadius: 18,
          borderWidth: 1,
          borderColor: c.border,
          backgroundColor: c.bgCard,
          overflow: "hidden",
          transform: [{ translateY: rise }],
        }}
      >
        {/* Top progress bar. */}
        <View style={{ height: 3, backgroundColor: c.border }}>
          <Animated.View
            style={{
              height: 3,
              backgroundColor: GREEN,
              width: bar.interpolate({ inputRange: [0, 1], outputRange: ["0%", "100%"] }),
            }}
          />
        </View>

        <View style={{ padding: 18, gap: 14 }}>
          <View style={{ gap: 3 }}>
            <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>{shown.title}</Text>
            {shown.subtitle ? (
              <Text style={{ color: c.textMuted, fontSize: 13 }} numberOfLines={1}>
                {shown.subtitle}
              </Text>
            ) : null}
          </View>

          <View style={{ gap: 12 }}>
            {STEPS.map((s, i) => (
              <StepRow key={s.key} index={i + 1} label={s.label} hint={s.hint} state={stepState(s.key, phase)} />
            ))}
          </View>
        </View>
      </Animated.View>
    </Animated.View>
  );
}

function StepRow({
  index,
  label,
  hint,
  state,
}: {
  index: number;
  label: string;
  hint: string;
  state: StepState;
}) {
  const c = useColors();
  const active = state === "active";
  const done = state === "done";
  // Gentle pulse on the active step so it reads as "working" beyond the spinner.
  const pulse = useRef(new Animated.Value(0.6)).current;
  useEffect(() => {
    if (!active) {
      pulse.setValue(done ? 1 : 0.5);
      return;
    }
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(pulse, { toValue: 1, duration: 650, easing: Easing.inOut(Easing.quad), useNativeDriver: true }),
        Animated.timing(pulse, { toValue: 0.6, duration: 650, easing: Easing.inOut(Easing.quad), useNativeDriver: true }),
      ])
    );
    loop.start();
    return () => loop.stop();
  }, [active, done]);

  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
      {/* Status indicator. */}
      <View style={{ width: 26, height: 26, alignItems: "center", justifyContent: "center" }}>
        {done ? (
          <View
            style={{
              width: 24,
              height: 24,
              borderRadius: 12,
              backgroundColor: `${GREEN}22`,
              borderWidth: 1,
              borderColor: `${GREEN}66`,
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <CheckIcon size={14} color={GREEN} />
          </View>
        ) : active ? (
          <ActivityIndicator size="small" color={GREEN} />
        ) : (
          <View
            style={{
              width: 22,
              height: 22,
              borderRadius: 11,
              borderWidth: 1.5,
              borderColor: c.border,
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700" }}>{index}</Text>
          </View>
        )}
      </View>

      <Animated.View style={{ flex: 1, opacity: active ? pulse : done ? 1 : 0.55 }}>
        <Text
          style={{
            color: active || done ? c.textPrimary : c.textMuted,
            fontSize: 14,
            fontWeight: active || done ? "700" : "600",
          }}
        >
          {label}
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 1 }} numberOfLines={1}>
          {done ? "Done" : active ? hint : "Waiting…"}
        </Text>
      </Animated.View>
    </View>
  );
}
