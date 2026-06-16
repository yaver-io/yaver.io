// carMessagingNotification.test.mts — Tier 1 MessagingStyle extras builder.
// Run: npx tsx src/lib/carMessagingNotification.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  buildAndroidAutoExtras,
  buildCarNotificationContent,
  presentCarConversation,
  CAR_NOTIFICATION_CHANNEL_ID,
  CAR_REPLY_REMOTE_INPUT_KEY,
  type CarConversation,
} from "./carMessagingNotification.ts";

const conv: CarConversation = {
  conversationId: "magara#42",
  contactName: "Yaver · magara",
  messages: [
    { from: "you", text: "fix the build", timestamp: 100 },
    { from: "agent", text: "On it.", timestamp: 200 },
    { from: "agent", text: "Done. Tests pass.", timestamp: 300 },
  ],
};

test("buildAndroidAutoExtras orders messages and picks the last agent reply to read", () => {
  const x = buildAndroidAutoExtras(conv);
  assert.equal(x.channelId, CAR_NOTIFICATION_CHANNEL_ID);
  assert.equal(x.remoteInputKey, CAR_REPLY_REMOTE_INPUT_KEY);
  assert.equal(x.category, "msg");
  assert.equal(x.messages.length, 3);
  assert.equal(x.messages[0].fromSelf, true);
  assert.equal(x.unreadText, "Done. Tests pass.");
  assert.equal(x.latestTimestamp, 300);
});

test("buildCarNotificationContent surfaces the unread agent text as the body", () => {
  const c = buildCarNotificationContent(conv);
  assert.equal(c.title, "Yaver · magara");
  assert.equal(c.body, "Done. Tests pass.");
  assert.equal(c.data.kind, "car-agent-message");
  assert.equal(c.data.conversationId, "magara#42");
  assert.ok(c.data.androidAutoExtras);
});

test("presentCarConversation no-ops safely without expo-notifications", async () => {
  // In tsx/node there's no expo-notifications module → should return false,
  // never throw.
  const ok = await presentCarConversation(conv);
  assert.equal(ok, false);
});
