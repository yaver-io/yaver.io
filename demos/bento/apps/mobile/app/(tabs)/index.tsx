import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Recipe, RecipeCard } from "../components/RecipeCard";

const CATEGORIES = ["All", "Quick", "Healthy", "Comfort", "Dessert"];

const RECIPES: Recipe[] = [
  { id: "1", title: "Teriyaki Bowl", image: "https://images.unsplash.com/photo-1547592180-85f173990554?w=400", cookTime: "25 min", rating: 4.7, category: "Comfort" },
  { id: "2", title: "Avocado Toast", image: "https://images.unsplash.com/photo-1541519227354-08fa5d50c44d?w=400", cookTime: "10 min", rating: 4.5, category: "Quick" },
  { id: "3", title: "Overnight Oats", image: "https://images.unsplash.com/photo-1517673400267-0251440c45dc?w=400", cookTime: "5 min", rating: 4.3, category: "Healthy" },
  { id: "4", title: "Chicken Stir-Fry", image: "https://images.unsplash.com/photo-1603133872878-684f208fb84b?w=400", cookTime: "20 min", rating: 4.6, category: "Quick" },
  { id: "5", title: "Pasta Carbonara", image: "https://images.unsplash.com/photo-1612874742237-6526221588e3?w=400", cookTime: "30 min", rating: 4.8, category: "Comfort" },
  { id: "6", title: "Mango Smoothie Bowl", image: "https://images.unsplash.com/photo-1511690743698-d9d85f2fbf38?w=400", cookTime: "8 min", rating: 4.4, category: "Healthy" },
  { id: "7", title: "Chocolate Lava Cake", image: "https://images.unsplash.com/photo-1624353365286-3f8d62daad51?w=400", cookTime: "22 min", rating: 4.9, category: "Dessert" },
  { id: "8", title: "Greek Salad", image: "https://images.unsplash.com/photo-1540420773420-3366772f4999?w=400", cookTime: "12 min", rating: 4.2, category: "Healthy" },
  { id: "9", title: "Miso Ramen", image: "https://images.unsplash.com/photo-1569718212165-3a8278d5f624?w=400", cookTime: "35 min", rating: 4.7, category: "Comfort" },
  { id: "10", title: "Berry Cheesecake", image: "https://images.unsplash.com/photo-1565958011703-44f9829ba187?w=400", cookTime: "40 min", rating: 4.8, category: "Dessert" },
];

export default function HomeScreen() {
  const [active, setActive] = useState("All");
  const recipes = active === "All" ? RECIPES : RECIPES.filter((r) => r.category === active);

  return (
    <SafeAreaView className="flex-1 bg-white" edges={["top"]}>
      <View className="flex-row items-center justify-between px-4 py-3">
        <Text className="text-2xl font-bold text-orange-500">Bento</Text>
        <View className="flex-row items-center gap-3">
          <Pressable onPress={() => router.push("/search")}>
            <Ionicons name="search" size={22} color="#374151" />
          </Pressable>
          <View className="w-9 h-9 rounded-full bg-gray-200 items-center justify-center">
            <Ionicons name="person" size={18} color="#6B7280" />
          </View>
        </View>
      </View>

      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={{ paddingHorizontal: 16, gap: 8 }}
        className="flex-grow-0 py-2"
      >
        {CATEGORIES.map((c) => {
          const selected = c === active;
          return (
            <Pressable
              key={c}
              onPress={() => setActive(c)}
              className={`px-4 py-2 rounded-full ${selected ? "" : "bg-gray-100"}`}
              style={selected ? { backgroundColor: "#F97316" } : undefined}
            >
              <Text className={selected ? "text-white font-medium" : "text-gray-700"}>{c}</Text>
            </Pressable>
          );
        })}
      </ScrollView>

      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <View className="flex-row flex-wrap justify-between">
          {recipes.map((r) => (
            <RecipeCard key={r.id} recipe={r} onPress={() => router.push(`/recipe/${r.id}`)} />
          ))}
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
