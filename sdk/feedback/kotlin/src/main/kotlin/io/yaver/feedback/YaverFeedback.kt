package io.yaver.feedback

import android.app.Activity
import android.app.Application
import android.content.Context
import android.graphics.Bitmap
import android.os.Build
import android.util.Base64
import android.util.Log
import android.view.View
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicReference

/**
 * Yaver Feedback SDK for native Android.
 *
 * ─── What this closes ───────────────────────────────────────────────────────
 *
 * Until this existed there was NO native Kotlin feedback SDK — the RN, Flutter,
 * Web and Unity SDKs shipped, and native Android was viewer-triggered only (a
 * `launch-feedback` control message pushed down the WebRTC events channel from
 * the Yaver viewer). That still works and remains the fallback; this adds the
 * in-app path so a Kotlin-only app has the same loop as everyone else.
 *
 * ─── Why there is no container-suppression logic here ───────────────────────
 *
 * The RN SDK must check `YaverInfo.isYaver` at runtime and disable its own
 * shake handler when loaded inside the Yaver container, because there the
 * CONTAINER owns shake (Reload / Back to Yaver) and two overlays would fight.
 *
 * A native Android app is never in that position. The container loads a HERMES
 * BYTECODE BUNDLE — React Native only. There is no mechanism by which a Kotlin
 * app runs inside it. So this SDK always owns shake, in both contexts that
 * exist:
 *
 *   - standalone (Play / sideload / `yaver wire push`)
 *   - streamed from a Cloud Workspace via Redroid, where the box injects a
 *     hardware shake into the emulator and this overlay fires INSIDE the
 *     stream. The phone's exit affordance lives in native viewer chrome outside
 *     the video, so they cannot collide.
 *
 * Adding suppression logic "for symmetry" would be strictly wrong: it would
 * disable feedback in the one case it is needed.
 */
object YaverFeedback {

    private const val TAG = "YaverFeedback"

    private val config = AtomicReference<FeedbackConfig?>(null)
    private val currentActivity = AtomicReference<Activity?>(null)
    private var shakeDetector: ShakeDetector? = null
    private val timeline = mutableListOf<TimelineEvent>()
    private val errors = mutableListOf<CapturedError>()
    private val startedAt = System.currentTimeMillis()

    // Single-threaded: submission is rare and ordering keeps reports readable.
    private val io = Executors.newSingleThreadExecutor { r ->
        Thread(r, "yaver-feedback").apply { isDaemon = true }
    }

    /**
     * Initialise. Safe to call twice; the second call replaces the config.
     *
     * Guard this behind a debug check in your app. The SDK does not guard
     * itself, because "is this a release build" is the host app's question to
     * answer, not ours — and a wrong guess either way is worse than the caller
     * being explicit.
     */
    @JvmStatic
    fun init(context: Context, config: FeedbackConfig) {
        require(config.agentUrl.isNotBlank()) { "agentUrl is required" }
        require(config.authToken.isNotBlank()) {
            "authToken is required — use an SDK token, never a user session token"
        }
        this.config.set(config)

        (context.applicationContext as? Application)?.let { registerLifecycle(it) }

        if (config.captureErrors) installErrorHandler()

        if (config.shakeEnabled) {
            shakeDetector?.stop()
            shakeDetector = ShakeDetector(
                context.applicationContext,
                config.shakeThresholdG,
            ) { open() }.also { it.start() }
        }
    }

    /** Stop listening. Call from onTerminate or a debug toggle. */
    @JvmStatic
    fun stop() {
        shakeDetector?.stop()
        shakeDetector = null
    }

    /** Add a timestamped annotation to the next report. */
    @JvmStatic
    fun mark(type: String, text: String? = null) {
        synchronized(timeline) {
            timeline.add(
                TimelineEvent(
                    time = (System.currentTimeMillis() - startedAt) / 1000.0,
                    type = type,
                    text = text,
                )
            )
        }
    }

    /**
     * Capture and submit a report now.
     *
     * Fire-and-forget by design: a feedback SDK must never block the UI thread
     * or surface a network error to the user. If submission fails it is logged
     * and dropped — the user's app is not the place to debug our transport.
     */
    @JvmStatic
    @JvmOverloads
    fun open(note: String? = null) {
        val cfg = config.get() ?: run {
            Log.w(TAG, "open() before init() — ignoring")
            return
        }
        val activity = currentActivity.get()
        val shot = if (cfg.captureScreenshot) captureScreenshot(activity) else null
        io.execute { submit(cfg, buildReport(activity, shot, note)) }
    }

    private fun buildReport(activity: Activity?, screenshot: String?, note: String?): FeedbackReport {
        val metrics = activity?.resources?.displayMetrics
        val snapshotTimeline: List<TimelineEvent>
        val snapshotErrors: List<CapturedError>
        synchronized(timeline) { snapshotTimeline = timeline.toList() }
        synchronized(errors) { snapshotErrors = errors.toList() }

        return FeedbackReport(
            id = UUID.randomUUID().toString(),
            screenshots = listOfNotNull(screenshot),
            timeline = snapshotTimeline,
            errors = snapshotErrors,
            deviceInfo = DeviceInfo(
                platform = "android",
                osVersion = Build.VERSION.RELEASE ?: "unknown",
                model = "${Build.MANUFACTURER} ${Build.MODEL}",
                screenWidth = metrics?.widthPixels ?: 0,
                screenHeight = metrics?.heightPixels ?: 0,
            ),
            note = note,
            createdAt = iso8601(Date()),
        )
    }

    private fun submit(cfg: FeedbackConfig, report: FeedbackReport) {
        try {
            val url = URL(cfg.agentUrl.trimEnd('/') + "/feedback")
            (url.openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"
                doOutput = true
                connectTimeout = 10_000
                readTimeout = 15_000
                setRequestProperty("Content-Type", "application/json")
                setRequestProperty("Authorization", "Bearer ${cfg.authToken}")
                outputStream.use { it.write(report.toJson().toString().toByteArray()) }
                val code = responseCode
                if (code !in 200..299) {
                    // Name the status. "Feedback failed" without one costs a
                    // session; 401 vs 404 vs 500 are three different fixes.
                    Log.w(TAG, "feedback rejected: HTTP $code (check the SDK token and agentUrl)")
                } else {
                    synchronized(timeline) { timeline.clear() }
                    synchronized(errors) { errors.clear() }
                }
                disconnect()
            }
        } catch (e: Exception) {
            Log.w(TAG, "feedback submit failed: ${e.message}")
        }
    }

    private fun captureScreenshot(activity: Activity?): String? {
        val view: View = activity?.window?.decorView?.rootView ?: return null
        return try {
            // drawingCache is deprecated but is the only approach that works
            // without PixelCopy's API 26 floor and a handler round-trip. The
            // SDK targets a wide range of host apps, so breadth wins here.
            val bitmap = Bitmap.createBitmap(view.width, view.height, Bitmap.Config.ARGB_8888)
            val canvas = android.graphics.Canvas(bitmap)
            view.draw(canvas)
            ByteArrayOutputStream().use { out ->
                // 60% JPEG: a bug report needs to be legible, not archival, and
                // the payload crosses a phone network.
                bitmap.compress(Bitmap.CompressFormat.JPEG, 60, out)
                "data:image/jpeg;base64," + Base64.encodeToString(out.toByteArray(), Base64.NO_WRAP)
            }
        } catch (e: Throwable) {
            Log.w(TAG, "screenshot failed: ${e.message}")
            null
        }
    }

    private fun installErrorHandler() {
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            synchronized(errors) {
                errors.add(
                    CapturedError(
                        message = throwable.message ?: throwable.javaClass.name,
                        stack = throwable.stackTraceToString().take(4000),
                        timestamp = iso8601(Date()),
                    )
                )
            }
            // ALWAYS chain. Swallowing the host app's crash handler would break
            // their Crashlytics/Sentry reporting — a feedback SDK that costs
            // someone their crash telemetry is a net loss, however good its own
            // reports are.
            previous?.uncaughtException(thread, throwable)
        }
    }

    private fun registerLifecycle(app: Application) {
        app.registerActivityLifecycleCallbacks(object : Application.ActivityLifecycleCallbacks {
            override fun onActivityResumed(activity: Activity) { currentActivity.set(activity) }
            override fun onActivityPaused(activity: Activity) {
                if (currentActivity.get() === activity) currentActivity.set(null)
            }
            override fun onActivityCreated(a: Activity, b: android.os.Bundle?) {}
            override fun onActivityStarted(a: Activity) {}
            override fun onActivityStopped(a: Activity) {}
            override fun onActivitySaveInstanceState(a: Activity, b: android.os.Bundle) {}
            override fun onActivityDestroyed(a: Activity) {}
        })
    }

    private fun iso8601(date: Date): String =
        SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
            timeZone = TimeZone.getTimeZone("UTC")
        }.format(date)
}
