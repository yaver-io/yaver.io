import React from 'react';
import { View, Text, Image, TouchableOpacity, StyleSheet } from 'react-native';

interface Product {
  id: string;
  name: string;
  price: number;
  image: string;
  category: string;
}

interface ProductCardProps {
  product: Product;
  onPress: (product: Product) => void;
}

export function ProductCard({ product, onPress }: ProductCardProps) {
  return (
    <TouchableOpacity style={styles.card} onPress={() => onPress(product)}>
      <View style={styles.imagePlaceholder}>
        <Text style={styles.imageText}>{product.category[0]}</Text>
      </View>
      <Text style={styles.name} numberOfLines={1}>{product.name}</Text>
      <Text style={styles.price}>${product.price.toFixed(2)}</Text>
    </TouchableOpacity>
  );
}

const styles = StyleSheet.create({
  card: {
    flex: 1,
    backgroundColor: '#1a1a2e',
    borderRadius: 14,
    padding: 10,
    margin: 4,
  },
  imagePlaceholder: {
    height: 120,
    borderRadius: 10,
    backgroundColor: '#2a2a3e',
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: 8,
  },
  imageText: {
    fontSize: 32,
    color: '#6366f1',
    fontWeight: '700',
  },
  name: {
    fontSize: 13,
    fontWeight: '600',
    color: '#e5e5e5',
    marginBottom: 4,
  },
  price: {
    fontSize: 15,
    fontWeight: '700',
    color: '#f5f5f5',
  },
});
