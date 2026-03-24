import React from 'react';
import {
  FlatList,
  SafeAreaView,
  StatusBar,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { ProductCard } from '../components/ProductCard';

const PRODUCTS = [
  { id: '1', name: 'Running Shoe Pro', price: 129.99, image: '', category: 'Shoes' },
  { id: '2', name: 'Leather Messenger Bag', price: 89.00, image: '', category: 'Bags' },
  { id: '3', name: 'Smart Watch Ultra', price: 249.00, image: '', category: 'Watches' },
  { id: '4', name: 'Polarized Sunglasses', price: 65.00, image: '', category: 'Accessories' },
  { id: '5', name: 'Canvas Backpack', price: 45.00, image: '', category: 'Bags' },
  { id: '6', name: 'Wireless Earbuds', price: 79.99, image: '', category: 'Electronics' },
];

export function HomeScreen() {
  return (
    <SafeAreaView style={styles.container}>
      <StatusBar barStyle="light-content" />
      <View style={styles.header}>
        <View>
          <Text style={styles.greeting}>Good morning</Text>
          <Text style={styles.title}>Acme Store</Text>
        </View>
        <View style={styles.avatar} />
      </View>

      <View style={styles.banner}>
        <Text style={styles.bannerTitle}>Summer Sale</Text>
        <Text style={styles.bannerSubtitle}>Up to 40% off selected items</Text>
      </View>

      <FlatList
        data={PRODUCTS}
        numColumns={2}
        keyExtractor={(item) => item.id}
        renderItem={({ item }) => (
          <ProductCard product={item} onPress={() => {}} />
        )}
        contentContainerStyle={styles.grid}
      />
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#0f1117',
  },
  header: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingHorizontal: 20,
    paddingTop: 16,
    paddingBottom: 12,
  },
  greeting: {
    fontSize: 13,
    color: '#888',
  },
  title: {
    fontSize: 22,
    fontWeight: '700',
    color: '#f5f5f5',
  },
  avatar: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: '#6366f1',
  },
  banner: {
    marginHorizontal: 16,
    marginBottom: 16,
    borderRadius: 16,
    padding: 16,
    backgroundColor: '#6366f130',
  },
  bannerTitle: {
    fontSize: 16,
    fontWeight: '700',
    color: '#a5b4fc',
  },
  bannerSubtitle: {
    fontSize: 12,
    color: '#818cf8',
    marginTop: 2,
  },
  grid: {
    paddingHorizontal: 12,
    paddingBottom: 80,
  },
});
