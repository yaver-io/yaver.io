import { StatusBar } from "expo-status-bar";
import { Text, View } from "react-native";

export default function App() {
  return (
    <View style={{ flex: 1, backgroundColor: "#F97316", alignItems: "center", justifyContent: "center" }}>
      <Text style={{ color: "white", fontSize: 28, fontWeight: "700" }}>Bento</Text>
      <Text style={{ color: "white", opacity: 0.8, marginTop: 8 }}>Meal prep that ships itself</Text>
      <StatusBar style="light" />
    </View>
  );
}
