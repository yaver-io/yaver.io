import React from 'react';
import {
  FlatList,
  SafeAreaView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import { useCart } from '../context/CartContext';

export function CartScreen() {
  const { items, removeItem, total } = useCart();

  return (
    <SafeAreaView style={s.container}>
      <Text style={s.title}>Cart ({items.length})</Text>

      {items.length === 0 ? (
        <View style={s.empty}>
          <Text style={s.emptyIcon}>{'\uD83D\uDED2'}</Text>
          <Text style={s.emptyText}>Your cart is empty</Text>
        </View>
      ) : (
        <>
          <FlatList
            data={items}
            keyExtractor={(item) => item.id}
            renderItem={({ item }) => (
              <View style={s.itemRow}>
                <View style={{ flex: 1 }}>
                  <Text style={s.itemName}>{item.name}</Text>
                  <Text style={s.itemQty}>Qty: {item.qty}</Text>
                  {/* BUG: crashes if price is null/undefined */}
                  <Text style={s.itemPrice}>${item.price.toFixed(2)}</Text>
                </View>
                <TouchableOpacity onPress={() => removeItem(item.id)} style={s.removeBtn}>
                  <Text style={s.removeBtnText}>Remove</Text>
                </TouchableOpacity>
              </View>
            )}
          />
          <View style={s.totalRow}>
            <Text style={s.totalLabel}>Total</Text>
            <Text style={s.totalValue}>${total.toFixed(2)}</Text>
          </View>
          <TouchableOpacity style={s.checkoutBtn}>
            <Text style={s.checkoutText}>Checkout</Text>
          </TouchableOpacity>
        </>
      )}
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0f1117' },
  title: { fontSize: 22, fontWeight: '700', color: '#f5f5f5', padding: 20, paddingBottom: 16 },
  empty: { flex: 1, alignItems: 'center', justifyContent: 'center' },
  emptyIcon: { fontSize: 48, marginBottom: 12 },
  emptyText: { fontSize: 15, color: '#666' },
  itemRow: { flexDirection: 'row', alignItems: 'center', paddingHorizontal: 16, paddingVertical: 14, borderBottomWidth: 1, borderBottomColor: '#1a1a2e' },
  itemName: { fontSize: 15, fontWeight: '600', color: '#e5e5e5' },
  itemQty: { fontSize: 12, color: '#888', marginTop: 2 },
  itemPrice: { fontSize: 14, fontWeight: '700', color: '#f5f5f5', marginTop: 4 },
  removeBtn: { paddingHorizontal: 12, paddingVertical: 6, borderRadius: 8, backgroundColor: '#1a1a2e' },
  removeBtnText: { fontSize: 12, color: '#f87171' },
  totalRow: { flexDirection: 'row', justifyContent: 'space-between', paddingHorizontal: 20, paddingVertical: 16, borderTopWidth: 1, borderTopColor: '#1a1a2e' },
  totalLabel: { fontSize: 14, color: '#888' },
  totalValue: { fontSize: 18, fontWeight: '700', color: '#f5f5f5' },
  checkoutBtn: { marginHorizontal: 16, marginBottom: 20, backgroundColor: '#6366f1', borderRadius: 14, paddingVertical: 16, alignItems: 'center' },
  checkoutText: { fontSize: 16, fontWeight: '700', color: '#fff' },
});
