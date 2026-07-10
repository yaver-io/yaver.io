// YaverWearBridgeModule.kt — the PHONE-side Wear Data Layer bridge for the
// Wear OS companion (docs/yaver-smartwatch-voice-terminal.md §3 mode A).
//
// Counterpart of the iOS YaverWatchBridge. Transport only — every decision
// (guards, confirm, dispatch, summarize) lives in JS (src/lib/watchEntry.ts →
// watchBridge.ts). Two jobs:
//
//   inbound  — YaverWearListenerService receives a message on PATH_TURN from
//              the watch and hands the JSON here; we emit it to JS via the
//              "yaverWatchMessage" DeviceEvent.
//   outbound — sendToWatch(json) (called by JS) ships a reply to the watch
//              over MessageClient on PATH_REPLY (delivered to all connected
//              nodes — typically the single paired watch).
//
// Paths are pinned to match wear/app/.../WatchProtocol.kt:
//   PATH_TURN  = "/yaver/watch/turn"   (watch → phone)
//   PATH_REPLY = "/yaver/watch/reply"  (phone → watch)
//
// Dead-process activation: when the phone app's process is dead, inbound
// Wear Data Layer messages are stored in SharedPreferences and drained when
// the JS bridge next mounts (consumePendingTurns). This mirrors the car
// surface's consumePendingReplies pattern — simpler than HeadlessJsTaskService
// and proven in this codebase. The user experience: if the phone is dead, the
// turn is queued and processed when the user next opens the app.

package io.yaver.mobile.wear

import android.content.Context
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableArray
import com.facebook.react.bridge.Arguments
import com.facebook.react.modules.core.DeviceEventManagerModule
import com.google.android.gms.wearable.Wearable
import org.json.JSONArray
import org.json.JSONObject

const val PATH_TURN = "/yaver/watch/turn"
const val PATH_REPLY = "/yaver/watch/reply"

class YaverWearBridgeModule(
  private val reactContext: ReactApplicationContext,
) : ReactContextBaseJavaModule(reactContext) {

  init {
    instance = this
  }

  override fun getName(): String = "YaverWatchBridge"

  /** JS → native: send a reply JSON to every connected Wear node on PATH_REPLY. */
  @ReactMethod
  fun sendToWatch(json: String) {
    val bytes = json.toByteArray(Charsets.UTF_8)
    val nodeClient = Wearable.getNodeClient(reactContext)
    val messageClient = Wearable.getMessageClient(reactContext)
    nodeClient.connectedNodes.addOnSuccessListener { nodes ->
      for (node in nodes) {
        messageClient.sendMessage(node.id, PATH_REPLY, bytes)
      }
    }
  }

  /** Required by RCTEventEmitter contract on the JS side; no-op here. */
  @ReactMethod fun addListener(eventName: String) {}
  @ReactMethod fun removeListeners(count: Int) {}

  /**
   * Drain Wear Data Layer turns captured while the JS bridge was not
   * listening yet. The watch can send a turn while the app process is cold
   * or before WatchBridgeHost has mounted; dropping that spoken command
   * makes the wrist feel broken. Mirrors YaverCarMessagingModule's
   * consumePendingReplies.
   */
  @ReactMethod
  fun consumePendingTurns(promise: com.facebook.react.bridge.Promise) {
    try {
      promise.resolve(drainPendingTurns(reactContext.applicationContext))
    } catch (e: Exception) {
      promise.reject("wear_turn_drain_failed", e.message, e)
    }
  }

  /** Called by the listener service with an inbound PATH_TURN payload.
   *  Emits it to JS if a context is live. */
  fun emitInbound(json: String) {
    if (!reactContext.hasActiveReactInstance()) return
    reactContext
      .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
      .emit("yaverWatchMessage", json)
  }

  companion object {
    // Weak-ish singleton so the WearableListenerService can reach a live
    // module. When the app is fully backgrounded this is null — inbound turns
    // are stored in SharedPreferences and drained on next mount.
    @Volatile
    var instance: YaverWearBridgeModule? = null

    private const val PREFS = "yaver_wear_turns"
    private const val PREF_PENDING = "pending"

    /** Store a turn JSON when the RN context is dead. Called by the listener
     *  service so nothing is dropped while the app process is cold. */
    fun storePendingTurn(ctx: Context, json: String) {
      val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
      val arr = JSONArray(prefs.getString(PREF_PENDING, "[]") ?: "[]")
      arr.put(json)
      prefs.edit().putString(PREF_PENDING, arr.toString()).apply()
    }

    /** Pop + return all pending turn JSON strings. Called by consumePendingTurns
     *  when the JS bridge mounts. */
    fun drainPendingTurns(ctx: Context): WritableArray {
      val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
      val raw = prefs.getString(PREF_PENDING, "[]") ?: "[]"
      prefs.edit().remove(PREF_PENDING).apply()
      val out = Arguments.createArray()
      val arr = JSONArray(raw)
      for (i in 0 until arr.length()) {
        val s = arr.optString(i, "")
        if (s.isNotEmpty()) out.pushString(s)
      }
      return out
    }
  }
}
