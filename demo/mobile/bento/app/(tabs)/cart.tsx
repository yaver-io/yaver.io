import React from 'react';
import { SafeAreaView, StyleSheet, Text, TouchableOpacity, View } from 'react-native';

export default function CartScreen() {
  return (
    <SafeAreaView style={s.container}>
      <Text style={s.title}>Cart</Text>
      <View style={s.empty}>
        <Text style={s.emptyIcon}>{'\uD83D\uDED2'}</Text>
        <Text style={s.emptyText}>Your cart is empty</Text>
      </View>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#fff', padding: 20 },
  title: { fontSize: 22, fontWeight: '700', color: '#111' },
  empty: { flex: 1, alignItems: 'center', justifyContent: 'center' },
  emptyIcon: { fontSize: 48, marginBottom: 12 },
  emptyText: { fontSize: 15, color: '#999' },
});
