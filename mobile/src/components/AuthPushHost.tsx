// AuthPushHost — mounts the device-auth approval push channel (P2).
// Registers this phone's push token whenever a session token is present, and
// keeps the notification listener installed so a "device_auth_request" push
// opens the Face-ID approval screen. Renders nothing.

import { useEffect } from "react";
import { useAuth } from "../context/AuthContext";
import {
  registerForAuthPush,
  installAuthPushListener,
  installPushTokenRefreshListener,
} from "../lib/pushAuth";
import { installTaskReviewNotificationListener } from "../lib/taskReviewNotification";

export function AuthPushHost() {
  const { token } = useAuth();

  useEffect(() => {
    if (!token) return;
    void registerForAuthPush(token);
    return installPushTokenRefreshListener(token);
  }, [token]);

  useEffect(() => {
    const unsub = installAuthPushListener();
    return unsub;
  }, []);

  useEffect(() => {
    const unsub = installTaskReviewNotificationListener();
    return unsub;
  }, []);

  return null;
}
