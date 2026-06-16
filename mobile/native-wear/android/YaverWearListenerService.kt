// YaverWearListenerService.kt — receives Wear Data Layer messages from the
// watch on the PHONE. Registered in the manifest by withWatchBridge.js with an
// intent-filter for PATH_TURN. Runs even when the app isn't foregrounded, so
// it's the reliable inbound edge.
//
// It forwards the JSON to the live RN module (YaverWearBridgeModule.instance),
// which emits it to JS (watchEntry.ts). If no React instance is alive (app
// fully killed), the correct activation is a HeadlessJsTaskService that spins
// up JS to handle the turn — left as a TODO so the scaffold stays honest.

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
      // TODO(activation): no live React instance — start a HeadlessJsTask
      // that loads watchEntry.ts, calls deliver(json), and lets the JS
      // sender ship replies back via MessageClient(PATH_REPLY). Until then,
      // inbound turns require the app to have been opened at least once this
      // process lifetime.
    }
  }
}
