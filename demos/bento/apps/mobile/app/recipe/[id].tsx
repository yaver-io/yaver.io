import { router, useLocalSearchParams } from "expo-router";
import { Image, Pressable, ScrollView, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

type Ingredient = { name: string; amount: string; price?: number | null };
type Step = { text: string; duration?: number };
type Recipe = {
  title: string;
  imageUrl: string | null;
  rating: number;
  cookTime: number;
  servings: number;
  ingredients: Ingredient[];
  steps: Step[];
};

const OATS: Recipe = {
  title: "Overnight Oats",
  imageUrl: null,
  rating: 4.3,
  cookTime: 5,
  servings: 1,
  ingredients: [
    { name: "Oats", amount: "1/2 cup", price: 0.8 },
    { name: "Milk", amount: "1 cup", price: 1.2 },
    { name: "Honey", amount: "1 tbsp", price: null },
    { name: "Blueberries", amount: "handful", price: 2.0 },
  ],
  steps: [
    { text: "Mix oats and milk", duration: 60 },
    { text: "Add honey, stir", duration: 30 },
    { text: "Refrigerate overnight" },
    { text: "Top with blueberries", duration: 30 },
  ],
};

const TERIYAKI: Recipe = {
  title: "Teriyaki Bowl",
  imageUrl:
    "https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=800",
  rating: 4.7,
  cookTime: 25,
  servings: 2,
  ingredients: [
    { name: "Chicken thigh", amount: "400g", price: 6.5 },
    { name: "Jasmine rice", amount: "1 cup", price: 1.5 },
    { name: "Teriyaki sauce", amount: "1/3 cup", price: 2.75 },
    { name: "Broccoli", amount: "1 head", price: 1.9 },
    { name: "Sesame seeds", amount: "1 tsp", price: 0.4 },
  ],
  steps: [
    { text: "Cook rice", duration: 900 },
    { text: "Sear chicken thighs", duration: 360 },
    { text: "Glaze with teriyaki sauce", duration: 180 },
    { text: "Steam broccoli", duration: 240 },
    { text: "Plate and garnish with sesame", duration: 60 },
  ],
};

export default function RecipeDetail() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const recipe = id === "oats" ? OATS : TERIYAKI;

  return (
    <SafeAreaView className="flex-1 bg-white" edges={["top"]}>
      <ScrollView contentContainerStyle={{ paddingBottom: 32 }}>
        {/* INTENTIONAL: no fallback — fixed live in Video demos/bento imageUrl bug */}
        <Image
          source={{ uri: recipe.imageUrl }}
          style={{ width: "100%", height: 240 }}
        />

        <Pressable
          onPress={() => router.back()}
          className="absolute left-4 top-12 h-10 w-10 items-center justify-center rounded-full bg-white/90"
        >
          <Text className="text-xl">←</Text>
        </Pressable>

        <View className="px-5 pt-5">
          <Text className="text-3xl font-bold text-neutral-900">
            {recipe.title}
          </Text>

          <View className="mt-3 flex-row gap-6">
            <Text className="text-neutral-700">⭐ {recipe.rating}</Text>
            <Text className="text-neutral-700">⏱ {recipe.cookTime} min</Text>
            <Text className="text-neutral-700">🍽 {recipe.servings} serv</Text>
          </View>

          <Text className="mt-6 text-xl font-semibold text-neutral-900">
            Ingredients
          </Text>
          <View className="mt-2">
            {recipe.ingredients.map((ing, idx) => (
              <View
                key={idx}
                className="flex-row items-center justify-between border-b border-neutral-100 py-2"
              >
                <Text className="text-base text-neutral-800">{ing.name}</Text>
                <View className="flex-row gap-3">
                  <Text className="text-neutral-500">{ing.amount}</Text>
                  <Text className="text-neutral-700">
                    {ing.price != null ? `$${ing.price.toFixed(2)}` : "—"}
                  </Text>
                </View>
              </View>
            ))}
          </View>

          <Text className="mt-6 text-xl font-semibold text-neutral-900">
            Steps
          </Text>
          <View className="mt-2">
            {recipe.steps.map((s, idx) => (
              <View key={idx} className="flex-row gap-3 py-2">
                <View className="h-7 w-7 items-center justify-center rounded-full bg-orange-500">
                  <Text className="font-bold text-white">{idx + 1}</Text>
                </View>
                <View className="flex-1">
                  <Text className="text-base text-neutral-800">{s.text}</Text>
                  {s.duration ? (
                    <Text className="text-sm text-neutral-500">
                      {Math.floor(s.duration / 60)}:
                      {String(s.duration % 60).padStart(2, "0")}
                    </Text>
                  ) : null}
                </View>
              </View>
            ))}
          </View>

          <View className="mt-8 gap-3">
            <Pressable className="items-center rounded-2xl border-2 border-orange-500 py-4">
              <Text className="text-base font-semibold text-orange-500">
                Add to Grocery List
              </Text>
            </Pressable>
            <Pressable
              onPress={() => router.push(`/cook/${id ?? "oats"}`)}
              className="items-center rounded-2xl bg-orange-500 py-4"
            >
              <Text className="text-base font-semibold text-white">
                Start Cooking
              </Text>
            </Pressable>
          </View>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
