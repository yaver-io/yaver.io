// AuthPushHost — mounts the device-auth approval push channel (P2).
// Registers this phone's push token whenever a session token is present, and
// keeps the notification listener installed so a "device_auth_request" push
// opens the Face-ID approval screen. Renders nothing. Dormant until a push
// transport (EAS projectId / native APNs/FCM) is configured — see pushAuth.ts.

import { useEffect } from "react";
import { useAuth } from "../context/AuthContext";
import { registerForAuthPush, installAuthPushListener } from "../lib/pushAuth";

export function AuthPushHost() {
  const { token } = useAuth();

  useEffect(() => {
    if (token) void registerForAuthPush(token);
  }, [token]);

  useEffect(() => {
    const unsub = installAuthPushListener();
    return unsub;
  }, []);

  return null;
}
