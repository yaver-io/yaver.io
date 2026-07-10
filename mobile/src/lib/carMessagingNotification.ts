/**
 * carMessagingNotification.ts — Tier 1 Android Auto MessagingStyle surface.
 *
 * See docs/yaver-car-voice-coding.md §3 (Tier 1) and §7.1 (gap).
 *
 * Models the remote coding agent as a CONTACT you message by voice. Each
 * agent status update is an incoming "message" in a MessagingStyle
 * conversation; Android Auto reads new messages aloud and offers a voice
 * reply (RemoteInput) that re-enters the carVoiceCoding dispatch loop.
 *
 * ── HONEST GAP (docs §7.1) ───────────────────────────────────────────
 * Managed-Expo `expo-notifications` exposes title/body/data, NOT the native
 * `NotificationCompat.MessagingStyle` + `CarExtender.UnreadConversation`
 * builder that Android Auto actually reads. So this helper does two things:
 *   1. ships a best-effort `expo-notifications` notification today (visible on
 *      phone, carries the conversation payload), and
 *   2. emits the exact native field spec a small native module / mod must
 *      fill in to be Auto-readable (`androidAutoExtras`). Until that native
 *      piece exists, Tier 1 is "scaffolded + documented", not head-unit-live.
 *
 * Pure data + thin presenter so it unit-tests under tsx without RN.
 */

/** One turn in the coding-agent conversation thread. */
export interface CarConversationMessage {
  /** "you" = the driver's spoken command; "agent" = a status reply. */
  from: "you" | "agent";
  text: string;
  /** epoch ms */
  timestamp: number;
}

export interface CarConversation {
  /** Stable id so replies land back in the same thread / box. */
  conversationId: string;
  /** Display name of the contact — the agent / the box. e.g. "Yaver · magara". */
  contactName: string;
  messages: CarConversationMessage[];
}

/** Android-channel + reply wiring constants. */
export const CAR_NOTIFICATION_CHANNEL_ID = "yaver-car-agent";
export const CAR_REPLY_ACTION = "io.yaver.car.REPLY";
export const CAR_REPLY_REMOTE_INPUT_KEY = "yaver_car_reply";
/** DeviceEventEmitter event the native receiver emits with a captured reply. */
export const CAR_REPLY_EVENT = "yaverCarReply";

/** Shape of a captured RemoteInput reply forwarded from native → JS. */
export interface CarReplyEvent {
  conversationId: string;
  text: string;
}

/**
 * Subscribe to native car-reply (RemoteInput) events. Returns an unsubscribe
 * fn. No-ops (returns a noop unsubscribe) when react-native isn't present
 * (test/node) so callers can wire it unconditionally. The handler receives the
 * raw spoken/typed reply text; gating + dispatch is the caller's job
 * (see carReplyDispatch.ts).
 */
export function subscribeCarReplies(handler: (ev: CarReplyEvent) => void): () => void {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const { DeviceEventEmitter, NativeModules } = require("react-native");
    const mod = NativeModules?.YaverCarMessaging;
    if (typeof mod?.consumePendingReplies === "function") {
      void mod.consumePendingReplies().then((replies: CarReplyEvent[] | undefined) => {
        if (!Array.isArray(replies)) return;
        replies.forEach((ev) => {
          if (ev && typeof ev.text === "string") handler(ev);
        });
      }).catch(() => {});
    }
    if (!DeviceEventEmitter?.addListener) return () => {};
    const sub = DeviceEventEmitter.addListener(CAR_REPLY_EVENT, (ev: CarReplyEvent) => {
      if (ev && typeof ev.text === "string") handler(ev);
    });
    return () => {
      try { sub.remove(); } catch { /* ignore */ }
    };
  } catch {
    return () => {};
  }
}

/**
 * The native fields a MessagingStyle + CarExtender notification needs, which
 * `expo-notifications` cannot set on its own. A native module / config-plugin
 * mod consumes this to build the real `NotificationCompat.Builder`:
 *
 *   - MessagingStyle(person=contactName) with one addMessage(...) per message
 *   - CarExtender().setUnreadConversation(
 *       new UnreadConversationBuilder(contactName)
 *         .addMessage(latestAgentText)
 *         .setReplyAction(replyPendingIntent, remoteInput)
 *         .setReadPendingIntent(readPendingIntent)
 *         .setLatestTimestamp(latestTimestamp))
 *   - RemoteInput(CAR_REPLY_REMOTE_INPUT_KEY) on the reply action
 *   - category = Notification.CATEGORY_MESSAGE
 */
export interface AndroidAutoMessagingExtras {
  channelId: string;
  contactName: string;
  /** Each MessagingStyle message, oldest→newest. */
  messages: { text: string; timestamp: number; fromSelf: boolean }[];
  /** What the car should read aloud (the newest agent message). */
  unreadText: string;
  latestTimestamp: number;
  replyAction: string;
  remoteInputKey: string;
  category: "msg";
}

/** Build the native-extras spec for a conversation (consumed by the native side). */
export function buildAndroidAutoExtras(conv: CarConversation): AndroidAutoMessagingExtras {
  const sorted = [...conv.messages].sort((a, b) => a.timestamp - b.timestamp);
  const lastAgent = [...sorted].reverse().find((m) => m.from === "agent");
  return {
    channelId: CAR_NOTIFICATION_CHANNEL_ID,
    contactName: conv.contactName,
    messages: sorted.map((m) => ({
      text: m.text,
      timestamp: m.timestamp,
      fromSelf: m.from === "you",
    })),
    unreadText: lastAgent?.text ?? "",
    latestTimestamp: sorted.length ? sorted[sorted.length - 1].timestamp : Date.now(),
    replyAction: CAR_REPLY_ACTION,
    remoteInputKey: CAR_REPLY_REMOTE_INPUT_KEY,
    category: "msg",
  };
}

/** Best-effort phone-visible notification content (the part Expo CAN express). */
export interface CarNotificationContent {
  title: string;
  body: string;
  data: {
    conversationId: string;
    kind: "car-agent-message";
    androidAutoExtras: AndroidAutoMessagingExtras;
  };
  channelId: string;
}

export function buildCarNotificationContent(conv: CarConversation): CarNotificationContent {
  const extras = buildAndroidAutoExtras(conv);
  return {
    title: conv.contactName,
    body: extras.unreadText || "Working…",
    data: {
      conversationId: conv.conversationId,
      kind: "car-agent-message",
      androidAutoExtras: extras,
    },
    channelId: CAR_NOTIFICATION_CHANNEL_ID,
  };
}

/**
 * Try the native MessagingStyle delivery path (the `YaverCarMessaging` native
 * module injected by withAndroidAutoMessaging.js). This is the ONLY path
 * Android Auto can actually read aloud + reply to. Returns true if the native
 * module handled it, false if it's absent (managed Expo, iOS, test/node) so the
 * caller falls back to the best-effort expo-notifications path.
 *
 * The native module consumes the exact `buildAndroidAutoExtras(...)` shape.
 */
export async function presentCarConversationNative(conv: CarConversation): Promise<boolean> {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const { NativeModules, Platform } = require("react-native");
    if (Platform?.OS !== "android") return false;
    const mod = NativeModules?.YaverCarMessaging;
    if (!mod || typeof mod.presentConversation !== "function") return false;
    const extras = buildAndroidAutoExtras(conv);
    const ok = await mod.presentConversation(conv.conversationId, extras);
    return ok !== false;
  } catch {
    return false;
  }
}

/**
 * Present the conversation in the car. Prefers the native MessagingStyle module
 * (Android-Auto-readable); falls back to a best-effort expo-notifications
 * notification (see GAP above) when the native module isn't present. No-ops
 * cleanly in a plain test/node context. Returns true if EITHER path delivered.
 */
export async function presentCarConversation(conv: CarConversation): Promise<boolean> {
  if (await presentCarConversationNative(conv)) return true;
  const content = buildCarNotificationContent(conv);
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const Notifications = require("expo-notifications");
    if (typeof Notifications.setNotificationChannelAsync === "function") {
      await Notifications.setNotificationChannelAsync(CAR_NOTIFICATION_CHANNEL_ID, {
        name: "Car coding agent",
        importance: Notifications.AndroidImportance?.HIGH ?? 4,
      });
    }
    await Notifications.scheduleNotificationAsync({
      content: {
        title: content.title,
        body: content.body,
        data: content.data,
        // expo-notifications passes unknown keys through to the native payload;
        // a native MessagingStyle module reads data.androidAutoExtras.
      },
      trigger: null, // immediate
    });
    return true;
  } catch {
    return false;
  }
}
