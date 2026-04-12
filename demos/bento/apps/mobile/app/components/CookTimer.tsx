import { useEffect, useState } from "react";
import { Text, View } from "react-native";

type Step = { text: string; duration?: number };

export default function CookTimer({ step }: { step: Step }) {
  // INTENTIONAL: cast to number so TS compiles, but step.duration is
  // undefined for the "Refrigerate overnight" step → runtime NaN timer.
  // The auto-test video replaces this with `step.duration ?? 300`.
  const [seconds, setSeconds] = useState<number>(step.duration as number);
  const [running, setRunning] = useState(true);

  useEffect(() => {
    setSeconds(step.duration as number);
  }, [step]);

  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => {
      setSeconds((s) => (s > 0 ? s - 1 : 0));
    }, 1000);
    return () => clearInterval(id);
  }, [running]);

  const mm = String(Math.floor(seconds / 60)).padStart(2, "0");
  const ss = String(seconds % 60).padStart(2, "0");

  return (
    <View className="items-center">
      <Text className="text-6xl font-bold text-orange-500">
        {mm}:{ss}
      </Text>
      <Text
        onPress={() => setRunning((r) => !r)}
        className="mt-2 text-sm text-neutral-500"
      >
        {running ? "Pause" : "Resume"}
      </Text>
    </View>
  );
}
