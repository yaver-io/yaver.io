import { useEffect, useState } from 'react';
import { DeviceEventEmitter, Platform } from 'react-native';
import { Slot } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { AuthProvider } from '../src/context/AuthContext';
import { CartProvider } from '../src/context/CartContext';
import { YaverFeedback } from '../src/yaver-sdk/YaverFeedback';
import { FloatingButton } from '../src/yaver-sdk/FloatingButton';
import { FeedbackModal } from '../src/yaver-sdk/FeedbackModal';
import { BlackBox } from '../src/yaver-sdk/BlackBox';

// Generated at build time by: node yaver.config.js
// Reads ~/.yaver/config.json + discovers local IP
const yaverConfig = require('../src/yaver-sdk/config.generated.json');

export default function RootLayout() {
  const [sdkReady, setSdkReady] = useState(false);

  useEffect(() => {
    if (YaverFeedback.isInitialized()) { setSdkReady(true); return; }

    const { authToken, agentUrl, convexUrl } = yaverConfig;

    if (!authToken) {
      console.warn('[YaverSDK] No token. Run `yaver auth` first.');
      return;
    }

    // Connect: direct LAN → Convex discovery → relay fallback
    (async () => {
      let resolvedUrl = '';

      // 1. Direct LAN (same WiFi, ~5ms)
      if (agentUrl) {
        try {
          const c = new AbortController();
          const t = setTimeout(() => c.abort(), 3000);
          const r = await fetch(`${agentUrl}/health`, { signal: c.signal });
          clearTimeout(t);
          if (r.ok) resolvedUrl = agentUrl;
        } catch {}
      }

      // 2. Convex device discovery (cross-network)
      if (!resolvedUrl && convexUrl) {
        try {
          const r = await fetch(`${convexUrl}/devices/list`, {
            headers: { Authorization: `Bearer ${authToken}` },
          });
          if (r.ok) {
            const data = await r.json();
            const devices = data.devices || data || [];
            const online = devices.find((d: any) => d.isOnline);
            if (online) {
              const url = `http://${online.localIP || online.ip}:${online.httpPort || 18080}`;
              try {
                const c = new AbortController();
                const t = setTimeout(() => c.abort(), 3000);
                const hr = await fetch(`${url}/health`, { signal: c.signal });
                clearTimeout(t);
                if (hr.ok) resolvedUrl = url;
              } catch {}
            }
          }
        } catch {}
      }

      // 3. Relay fallback (NAT traversal, 4G)
      if (!resolvedUrl && convexUrl) {
        try {
          const r = await fetch(`${convexUrl}/platformConfig`);
          if (r.ok) {
            const cfg = await r.json();
            for (const relay of (cfg.relayServers || [])) {
              try {
                const url = `${relay.url}/proxy`;
                const c = new AbortController();
                const t = setTimeout(() => c.abort(), 5000);
                const hr = await fetch(`${url}/health`, {
                  signal: c.signal,
                  headers: { Authorization: `Bearer ${authToken}` },
                });
                clearTimeout(t);
                if (hr.ok) { resolvedUrl = url; break; }
              } catch { continue; }
            }
          }
        } catch {}
      }

      YaverFeedback.init({
        agentUrl: resolvedUrl || agentUrl,
        authToken,
        convexUrl,
        trigger: 'shake',
        buildPlatforms: 'both',
        autoDeploy: true,
      });
      BlackBox.start();
      BlackBox.wrapConsole();
      setSdkReady(true);
      console.log('[YaverSDK]', resolvedUrl ? `Connected: ${resolvedUrl}` : 'Offline');

      // Wire shake detection → open feedback modal
      // iOS emits 'shakeEvent' in debug, Android needs react-native-shake or manual
      const shakeHandler = () => {
        YaverFeedback.startReport();
      };
      if (Platform.OS === 'ios') {
        DeviceEventEmitter.addListener('shakeEvent', shakeHandler);
      } else {
        DeviceEventEmitter.addListener('ShakeEvent', shakeHandler);
      }
    })();
  }, []);

  return (
    <AuthProvider>
      <CartProvider>
        <StatusBar style="dark" />
        <Slot />
        {sdkReady && (
          <>
            <FeedbackModal />
            <FloatingButton
              color="#1a1a1a"
              style="terminal"
              size={44}
            />
          </>
        )}
      </CartProvider>
    </AuthProvider>
  );
}
