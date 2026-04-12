import { Image, Pressable, Text, View } from "react-native";

export type Recipe = {
  id: string;
  title: string;
  image: string;
  cookTime: string;
  rating: number;
  category: string;
};

export function RecipeCard({ recipe, onPress }: { recipe: Recipe; onPress: () => void }) {
  return (
    <Pressable onPress={onPress} className="w-[48%] mb-4">
      <Image
        source={{ uri: recipe.image }}
        className="w-full aspect-square rounded-xl bg-gray-200"
      />
      <Text className="font-medium mt-2" numberOfLines={1}>
        {recipe.title}
      </Text>
      <Text className="text-xs text-gray-500 mt-1">
        {recipe.cookTime} · ★ {recipe.rating.toFixed(1)}
      </Text>
    </Pressable>
  );
}
