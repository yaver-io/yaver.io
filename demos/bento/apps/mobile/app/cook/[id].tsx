import { router, useLocalSearchParams } from "expo-router";
import { useState } from "react";
import { Pressable, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import CookTimer from "../components/CookTimer";

type Step = { text: string; duration?: number };

const OATS_STEPS: Step[] = [
  { text: "Mix oats and milk", duration: 60 },
  { text: "Add honey, stir", duration: 30 },
  { text: "Refrigerate overnight" },
  { text: "Top with blueberries", duration: 30 },
];

export default function CookMode() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const title = id === "oats" ? "Overnight Oats" : "Recipe";
  const steps = OATS_STEPS;
  const [idx, setIdx] = useState(0);
  const step = steps[idx];
  const progress = ((idx + 1) / steps.length) * 100;

  return (
    <SafeAreaView className="flex-1 bg-white" edges={["top"]}>
      <View className="flex-row items-center gap-3 px-5 pb-3">
        <Pressable onPress={() => router.back()} className="h-10 w-10 items-center justify-center">
          <Text className="text-2xl">←</Text>
        </Pressable>
        <Text className="text-xl font-semibold text-neutral-900">{title}</Text>
      </View>

      <View className="h-1.5 bg-neutral-100">
        <View
          className="h-1.5 bg-orange-500"
          style={{ width: `${progress}%` }}
        />
      </View>

      <View className="flex-1 items-center justify-center px-6">
        <View className="w-full rounded-3xl bg-orange-50 p-8">
          <Text className="text-sm font-medium uppercase tracking-wider text-orange-500">
            Step {idx + 1} of {steps.length}
          </Text>
          <Text className="mt-3 text-2xl font-semibold text-neutral-900">
            {step.text}
          </Text>
          <View className="mt-8">
            <CookTimer step={step} />
          </View>
        </View>
      </View>

      <View className="flex-row gap-3 px-5 pb-6">
        <Pressable
          disabled={idx === 0}
          onPress={() => setIdx((i) => Math.max(0, i - 1))}
          className="flex-1 items-center rounded-2xl border-2 border-orange-500 py-4"
        >
          <Text className="font-semibold text-orange-500">Previous</Text>
        </Pressable>
        <Pressable
          disabled={idx === steps.length - 1}
          onPress={() => setIdx((i) => Math.min(steps.length - 1, i + 1))}
          className="flex-1 items-center rounded-2xl bg-orange-500 py-4"
        >
          <Text className="font-semibold text-white">Next</Text>
        </Pressable>
      </View>
    </SafeAreaView>
  );
}
