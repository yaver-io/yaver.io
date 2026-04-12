import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import GroceryTotal from "../components/GroceryTotal";

type Ingredient = { name: string; amount: string; price?: number | null };

const GROCERIES: { recipe: string; items: Ingredient[] }[] = [
  {
    recipe: "Overnight Oats",
    items: [
      { name: "Oats", amount: "1/2 cup", price: 0.8 },
      { name: "Milk", amount: "1 cup", price: 1.2 },
      { name: "Honey", amount: "1 tbsp", price: null },
      { name: "Blueberries", amount: "handful", price: 2.0 },
    ],
  },
  {
    recipe: "Teriyaki Bowl",
    items: [
      { name: "Chicken thigh", amount: "400g", price: 6.5 },
      { name: "Olive oil", amount: "2 tbsp", price: null },
      { name: "Jasmine rice", amount: "1 cup", price: 1.5 },
      { name: "Chili flakes", amount: "1 tsp", price: null },
      { name: "Broccoli", amount: "1 head", price: 1.9 },
    ],
  },
];

export default function GroceryScreen() {
  const [checked, setChecked] = useState<Record<string, boolean>>({});
  const toggle = (k: string) =>
    setChecked((p) => ({ ...p, [k]: !p[k] }));

  const all = GROCERIES.flatMap((g) => g.items);

  return (
    <SafeAreaView className="flex-1 bg-white" edges={["top"]}>
      <ScrollView contentContainerStyle={{ padding: 20, paddingBottom: 48 }}>
        <Text className="text-3xl font-bold text-neutral-900">Grocery</Text>
        <Text className="mt-1 text-neutral-500">
          Everything you need for this week
        </Text>

        {GROCERIES.map((group) => (
          <View key={group.recipe} className="mt-6">
            <Text className="text-lg font-semibold text-orange-500">
              {group.recipe}
            </Text>
            <View className="mt-2 rounded-2xl bg-neutral-50 p-1">
              {group.items.map((item, idx) => {
                const key = `${group.recipe}-${idx}`;
                const isChecked = !!checked[key];
                return (
                  <Pressable
                    key={key}
                    onPress={() => toggle(key)}
                    className="flex-row items-center gap-3 px-3 py-3"
                  >
                    <View
                      className={`h-6 w-6 items-center justify-center rounded-md border-2 ${
                        isChecked
                          ? "border-orange-500 bg-orange-500"
                          : "border-neutral-300"
                      }`}
                    >
                      {isChecked ? (
                        <Text className="text-xs text-white">✓</Text>
                      ) : null}
                    </View>
                    <Text
                      className={`flex-1 text-base ${
                        isChecked
                          ? "text-neutral-400 line-through"
                          : "text-neutral-800"
                      }`}
                    >
                      {item.name}
                    </Text>
                    <Text className="text-sm text-neutral-500">
                      {item.amount}
                    </Text>
                    <Text className="w-14 text-right text-sm text-neutral-700">
                      {item.price != null ? `$${item.price.toFixed(2)}` : "—"}
                    </Text>
                  </Pressable>
                );
              })}
            </View>
          </View>
        ))}

        <GroceryTotal ingredients={all} />
      </ScrollView>
    </SafeAreaView>
  );
}
