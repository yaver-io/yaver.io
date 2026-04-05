/**
 * Yaver Feedback SDK — React Native Example
 *
 * Shows all three modes + connection screen + voice commands.
 * The user selects the mode at runtime from within their app.
 */

import React, { useState } from 'react';
import { View, Text, TouchableOpacity, StyleSheet, ScrollView } from 'react-native';
// import {
//   YaverFeedback,
//   YaverConnectionScreen,
//   YaverFeedbackButton,
// } from 'yaver-feedback-react-native';

// Initialize in dev mode only
// if (__DEV__) {
//   YaverFeedback.init({
//     trigger: 'floating-button',
//     // agentUrl auto-discovered on LAN
//   });
// }

type Mode = 'live' | 'narrated' | 'batch';

const modes: Array<{ key: Mode; title: string; color: string; description: string }> = [
  {
    key: 'live',
    title: 'Full Interactive',
    color: '#dc2626',
    description:
      'Agent sees your screen live. Vision model detects bugs. ' +
      'Hot reload fixes as you speak. Say "make this bigger" and it happens.',
  },
  {
    key: 'narrated',
    title: 'Semi Interactive',
    color: '#f59e0b',
    description:
      'Agent sees your screen and comments, but no auto-fix. ' +
      'Conversation mode. Say "fix it now" or "keep in mind for later".',
  },
  {
    key: 'batch',
    title: 'Post Mode',
    color: '#16a34a',
    description:
      'Record everything offline. No streaming. Submit compressed ' +
      'bundle when done. Best for slow connections or detailed QA.',
  },
];

export default function FeedbackExample() {
  const [selectedMode, setSelectedMode] = useState<Mode>('narrated');
  const [showConnection, setShowConnection] = useState(false);

  if (showConnection) {
    // return <YaverConnectionScreen />;
    return (
      <View style={styles.container}>
        <Text style={styles.title}>Connection Screen</Text>
        <Text style={styles.subtitle}>
          (YaverConnectionScreen would render here — auto-discovers your dev machine)
        </Text>
        <TouchableOpacity style={styles.button} onPress={() => setShowConnection(false)}>
          <Text style={styles.buttonText}>Back</Text>
        </TouchableOpacity>
      </View>
    );
  }

  return (
    <ScrollView style={styles.container}>
      <Text style={styles.title}>Yaver Feedback SDK</Text>
      <Text style={styles.subtitle}>Select a mode, then test your app</Text>

      {/* Mode selector */}
      {modes.map((mode) => (
        <TouchableOpacity
          key={mode.key}
          style={[
            styles.modeCard,
            {
              borderColor: selectedMode === mode.key ? mode.color : '#333',
              backgroundColor: selectedMode === mode.key ? mode.color + '15' : 'transparent',
            },
          ]}
          onPress={() => {
            setSelectedMode(mode.key);
            // YaverFeedback.setMode(mode.key);
          }}
        >
          <View style={styles.modeHeader}>
            <View style={[styles.dot, { backgroundColor: mode.color }]} />
            <Text style={styles.modeTitle}>{mode.title}</Text>
            {selectedMode === mode.key && <Text style={{ color: mode.color }}>✓</Text>}
          </View>
          <Text style={styles.modeDesc}>{mode.description}</Text>
        </TouchableOpacity>
      ))}

      {/* Connection button */}
      <TouchableOpacity style={[styles.button, { marginTop: 16 }]} onPress={() => setShowConnection(true)}>
        <Text style={styles.buttonText}>Connection Settings</Text>
      </TouchableOpacity>

      {/* Simulated app content */}
      <View style={styles.appContent}>
        <Text style={styles.sectionTitle}>Your App Content</Text>
        <TouchableOpacity style={[styles.button, { backgroundColor: '#2563eb' }]}>
          <Text style={styles.buttonText}>Login (has a bug)</Text>
        </TouchableOpacity>
        <View style={styles.bugElement}>
          <Text style={{ color: 'white', fontSize: 12 }}>This overlapping element is a bug</Text>
        </View>
      </View>

      {/* The floating button from SDK would appear here */}
      {/* <YaverFeedbackButton /> */}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0d0d1a', padding: 16 },
  title: { color: '#e0e0e0', fontSize: 20, fontWeight: '700', marginBottom: 4 },
  subtitle: { color: '#888', fontSize: 14, marginBottom: 16 },
  modeCard: { borderWidth: 1, borderRadius: 8, padding: 12, marginBottom: 8 },
  modeHeader: { flexDirection: 'row', alignItems: 'center', gap: 8, marginBottom: 4 },
  dot: { width: 10, height: 10, borderRadius: 5 },
  modeTitle: { color: '#e0e0e0', fontWeight: '600', fontSize: 14, flex: 1 },
  modeDesc: { color: '#888', fontSize: 12 },
  button: { backgroundColor: '#333', padding: 12, borderRadius: 8, alignItems: 'center' },
  buttonText: { color: '#e0e0e0', fontWeight: '600', fontSize: 13 },
  sectionTitle: { color: '#e0e0e0', fontSize: 14, fontWeight: '600', marginBottom: 8 },
  appContent: { marginTop: 20, borderTopWidth: 1, borderTopColor: '#333', paddingTop: 16 },
  bugElement: { backgroundColor: '#dc262660', padding: 10, borderRadius: 8, marginTop: 8 },
});
