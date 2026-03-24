import React from 'react';
import { SafeAreaView, StyleSheet, Text, TouchableOpacity, View } from 'react-native';
import { useRouter } from 'expo-router';
import { useAuth } from '../../src/context/AuthContext';

export default function ProfileScreen() {
  const { user, logout } = useAuth();
  const router = useRouter();

  const handleSignOut = () => {
    logout();
    router.replace('/login');
  };

  return (
    <SafeAreaView style={s.container}>
      <View style={s.header}>
        <View style={s.avatar}><Text style={s.avatarText}>{user?.name?.[0] ?? 'J'}</Text></View>
        <Text style={s.name}>{user?.name ?? 'Jane Developer'}</Text>
        <Text style={s.email}>{user?.email ?? 'jane@acme.dev'}</Text>
      </View>
      {['Orders', 'Wishlist', 'Addresses', 'Settings'].map((item) => (
        <TouchableOpacity key={item} style={s.row}>
          <Text style={s.rowText}>{item}</Text>
          <Text style={s.arrow}>{'\u203A'}</Text>
        </TouchableOpacity>
      ))}
      <TouchableOpacity style={s.signOut} onPress={handleSignOut}>
        <Text style={s.signOutText}>Sign Out</Text>
      </TouchableOpacity>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#fff' },
  header: { alignItems: 'center', paddingVertical: 32 },
  avatar: { width: 72, height: 72, borderRadius: 36, backgroundColor: '#6366f1', alignItems: 'center', justifyContent: 'center', marginBottom: 12 },
  avatarText: { fontSize: 28, fontWeight: '700', color: '#fff' },
  name: { fontSize: 18, fontWeight: '700', color: '#111' },
  email: { fontSize: 13, color: '#999', marginTop: 4 },
  row: { flexDirection: 'row', justifyContent: 'space-between', paddingHorizontal: 20, paddingVertical: 16, borderBottomWidth: 1, borderBottomColor: '#f0f0f0' },
  rowText: { fontSize: 15, color: '#333' },
  arrow: { fontSize: 20, color: '#ccc' },
  signOut: { marginHorizontal: 20, marginTop: 24, borderRadius: 12, borderWidth: 1, borderColor: '#e5e5e5', paddingVertical: 14, alignItems: 'center' },
  signOutText: { fontSize: 14, color: '#999' },
});
