package io.yaver.mobile

// YaverScreenRecorderModule — Android counterpart to the iOS
// ScreenRecorder native module. Exposed to React Native as
// `NativeModules.ScreenRecorder` with the same JS surface
// (startRecording / stopRecording / isRecordingActive). Used by
// the vibe-preview phone-side capture path: developer's phone
// records itself, then the JS side uploads the MP4 to the agent
// via /vibing/preview/clip/upload.
//
// Pipeline:
//   startRecording() →
//     1. createScreenCaptureIntent → startActivityForResult
//     2. onActivityResult populates the MediaProjection
//     3. configure MediaRecorder (H264 + AAC, MP4 container)
//     4. createVirtualDisplay backed by the recorder's surface
//     5. recorder.start()
//   stopRecording() →
//     1. recorder.stop() / release()
//     2. virtualDisplay.release()
//     3. mediaProjection.stop()
//     4. resolve with the on-disk MP4 path

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.MediaRecorder
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.util.DisplayMetrics
import android.view.WindowManager
import com.facebook.react.bridge.ActivityEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import java.io.File

class YaverScreenRecorderModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx), ActivityEventListener {

  companion object {
    private const val MODULE_NAME = "ScreenRecorder"
    private const val REQUEST_CODE_CAPTURE = 0xCA
    // 8 Mbps is enough for 720p screen content; below this RN red-box
    // text gets blocky, above it the upload size grows quickly.
    private const val VIDEO_BITRATE = 8_000_000
    private const val VIDEO_FPS = 30
    private const val AUDIO_BITRATE = 64_000
  }

  private var pendingPromise: Promise? = null
  private var mediaRecorder: MediaRecorder? = null
  private var mediaProjection: MediaProjection? = null
  private var virtualDisplay: VirtualDisplay? = null
  private var outputPath: String? = null
  private var isRecording: Boolean = false

  init {
    ctx.addActivityEventListener(this)
  }

  override fun getName(): String = MODULE_NAME

  // ─── JS-facing methods ─────────────────────────────────────────────────────

  @ReactMethod
  fun startRecording(promise: Promise) {
    if (isRecording) {
      promise.reject("ALREADY_RECORDING", "Already recording")
      return
    }
    val activity = currentActivity ?: run {
      promise.reject("NO_ACTIVITY", "Cannot start screen recording without a foreground activity")
      return
    }
    val mgr = ctx.getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager?
    if (mgr == null) {
      promise.reject("UNAVAILABLE", "MediaProjectionManager not available on this device")
      return
    }
    pendingPromise = promise
    activity.startActivityForResult(mgr.createScreenCaptureIntent(), REQUEST_CODE_CAPTURE)
  }

  @ReactMethod
  fun stopRecording(promise: Promise) {
    if (!isRecording) {
      promise.reject("NOT_RECORDING", "Not currently recording")
      return
    }
    try {
      mediaRecorder?.stop()
    } catch (e: RuntimeException) {
      // MediaRecorder.stop() throws if no frames were captured. The
      // file will be empty/garbage in that case; surface as failure
      // but still tear down the resources below.
      cleanup()
      promise.reject("STOP_FAILED", "Recorder produced no output: ${e.message}", e)
      return
    }
    cleanup()
    promise.resolve(outputPath)
  }

  @ReactMethod
  fun isRecordingActive(promise: Promise) {
    promise.resolve(isRecording)
  }

  // ─── ActivityEventListener — receives MediaProjection grant ─────────────────

  override fun onActivityResult(activity: Activity?, requestCode: Int, resultCode: Int, data: Intent?) {
    if (requestCode != REQUEST_CODE_CAPTURE) return
    val promise = pendingPromise ?: return
    pendingPromise = null

    if (resultCode != Activity.RESULT_OK || data == null) {
      promise.reject("USER_DENIED", "Screen capture permission was denied")
      return
    }
    val mgr = ctx.getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager?
    if (mgr == null) {
      promise.reject("UNAVAILABLE", "MediaProjectionManager disappeared")
      return
    }
    try {
      mediaProjection = mgr.getMediaProjection(resultCode, data)
      configureRecorder()
      virtualDisplay = mediaProjection!!.createVirtualDisplay(
          "yaver-vibe-preview",
          screenWidth(), screenHeight(), screenDensity(),
          DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
          mediaRecorder!!.surface,
          null, null
      )
      mediaRecorder!!.start()
      isRecording = true
      promise.resolve(true)
    } catch (e: Exception) {
      cleanup()
      promise.reject("START_FAILED", e.message ?: "could not start recorder", e)
    }
  }

  override fun onNewIntent(intent: Intent?) { /* no-op */ }

  // ─── Internal helpers ──────────────────────────────────────────────────────

  private fun configureRecorder() {
    val cacheDir = File(ctx.cacheDir, "yaver-vibe-preview")
    cacheDir.mkdirs()
    val path = File(cacheDir, "vibe-${System.currentTimeMillis()}.mp4").absolutePath
    outputPath = path

    val recorder =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(ctx)
        else @Suppress("DEPRECATION") MediaRecorder()

    // Order matters; MediaRecorder API is stateful.
    recorder.setVideoSource(MediaRecorder.VideoSource.SURFACE)
    recorder.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
    recorder.setVideoEncoder(MediaRecorder.VideoEncoder.H264)
    recorder.setVideoSize(screenWidth(), screenHeight())
    recorder.setVideoFrameRate(VIDEO_FPS)
    recorder.setVideoEncodingBitRate(VIDEO_BITRATE)
    recorder.setOutputFile(path)
    recorder.prepare()
    mediaRecorder = recorder
  }

  private fun cleanup() {
    isRecording = false
    try { mediaRecorder?.reset() } catch (_: Exception) {}
    try { mediaRecorder?.release() } catch (_: Exception) {}
    mediaRecorder = null
    try { virtualDisplay?.release() } catch (_: Exception) {}
    virtualDisplay = null
    try { mediaProjection?.stop() } catch (_: Exception) {}
    mediaProjection = null
  }

  private fun screenWidth(): Int = displayMetrics().widthPixels
  private fun screenHeight(): Int = displayMetrics().heightPixels
  private fun screenDensity(): Int = displayMetrics().densityDpi

  private fun displayMetrics(): DisplayMetrics {
    val wm = ctx.getSystemService(Context.WINDOW_SERVICE) as WindowManager
    val metrics = DisplayMetrics()
    @Suppress("DEPRECATION")
    wm.defaultDisplay.getRealMetrics(metrics)
    return metrics
  }
}
