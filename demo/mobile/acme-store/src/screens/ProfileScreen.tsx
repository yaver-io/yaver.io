import React from 'react';
import { SafeAreaView, StyleSheet, Text, TouchableOpacity, View } from 'react-native';
import { useAuth } from '../context/AuthContext';

export function ProfileScreen() {
  const { user, logout } = useAuth();

  return (
    <SafeAreaView style={s.container}>
      <View style={s.header}>
        {/* BUG: crashes if user.name is undefined — no fallback */}
        <View style={s.avatar}>
          <Text style={s.avatarText}>{user?.name?.[0] ?? '?'}</Text>
        </View>
        <Text style={s.name}>{user?.name}</Text>
        <Text style={s.email}>{user?.email}</Text>
      </View>

      {['Orders', 'Wishlist', 'Addresses', 'Payment Methods', 'Settings'].map((item) => (
        <TouchableOpacity key={item} style={s.menuRow}>
          <Text style={s.menuText}>{item}</Text>
          <Text style={s.arrow}>{'\u203A'}</Text>
        </TouchableOpacity>
      ))}

      <TouchableOpacity style={s.logoutBtn} onPress={logout}>
        <Text style={s.logoutText}>Sign Out</Text>
      </TouchableOpacity>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0f1117' },
  header: { alignItems: 'center', paddingVertical: 32 },
  avatar: { width: 72, height: 72, borderRadius: 36, backgroundColor: '#6366f1', alignItems: 'center', justifyContent: 'center', marginBottom: 12 },
  avatarText: { fontSize: 28, fontWeight: '700', color: '#fff' },
  name: { fontSize: 18, fontWeight: '700', color: '#f5f5f5' },
  email: { fontSize: 13, color: '#888', marginTop: 4 },
  menuRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', paddingHorizontal: 20, paddingVertical: 16, borderBottomWidth: 1, borderBottomColor: '#1a1a2e' },
  menuText: { fontSize: 15, color: '#e5e5e5' },
  arrow: { fontSize: 20, color: '#666' },
  logoutBtn: { marginHorizontal: 20, marginTop: 32, borderRadius: 14, borderWidth: 1, borderColor: '#2a2a3e', paddingVertical: 14, alignItems: 'center' },
  logoutText: { fontSize: 14, color: '#888' },
});
