// YaverWearListenerService.kt — receives Wear Data Layer messages from the
// watch on the PHONE. Registered in the manifest by withWatchBridge.js with an
// intent-filter for PATH_TURN. Runs even when the app isn't foregrounded, so
// it's the reliable inbound edge.
//
// It forwards the JSON to the live RN module (YaverWearBridgeModule.instance),
// which emits it to JS (watchEntry.ts). If no React instance is alive (app
// fully killed), the turn is stored in SharedPreferences and drained when the
// JS bridge next mounts (consumePendingTurns). This mirrors the car surface's
// consumePendingReplies pattern — no more dropped head-unit commands.

package io.yaver.mobile.wear

import com.google.android.gms.wearable.MessageEvent
import com.google.android.gms.wearable.WearableListenerService

class YaverWearListenerService : WearableListenerService() {
  override fun onMessageReceived(event: MessageEvent) {
    if (event.path != PATH_TURN) return
    val json = String(event.data, Charsets.UTF_8)
    val module = YaverWearBridgeModule.instance
    if (module != null) {
      module.emitInbound(json)
    } else {
      // No live React instance — persist the turn so it's drained when the
      // JS bridge mounts (consumePendingTurns). Nothing is dropped.
      YaverWearBridgeModule.storePendingTurn(applicationContext, json)
    }
  }
}
