package io.yaver.wear

import com.google.android.gms.wearable.MessageEvent
import com.google.android.gms.wearable.WearableListenerService

/**
 * Receives Phone → Watch replies on [WatchProtocol.PATH_REPLY] over the Wear
 * Data Layer and routes them into [WatchState] (which the Compose UI collects).
 *
 * This is a WearableListenerService so it is delivered EVEN IF the watch UI is
 * not foregrounded — that's the whole "wake on completion" story: the phone runs
 * the async task, and when it finishes it pushes a `summary` here, which fires a
 * haptic and updates the line. The watch never polls.
 *
 * Registered in AndroidManifest.xml scoped to the /yaver/watch/reply path so we
 * don't wake on unrelated Data Layer traffic.
 */
class ReplyListenerService : WearableListenerService() {

    override fun onMessageReceived(event: MessageEvent) {
        if (event.path != WatchProtocol.PATH_REPLY) return

        val json = WatchProtocol.text(event.data)
        val reply = WatchProtocol.parseReply(json) // never throws

        // Update shared UI state (the foreground Compose UI collects this).
        WatchState.applyReply(reply)

        // Fire the matching haptic so the wrist feels the result even if the
        // screen is off / the app is backgrounded. Reuses the centralized cue
        // policy + effect patterns from WatchState/Haptics (single source).
        Haptics(applicationContext).fire(WatchState.hapticFor(reply))
    }
}
