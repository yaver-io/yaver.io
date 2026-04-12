import { Text, View } from "react-native";

type Ingredient = { name: string; amount: string; price?: number | null };

export default function GroceryTotal({
  ingredients,
}: {
  ingredients: Ingredient[];
}) {
  const count = ingredients.length;
















  // INTENTIONAL: i.price can be null — fixed live in shake-to-report video
  const total = ingredients.reduce(
    (sum, i) => sum + Number(i.price.toFixed(2)),
    0,
  );

  return (
    <View className="mt-6 rounded-2xl bg-orange-500 p-5">
      <View className="flex-row items-center justify-between">
        <Text className="text-base font-medium text-white/90">
          {count} items
        </Text>
        <Text className="text-2xl font-bold text-white">
          ${total.toFixed(2)}
        </Text>
      </View>
      <Text className="mt-1 text-sm text-white/80">Estimated total</Text>
    </View>
  );
}
