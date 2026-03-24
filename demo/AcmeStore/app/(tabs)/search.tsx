import React, { useState } from 'react';
import { SafeAreaView, StyleSheet, Text, TextInput, View } from 'react-native';

export default function SearchScreen() {
  const [query, setQuery] = useState('');

  return (
    <SafeAreaView style={s.container}>
      <Text style={s.title}>Search</Text>
      <TextInput style={s.input} value={query} onChangeText={setQuery} placeholder="Search products..." placeholderTextColor="#999" />
      <Text style={s.hint}>Try searching for &quot;shoes&quot; or &quot;watch&quot;</Text>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#fff', padding: 20 },
  title: { fontSize: 22, fontWeight: '700', color: '#111', marginBottom: 16 },
  input: { backgroundColor: '#f5f5f5', borderRadius: 12, paddingHorizontal: 16, paddingVertical: 14, fontSize: 15, color: '#111' },
  hint: { marginTop: 16, fontSize: 13, color: '#999', textAlign: 'center' },
});
