import React from 'react';
import { View, Text, TouchableOpacity, StyleSheet } from 'react-native';

interface Product {
  id: string;
  name: string;
  price: number;
  category: string;
  color: string;
}

interface ProductCardProps {
  product: Product;
  onPress: (product: Product) => void;
}

export function ProductCard({ product, onPress }: ProductCardProps) {
  return (
    <TouchableOpacity style={styles.card} onPress={() => onPress(product)}>
      <View style={[styles.imagePlaceholder, { backgroundColor: product.color + '20' }]}>
        <Text style={[styles.imageText, { color: product.color }]}>{product.category[0]}</Text>
      </View>
      <Text style={styles.name} numberOfLines={1}>{product.name}</Text>
      <Text style={styles.price}>${product.price?.toFixed(2) ?? '—'}</Text>
    </TouchableOpacity>
  );
}

const styles = StyleSheet.create({
  card: {
    flex: 1,
    backgroundColor: '#f8f8f8',
    borderRadius: 14,
    padding: 10,
    margin: 4,
  },
  imagePlaceholder: {
    height: 120,
    borderRadius: 10,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: 8,
  },
  imageText: {
    fontSize: 32,
    fontWeight: '700',
  },
  name: {
    fontSize: 13,
    fontWeight: '600',
    color: '#333',
    marginBottom: 4,
  },
  price: {
    fontSize: 15,
    fontWeight: '700',
    color: '#111',
  },
});
