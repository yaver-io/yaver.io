import React from 'react';
import { NavigationContainer } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { StatusBar } from 'react-native';
import { AuthProvider, useAuth } from './src/context/AuthContext';
import { CartProvider } from './src/context/CartContext';
import { HomeScreen } from './src/screens/HomeScreen';
import { SearchScreen } from './src/screens/SearchScreen';
import { CartScreen } from './src/screens/CartScreen';
import { ProfileScreen } from './src/screens/ProfileScreen';
import { LoginScreen } from './src/screens/LoginScreen';
import { ProductDetailScreen } from './src/screens/ProductDetailScreen';

// ─── Yaver Feedback SDK (dev only) ───
// import { YaverFeedback, FloatingButton, BlackBox } from '@yaver/feedback-react-native';

const Tab = createBottomTabNavigator();
const Stack = createNativeStackNavigator();

function HomeTabs() {
  return (
    <Tab.Navigator
      screenOptions={{
        headerShown: false,
        tabBarStyle: { backgroundColor: '#0f1117', borderTopColor: '#1a1a2e' },
        tabBarActiveTintColor: '#fff',
        tabBarInactiveTintColor: '#666',
      }}
    >
      <Tab.Screen name="Home" component={HomeScreen} />
      <Tab.Screen name="Search" component={SearchScreen} />
      <Tab.Screen name="Cart" component={CartScreen} />
      <Tab.Screen name="Profile" component={ProfileScreen} />
    </Tab.Navigator>
  );
}

function AppNavigator() {
  const { user } = useAuth();

  return (
    <Stack.Navigator screenOptions={{ headerShown: false }}>
      {!user ? (
        <Stack.Screen name="Login" component={LoginScreen} />
      ) : (
        <>
          <Stack.Screen name="Main" component={HomeTabs} />
          <Stack.Screen name="ProductDetail" component={ProductDetailScreen} />
        </>
      )}
    </Stack.Navigator>
  );
}

export default function App() {
  // ─── Uncomment to enable Yaver Feedback SDK ───
  // const isDev = __DEV__;
  // if (isDev && !YaverFeedback.isInitialized()) {
  //   YaverFeedback.init({ trigger: 'floating-button' });
  //   BlackBox.start();
  //   BlackBox.wrapConsole();
  // }

  return (
    <AuthProvider>
      <CartProvider>
        <NavigationContainer>
          <StatusBar barStyle="light-content" />
          <AppNavigator />
          {/* {isDev && <FloatingButton />} */}
        </NavigationContainer>
      </CartProvider>
    </AuthProvider>
  );
}
