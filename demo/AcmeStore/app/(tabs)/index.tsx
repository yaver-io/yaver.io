import React from 'react';
import { FlatList, SafeAreaView, StyleSheet, Text, TouchableOpacity, View } from 'react-native';
import { PRODUCTS } from '../../src/lib/products';
import { ProductCard } from '../../src/components/ProductCard';

export default function HomeScreen() {
  return (
    <SafeAreaView style={s.container}>
      <View style={s.header}>
        <Text style={s.title}>Acme Store</Text>
        <View style={s.avatar} />
      </View>

      <FlatList
        data={PRODUCTS}
        numColumns={2}
        keyExtractor={(item) => item.id}
        renderItem={({ item }) => (
          <ProductCard product={item} onPress={() => {}} />
        )}
        contentContainerStyle={s.grid}
      />
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#fff' },
  header: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', paddingHorizontal: 20, paddingTop: 16, paddingBottom: 12 },
  title: { fontSize: 22, fontWeight: '700', color: '#111' },
  avatar: { width: 36, height: 36, borderRadius: 18, backgroundColor: '#6366f1' },
  grid: { paddingHorizontal: 12, paddingBottom: 80 },
});
