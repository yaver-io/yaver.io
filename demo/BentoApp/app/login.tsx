import React from 'react';
import { SafeAreaView, StyleSheet } from 'react-native';
import { useRouter } from 'expo-router';
import { LoginForm } from '../src/components/LoginForm';
import { useAuth } from '../src/context/AuthContext';

export default function LoginScreen() {
  const { login } = useAuth();
  const router = useRouter();

  const handleLogin = async (email: string, password: string) => {
    await login(email, password);
    router.replace('/(tabs)');
  };

  return (
    <SafeAreaView style={s.container}>
      <LoginForm onLogin={handleLogin} />
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#fff', justifyContent: 'center' },
});
