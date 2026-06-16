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
// NOT built until plugins/withWatchBridge.js is registered (same posture as
// the mesh tunnel). When the app is backgrounded, delivering an inbound
// message to JS needs a HeadlessJsTask — noted as the activation gap below.

package io.yaver.mobile.wear

import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.modules.core.DeviceEventManagerModule
import com.google.android.gms.wearable.Wearable

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

  /** Called by the listener service (or a HeadlessJsTask) with an inbound
   *  PATH_TURN payload. Emits it to JS if a context is live. */
  fun emitInbound(json: String) {
    if (!reactContext.hasActiveReactInstance()) return
    reactContext
      .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
      .emit("yaverWatchMessage", json)
  }

  companion object {
    // Weak-ish singleton so the WearableListenerService can reach a live
    // module. When the app is fully backgrounded this is null — that case
    // wants a HeadlessJsTaskService (TODO activation).
    @Volatile
    var instance: YaverWearBridgeModule? = null
  }
}
