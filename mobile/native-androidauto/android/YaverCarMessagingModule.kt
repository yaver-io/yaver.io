// YaverCarMessagingModule.kt — Tier 1 Android Auto MessagingStyle delivery.
//
// REFERENCE IMPLEMENTATION. NOT yet wired into the Android build by default —
// the Expo config plugin mobile/plugins/withAndroidAutoMessaging.js copies this
// (plus the package + receiver) into the app's source set and registers them
// when that plugin is activated in app.json. See
// docs/yaver-car-voice-coding.md §3 (Tier 1) + §7.1.
//
// Lives under mobile/native-androidauto/ (tracked) because `expo prebuild
// --clean` regenerates mobile/android; the config plugin is what survives the
// regeneration (it re-injects these sources every prebuild), mirroring
// native-mesh/YaverMeshVpnService.kt + withMeshTunnel.js.
//
// What it does: consumes the `androidAutoExtras` map produced by
// src/lib/carMessagingNotification.ts::buildAndroidAutoExtras(...) and posts a
// genuine `NotificationCompat.MessagingStyle` notification carrying a
// `CarExtender.UnreadConversation` with a `RemoteInput` reply action. That is
// the shape Android Auto actually reads aloud and offers a voice reply for —
// the part managed `expo-notifications` cannot express.
//
// The reply RemoteInput fires YaverCarReplyReceiver, which re-emits the spoken
// reply text back into JS as a "yaverCarReply" device event. The JS side
// (src/lib/carReplyDispatch.ts) gates it (confirm risky verbs) and dispatches
// it into the coding pipeline.

package io.yaver.mobile.car

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import androidx.core.app.RemoteInput
import org.json.JSONArray
import org.json.JSONObject
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.bridge.WritableArray
import com.facebook.react.bridge.WritableMap
import com.facebook.react.bridge.Arguments
import com.facebook.react.modules.core.DeviceEventManagerModule

class YaverCarMessagingModule(
    private val reactContext: ReactApplicationContext,
) : ReactContextBaseJavaModule(reactContext) {

    override fun getName() = "YaverCarMessaging"

    /** Lets JS check whether the native MessagingStyle path is present so
     *  carMessagingNotification.ts can fall back to expo-notifications when
     *  this module isn't compiled in. */
    @ReactMethod
    fun isAvailable(promise: com.facebook.react.bridge.Promise) {
        promise.resolve(true)
    }

    /**
     * Drain RemoteInput replies captured while the JS bridge/screen was not
     * listening yet. Android Auto can deliver a broadcast while the app process
     * is cold or before the Car Voice screen has mounted; dropping that spoken
     * command makes the head-unit path feel broken.
     */
    @ReactMethod
    fun consumePendingReplies(promise: com.facebook.react.bridge.Promise) {
        try {
            promise.resolve(drainPendingReplies(reactContext.applicationContext))
        } catch (e: Exception) {
            promise.reject("car_reply_drain_failed", e.message, e)
        }
    }

    /**
     * Post a MessagingStyle + CarExtender + RemoteInput notification.
     * `extras` is the JS AndroidAutoMessagingExtras object verbatim:
     *   { channelId, contactName, messages:[{text,timestamp,fromSelf}],
     *     unreadText, latestTimestamp, replyAction, remoteInputKey, category }
     * `conversationId` keys replies back to the right thread/box.
     */
    @ReactMethod
    fun presentConversation(
        conversationId: String,
        extras: ReadableMap,
        promise: com.facebook.react.bridge.Promise,
    ) {
        try {
            val ctx = reactContext.applicationContext
            val channelId = extras.getString("channelId") ?: CHANNEL_ID
            val contactName = extras.getString("contactName") ?: "Yaver"
            val unreadText = extras.getString("unreadText") ?: ""
            val latestTimestamp =
                if (extras.hasKey("latestTimestamp")) extras.getDouble("latestTimestamp").toLong()
                else System.currentTimeMillis()
            val replyAction = extras.getString("replyAction") ?: REPLY_ACTION
            val remoteInputKey = extras.getString("remoteInputKey") ?: REMOTE_INPUT_KEY

            ensureChannel(ctx, channelId)

            val self = Person.Builder().setName("You").build()
            val agent = Person.Builder().setName(contactName).build()

            val style = NotificationCompat.MessagingStyle(self)
                .setConversationTitle(contactName)
            val msgs = extras.getArray("messages")
            if (msgs != null) {
                for (i in 0 until msgs.size()) {
                    val m = msgs.getMap(i) ?: continue
                    val text = m.getString("text") ?: ""
                    val ts = if (m.hasKey("timestamp")) m.getDouble("timestamp").toLong() else latestTimestamp
                    val fromSelf = m.hasKey("fromSelf") && m.getBoolean("fromSelf")
                    style.addMessage(text, ts, if (fromSelf) null else agent)
                }
            }

            // RemoteInput: the voice/text reply the car captures.
            val remoteInput = RemoteInput.Builder(remoteInputKey)
                .setLabel("Reply")
                .build()

            // PendingIntent → YaverCarReplyReceiver (re-emits to JS).
            val replyIntent = Intent(ctx, YaverCarReplyReceiver::class.java).apply {
                action = replyAction
                putExtra(EXTRA_CONVERSATION_ID, conversationId)
                putExtra(EXTRA_REMOTE_INPUT_KEY, remoteInputKey)
            }
            val piFlags =
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S)
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
                else PendingIntent.FLAG_UPDATE_CURRENT
            val replyPending = PendingIntent.getBroadcast(
                ctx, conversationId.hashCode(), replyIntent, piFlags,
            )

            val replyActionCompat = NotificationCompat.Action.Builder(
                android.R.drawable.ic_menu_send, "Reply", replyPending,
            )
                .addRemoteInput(remoteInput)
                .setSemanticAction(NotificationCompat.Action.SEMANTIC_ACTION_REPLY)
                .setShowsUserInterface(false)
                .build()

            // "Mark read" PendingIntent (CarExtender needs one).
            val readIntent = Intent(ctx, YaverCarReplyReceiver::class.java).apply {
                action = "$replyAction.READ"
                putExtra(EXTRA_CONVERSATION_ID, conversationId)
            }
            val readPending = PendingIntent.getBroadcast(
                ctx, conversationId.hashCode() + 1, readIntent,
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S)
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
                else PendingIntent.FLAG_UPDATE_CURRENT,
            )

            // CarExtender.UnreadConversation — what Android Auto reads aloud.
            val unread = NotificationCompat.CarExtender.UnreadConversation
                .Builder(contactName)
                .addMessage(if (unreadText.isNotEmpty()) unreadText else "Working…")
                .setReplyAction(replyPending, remoteInput)
                .setReadPendingIntent(readPending)
                .setLatestTimestamp(latestTimestamp)
                .build()
            val carExtender = NotificationCompat.CarExtender().setUnreadConversation(unread)

            val notif = NotificationCompat.Builder(ctx, channelId)
                .setSmallIcon(ctx.applicationInfo.icon)
                .setStyle(style)
                .setCategory(NotificationCompat.CATEGORY_MESSAGE)
                .addAction(replyActionCompat)
                .extend(carExtender)
                .setShowWhen(true)
                .setWhen(latestTimestamp)
                .setAutoCancel(false)
                .build()

            NotificationManagerCompat.from(ctx)
                .notify(conversationId.hashCode(), notif)
            promise.resolve(true)
        } catch (e: Exception) {
            promise.reject("car_messaging_failed", e.message, e)
        }
    }

    private fun ensureChannel(ctx: Context, channelId: String) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            if (nm.getNotificationChannel(channelId) == null) {
                nm.createNotificationChannel(
                    NotificationChannel(
                        channelId,
                        "Car coding agent",
                        NotificationManager.IMPORTANCE_HIGH,
                    ),
                )
            }
        }
    }

    companion object {
        const val CHANNEL_ID = "yaver-car-agent"
        const val REPLY_ACTION = "io.yaver.car.REPLY"
        const val REMOTE_INPUT_KEY = "yaver_car_reply"
        const val EXTRA_CONVERSATION_ID = "yaver_conversation_id"
        const val EXTRA_REMOTE_INPUT_KEY = "yaver_remote_input_key"
        const val EVENT_NAME = "yaverCarReply"
        private const val PREFS = "yaver_car_replies"
        private const val PREF_PENDING = "pending"

        /** Re-emit a captured reply into JS. Called by the receiver while the
         *  RN context is alive; if it's not, the reply is dropped (the car
         *  notification is best-effort and the driver can retry). */
        fun emitReply(reactContext: ReactApplicationContext?, conversationId: String, text: String) {
            val rc = reactContext ?: return
            if (!rc.hasActiveReactInstance()) return
            val payload: WritableMap = Arguments.createMap().apply {
                putString("conversationId", conversationId)
                putString("text", text)
            }
            rc.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
                .emit(EVENT_NAME, payload)
        }

        fun storePendingReply(ctx: Context, conversationId: String, text: String) {
            val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            val arr = JSONArray(prefs.getString(PREF_PENDING, "[]") ?: "[]")
            arr.put(JSONObject().apply {
                put("conversationId", conversationId)
                put("text", text)
                put("timestamp", System.currentTimeMillis())
            })
            prefs.edit().putString(PREF_PENDING, arr.toString()).apply()
        }

        fun drainPendingReplies(ctx: Context): WritableArray {
            val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            val raw = prefs.getString(PREF_PENDING, "[]") ?: "[]"
            prefs.edit().remove(PREF_PENDING).apply()
            val out = Arguments.createArray()
            val arr = JSONArray(raw)
            for (i in 0 until arr.length()) {
                val obj = arr.optJSONObject(i) ?: continue
                out.pushMap(Arguments.createMap().apply {
                    putString("conversationId", obj.optString("conversationId"))
                    putString("text", obj.optString("text"))
                    putDouble("timestamp", obj.optDouble("timestamp", 0.0))
                })
            }
            return out
        }
    }
}

/**
 * Receives the RemoteInput reply PendingIntent and forwards the captured text
 * to JS via YaverCarMessagingModule.emitReply. Registered (exported=false) in
 * the manifest by withAndroidAutoMessaging.js.
 */
class YaverCarReplyReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action?.endsWith(".READ") == true) return // mark-read: nothing to do
        val results = RemoteInput.getResultsFromIntent(intent) ?: return
        val key = intent.getStringExtra(YaverCarMessagingModule.EXTRA_REMOTE_INPUT_KEY)
            ?: YaverCarMessagingModule.REMOTE_INPUT_KEY
        val text = results.getCharSequence(key)?.toString()?.trim().orEmpty()
        if (text.isEmpty()) return
        val conversationId =
            intent.getStringExtra(YaverCarMessagingModule.EXTRA_CONVERSATION_ID).orEmpty()

        // Reach the live RN context to emit the event. The application is the
        // ReactApplication host that owns the active ReactApplicationContext.
        val app = context.applicationContext
        val reactContext = runCatching {
            val host = (app as? com.facebook.react.ReactApplication)?.reactNativeHost
            host?.reactInstanceManager?.currentReactContext as? ReactApplicationContext
        }.getOrNull()
        if (reactContext?.hasActiveReactInstance() == true) {
            YaverCarMessagingModule.emitReply(reactContext, conversationId, text)
        } else {
            YaverCarMessagingModule.storePendingReply(context.applicationContext, conversationId, text)
        }
    }
}
