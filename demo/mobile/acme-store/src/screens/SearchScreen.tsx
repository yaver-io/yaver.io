import React, { useState } from 'react';
import {
  FlatList,
  SafeAreaView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { PRODUCTS } from '../lib/products';

export function SearchScreen({ navigation }: any) {
  const [query, setQuery] = useState('');

  const results = query.trim()
    ? PRODUCTS.filter((p) =>
        p.name.toLowerCase().includes(query.toLowerCase()) ||
        p.category.toLowerCase().includes(query.toLowerCase())
      )
    : [];

  return (
    <SafeAreaView style={s.container}>
      <Text style={s.title}>Search</Text>
      <View style={s.searchBar}>
        <TextInput
          style={s.input}
          value={query}
          onChangeText={setQuery}
          placeholder="Search products..."
          placeholderTextColor="#666"
          autoCorrect={false}
        />
      </View>

      {query.trim() ? (
        <FlatList
          data={results}
          keyExtractor={(item) => item.id}
          renderItem={({ item }) => (
            <TouchableOpacity
              style={s.resultRow}
              onPress={() => navigation.navigate('ProductDetail', { productId: item.id })}
            >
              <View style={[s.thumb, { backgroundColor: item.color + '30' }]} />
              <View style={{ flex: 1 }}>
                <Text style={s.resultName}>{item.name}</Text>
                <Text style={s.resultCat}>{item.category}</Text>
              </View>
              <Text style={s.resultPrice}>${item.price?.toFixed(2)}</Text>
            </TouchableOpacity>
          )}
          ListEmptyComponent={
            <Text style={s.empty}>No results for &quot;{query}&quot;</Text>
          }
        />
      ) : (
        <View style={s.suggestions}>
          <Text style={s.sectionLabel}>Recent</Text>
          {['running shoes', 'leather wallet', 'wireless earbuds'].map((q) => (
            <TouchableOpacity key={q} style={s.recentRow} onPress={() => setQuery(q)}>
              <Text style={s.recentText}>{q}</Text>
            </TouchableOpacity>
          ))}
          <Text style={[s.sectionLabel, { marginTop: 24 }]}>Trending</Text>
          <View style={s.tags}>
            {['Nike', 'Apple Watch', 'Ray-Ban', 'Adidas'].map((t) => (
              <TouchableOpacity key={t} style={s.tag} onPress={() => setQuery(t.toLowerCase())}>
                <Text style={s.tagText}>{t}</Text>
              </TouchableOpacity>
            ))}
          </View>
        </View>
      )}
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0f1117' },
  title: { fontSize: 22, fontWeight: '700', color: '#f5f5f5', padding: 20, paddingBottom: 12 },
  searchBar: { paddingHorizontal: 16, marginBottom: 16 },
  input: { backgroundColor: '#1a1a2e', borderRadius: 12, paddingHorizontal: 16, paddingVertical: 12, fontSize: 15, color: '#f5f5f5', borderWidth: 1, borderColor: '#2a2a3e' },
  resultRow: { flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 16, paddingVertical: 12, borderBottomWidth: 1, borderBottomColor: '#1a1a2e' },
  thumb: { width: 48, height: 48, borderRadius: 10 },
  resultName: { fontSize: 14, fontWeight: '600', color: '#e5e5e5' },
  resultCat: { fontSize: 12, color: '#888', marginTop: 2 },
  resultPrice: { fontSize: 14, fontWeight: '700', color: '#f5f5f5' },
  empty: { textAlign: 'center', color: '#666', marginTop: 40, fontSize: 14 },
  suggestions: { padding: 16 },
  sectionLabel: { fontSize: 12, fontWeight: '600', color: '#888', marginBottom: 12, textTransform: 'uppercase', letterSpacing: 1 },
  recentRow: { paddingVertical: 12, borderBottomWidth: 1, borderBottomColor: '#1a1a2e' },
  recentText: { fontSize: 14, color: '#ccc' },
  tags: { flexDirection: 'row', flexWrap: 'wrap', gap: 8 },
  tag: { backgroundColor: '#1a1a2e', borderRadius: 20, paddingHorizontal: 14, paddingVertical: 8 },
  tagText: { fontSize: 12, color: '#ccc' },
});
