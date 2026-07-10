package io.yaver.wear

import android.app.Application

/**
 * Application class for the Yaver Wear OS app.
 *
 * Deliberately minimal: the watch owns nothing. There is no agent, no chromedp,
 * no coding loop here — those live on the paired phone (default) or the remote
 * runner (standalone). This class just exists so the process has a stable
 * Application object the Data Layer services and UI can hang off of, and so we
 * have one place to do app-wide init if it's ever needed.
 *
 * The one piece of app-wide init we DO need: [Speech.init], so the TextToSpeech
 * engine is warmed up before any reply arrives from [ReplyListenerService]
 * (which has no Activity and can't lazy-init TTS itself).
 */
class YaverWearApp : Application() {
    override fun onCreate() {
        super.onCreate()
        Speech.init(this)
    }
}
