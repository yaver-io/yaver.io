import React from 'react';
import { SafeAreaView, StyleSheet, StatusBar } from 'react-native';
import { LoginForm } from '../components/LoginForm';
import { authService } from '../lib/auth';

export function LoginScreen() {
  const handleLogin = async (email: string, password: string) => {
    const result = await authService.login(email, password);
    if (!result.success) {
      throw new Error(result.error || 'Authentication failed');
    }
    // Navigate to home on success
  };

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar barStyle="light-content" />
      <LoginForm onLogin={handleLogin} />
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#0f1117',
    justifyContent: 'center',
  },
});
