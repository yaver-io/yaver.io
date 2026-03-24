import React from 'react';
import { SafeAreaView, ScrollView, StyleSheet, Text, TouchableOpacity, View } from 'react-native';
import { PRODUCTS } from '../lib/products';
import { useCart } from '../context/CartContext';

export function ProductDetailScreen({ route, navigation }: any) {
  const { productId } = route.params;
  const product = PRODUCTS.find((p) => p.id === productId);
  const { items, addItem } = useCart();

  if (!product) {
    return (
      <SafeAreaView style={s.container}>
        <Text style={s.error}>Product not found</Text>
      </SafeAreaView>
    );
  }

  const inCart = items.some((i) => i.id === product.id);

  return (
    <SafeAreaView style={s.container}>
      <ScrollView>
        <TouchableOpacity style={s.back} onPress={() => navigation.goBack()}>
          <Text style={s.backText}>{'\u2190'} Back</Text>
        </TouchableOpacity>

        <View style={[s.hero, { backgroundColor: product.color + '20' }]} />

        <View style={s.content}>
          <Text style={s.category}>{product.category}</Text>
          <Text style={s.name}>{product.name}</Text>
          {/* BUG: crashes if price is null — no null check */}
          <Text style={s.price}>${product.price.toFixed(2)}</Text>
          <Text style={s.desc}>
            Premium quality {product.name.toLowerCase()} crafted with attention to detail.
            Perfect for everyday use. Free shipping on orders over $50.
          </Text>

          <View style={s.actions}>
            <TouchableOpacity
              style={[s.addBtn, inCart && s.addBtnDisabled]}
              onPress={() => addItem({ id: product.id, name: product.name, price: product.price })}
            >
              <Text style={s.addBtnText}>{inCart ? 'In Cart' : 'Add to Cart'}</Text>
            </TouchableOpacity>
            <TouchableOpacity style={s.wishBtn}>
              <Text style={s.wishBtnText}>{'\u2661'}</Text>
            </TouchableOpacity>
          </View>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0f1117' },
  error: { color: '#ef4444', fontSize: 16, textAlign: 'center', marginTop: 40 },
  back: { padding: 16 },
  backText: { fontSize: 14, color: '#6366f1' },
  hero: { height: 240, marginHorizontal: 16, borderRadius: 20 },
  content: { padding: 20 },
  category: { fontSize: 12, color: '#888', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4 },
  name: { fontSize: 24, fontWeight: '700', color: '#f5f5f5', marginBottom: 8 },
  price: { fontSize: 22, fontWeight: '700', color: '#f5f5f5', marginBottom: 16 },
  desc: { fontSize: 14, lineHeight: 22, color: '#999', marginBottom: 24 },
  actions: { flexDirection: 'row', gap: 12 },
  addBtn: { flex: 1, backgroundColor: '#6366f1', borderRadius: 14, paddingVertical: 16, alignItems: 'center' },
  addBtnDisabled: { backgroundColor: '#1a1a2e' },
  addBtnText: { fontSize: 16, fontWeight: '700', color: '#fff' },
  wishBtn: { width: 56, borderRadius: 14, borderWidth: 1, borderColor: '#2a2a3e', alignItems: 'center', justifyContent: 'center' },
  wishBtnText: { fontSize: 24, color: '#999' },
});
