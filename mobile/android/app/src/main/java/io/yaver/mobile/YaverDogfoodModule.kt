package io.yaver.mobile

// Android counterpart of mobile/ios/Yaver/YaverDogfood.swift — host-side
// screenshot auto-catch for the "improve Yaver with Yaver" dogfood loop. When
// dogfood mode is on, JS calls start(); from then on, every time the user takes
// a screenshot anywhere in Yaver we re-render the activity's decorView to a JPEG
// and emit `onDogfoodScreenshot` so the Dogfood thread can stage it for
// annotation + prompt + dispatch.
//
// Why re-render the window instead of reading the saved screenshot file: same
// reasoning as iOS — re-rendering needs no Photos/media access for the bitmap
// itself, is instant, captures the exact app UI, and keeps the image on-device
// for the JS redact pass (screenshots are P2P-only, never Convex).
//
// Detection has two tiers:
//   • Android 14+ (API 34): Activity.registerScreenCaptureCallback — the OS
//     tells us the moment a screenshot is taken. Needs only the normal
//     DETECT_SCREEN_CAPTURE permission (auto-granted, no runtime prompt).
//   • Android <14: a ContentObserver on MediaStore images. When a new image
//     lands we check whether it looks like a fresh screenshot and, if so,
//     re-render the decorView. This path only arms when a media-read permission
//     is already granted; otherwise it stays inert and the JS UI degrades to
//     the manual "+ add screenshot" path (isDogfoodCaptureAvailable() handles
//     the toggle).
//
// JS contract (mobile/src/lib/dogfoodCapture.ts):
//   const emitter = new NativeEventEmitter(NativeModules.YaverDogfood)
//   emitter.addListener("onDogfoodScreenshot", (ev) => stage(ev))  // {path, takenAt, route}
//   await NativeModules.YaverDogfood.start()
//   …
//   await NativeModules.YaverDogfood.stop()

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.pm.PackageManager
import android.database.ContentObserver
import android.graphics.Bitmap
import android.graphics.Canvas
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import android.util.Log
import androidx.annotation.RequiresApi
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.modules.core.DeviceEventManagerModule
import java.io.File
import java.io.FileOutputStream

class YaverDogfoodModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx) {

  override fun getName(): String = NAME

  private var observing = false
  /** Current expo-router path, pushed from JS via setRoute so the screenshot
   *  payload can carry "which screen". Best-effort label only. */
  private var currentRoute: String = ""

  // API 34+ screenshot-callback path. Typed as Any? so the API-34 class is
  // never referenced in a field signature verified on older devices.
  private var screenCaptureCallback: Any? = null
  private var boundActivity: Activity? = null

  // <API 34 MediaStore-observer fallback.
  private var contentObserver: ContentObserver? = null
  private var lastObserverFireAt = 0L

  @ReactMethod
  fun start(promise: Promise) {
    if (observing) {
      promise.resolve(Arguments.createMap().apply {
        putBoolean("alreadyObserving", true)
        putBoolean("available", true)
      })
      return
    }
    val ok =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) registerScreenCaptureCallbackApi34()
        else registerContentObserverLegacy()
    observing = ok
    promise.resolve(Arguments.createMap().apply {
      putBoolean("alreadyObserving", false)
      putBoolean("available", ok)
    })
  }

  @ReactMethod
  fun stop(promise: Promise) {
    try {
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) unregisterScreenCaptureCallbackApi34()
      else unregisterContentObserverLegacy()
    } catch (t: Throwable) {
      Log.e(TAG, "stop failed", t)
    }
    observing = false
    promise.resolve(null)
  }

  @ReactMethod
  fun isObserving(promise: Promise) {
    promise.resolve(observing)
  }

  /** JS pushes the active route on navigation. Empty string clears it. */
  @ReactMethod
  fun setRoute(route: String) {
    currentRoute = route.trim()
  }

  // RN NativeEventEmitter contract — required stubs even though we key the OS
  // observer off explicit start()/stop() rather than listener counts.
  @ReactMethod fun addListener(eventName: String) {}
  @ReactMethod fun removeListeners(count: Int) {}

  override fun onCatalystInstanceDestroy() {
    super.onCatalystInstanceDestroy()
    // Bridge swaps (guest bundle load / Hermes reload) tear down this module.
    // JS re-arms via dogfoodMode on the next foreground, so just release the
    // native observers here to avoid leaking a callback bound to a dead activity.
    try {
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) unregisterScreenCaptureCallbackApi34()
      else unregisterContentObserverLegacy()
    } catch (t: Throwable) {
      // ignore
    }
    observing = false
  }

  // MARK: - API 34+ detection

  @RequiresApi(Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
  private fun registerScreenCaptureCallbackApi34(): Boolean {
    val activity = ctx.currentActivity ?: return false
    val cb = Activity.ScreenCaptureCallback { captureAndEmit() }
    return try {
      activity.registerScreenCaptureCallback(activity.mainExecutor, cb)
      screenCaptureCallback = cb
      boundActivity = activity
      Log.i(TAG, "screenshot auto-catch armed (ScreenCaptureCallback)")
      true
    } catch (t: Throwable) {
      Log.e(TAG, "registerScreenCaptureCallback failed", t)
      false
    }
  }

  @RequiresApi(Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
  private fun unregisterScreenCaptureCallbackApi34() {
    val act = boundActivity ?: ctx.currentActivity
    (screenCaptureCallback as? Activity.ScreenCaptureCallback)?.let { cb ->
      try {
        act?.unregisterScreenCaptureCallback(cb)
      } catch (t: Throwable) {
        // ignore
      }
    }
    screenCaptureCallback = null
    boundActivity = null
  }

  // MARK: - <API 34 fallback (MediaStore observer)

  private fun registerContentObserverLegacy(): Boolean {
    if (!hasMediaReadPermission()) {
      Log.w(TAG, "screenshot auto-catch on Android <14 needs a media-read permission; not granted — using manual add")
      return false
    }
    val obs = object : ContentObserver(Handler(Looper.getMainLooper())) {
      override fun onChange(selfChange: Boolean, uri: Uri?) {
        // MediaStore fires several notifications per insert; debounce, then
        // only react if the newest image actually looks like a screenshot.
        val now = System.currentTimeMillis()
        if (now - lastObserverFireAt < 1500L) return
        if (looksLikeRecentScreenshot()) {
          lastObserverFireAt = now
          captureAndEmit()
        }
      }
    }
    return try {
      ctx.contentResolver.registerContentObserver(
          MediaStore.Images.Media.EXTERNAL_CONTENT_URI, true, obs)
      contentObserver = obs
      Log.i(TAG, "screenshot auto-catch armed (MediaStore observer)")
      true
    } catch (t: Throwable) {
      Log.e(TAG, "registerContentObserver failed", t)
      false
    }
  }

  private fun unregisterContentObserverLegacy() {
    contentObserver?.let {
      try {
        ctx.contentResolver.unregisterContentObserver(it)
      } catch (t: Throwable) {
        // ignore
      }
    }
    contentObserver = null
  }

  private fun hasMediaReadPermission(): Boolean {
    val perm =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) Manifest.permission.READ_MEDIA_IMAGES
        else Manifest.permission.READ_EXTERNAL_STORAGE
    return ctx.checkSelfPermission(perm) == PackageManager.PERMISSION_GRANTED
  }

  private fun looksLikeRecentScreenshot(): Boolean {
    return try {
      val hasRelPath = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
      val proj =
          if (hasRelPath)
              arrayOf(
                  MediaStore.Images.Media.DISPLAY_NAME,
                  MediaStore.Images.Media.DATE_ADDED,
                  MediaStore.Images.Media.RELATIVE_PATH)
          else arrayOf(MediaStore.Images.Media.DISPLAY_NAME, MediaStore.Images.Media.DATE_ADDED)
      ctx.contentResolver.query(
              MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
              proj,
              null,
              null,
              "${MediaStore.Images.Media.DATE_ADDED} DESC")
          ?.use { c ->
            if (!c.moveToFirst()) return false
            val name = c.getString(0) ?: ""
            val addedSec = c.getLong(1)
            val rel = if (hasRelPath) (c.getString(2) ?: "") else ""
            val nowSec = System.currentTimeMillis() / 1000
            val fresh = nowSec - addedSec <= 10
            val isShot = name.contains("screenshot", true) || rel.contains("screenshot", true)
            fresh && isShot
          } ?: false
    } catch (t: Throwable) {
      false
    }
  }

  // MARK: - Capture

  private fun captureAndEmit() {
    val activity = ctx.currentActivity ?: return
    activity.runOnUiThread {
      try {
        val view = activity.window?.decorView?.rootView ?: return@runOnUiThread
        if (view.width <= 0 || view.height <= 0) return@runOnUiThread
        val bmp = Bitmap.createBitmap(view.width, view.height, Bitmap.Config.ARGB_8888)
        view.draw(Canvas(bmp))
        val takenAt = System.currentTimeMillis()
        val path = writeJpeg(bmp, takenAt)
        bmp.recycle()
        if (path != null) emitShot(path, takenAt)
      } catch (t: Throwable) {
        Log.e(TAG, "capture failed", t)
      }
    }
  }

  private fun writeJpeg(bmp: Bitmap, stamp: Long): String? {
    return try {
      val dir = File(ctx.cacheDir, "dogfood")
      if (!dir.exists()) dir.mkdirs()
      val f = File(dir, "shot-$stamp.jpg")
      FileOutputStream(f).use { bmp.compress(Bitmap.CompressFormat.JPEG, 85, it) }
      f.absolutePath
    } catch (t: Throwable) {
      Log.e(TAG, "write failed", t)
      null
    }
  }

  private fun emitShot(path: String, takenAt: Long) {
    if (!ctx.hasActiveReactInstance()) return
    val payload = Arguments.createMap().apply {
      putString("path", path)
      putDouble("takenAt", takenAt.toDouble())
      if (currentRoute.isNotEmpty()) putString("route", currentRoute)
    }
    ctx.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
        .emit("onDogfoodScreenshot", payload)
  }

  companion object {
    const val NAME = "YaverDogfood"
    private const val TAG = "YaverDogfood"
  }
}
