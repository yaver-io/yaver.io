import { useEffect } from 'react';
import { Slot } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { AuthProvider } from '../src/context/AuthContext';
import { CartProvider } from '../src/context/CartContext';
import { YaverFeedback } from '../src/yaver-sdk/YaverFeedback';
import { FloatingButton } from '../src/yaver-sdk/FloatingButton';
import { BlackBox } from '../src/yaver-sdk/BlackBox';

export default function RootLayout() {
  useEffect(() => {
    // Initialize Yaver Feedback SDK
    if (!YaverFeedback.isInitialized()) {
      YaverFeedback.init({
        authToken: 'demo',
        trigger: 'floating-button',
        buildPlatforms: 'ios',
        autoDeploy: false,
      });
      BlackBox.start();
    }
  }, []);

  return (
    <AuthProvider>
      <CartProvider>
        <StatusBar style="dark" />
        <Slot />
        <FloatingButton
          color="#1a1a1a"
          style="terminal"
          size={44}
        />
      </CartProvider>
    </AuthProvider>
  );
}
