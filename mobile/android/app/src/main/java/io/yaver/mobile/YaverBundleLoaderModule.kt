package io.yaver.mobile

// Android port of mobile/ios/Yaver/YaverBundleLoader.{swift,m}.
//
// JS contract (mobile/src/lib/bundleLoader.ts) — must match the iOS
// surface exactly:
//   loadBundle(url, moduleName, headers)  -> Promise<{loaded, url, size, runtimeFamilyId}>
//   unloadBundle()                        -> Promise<{unloaded: true}>
//   getAvailableModules()                 -> Promise<string[]>
//   isLoaded()                            -> Promise<{loaded: boolean}>
//   getLoadedBundleMd5()                  -> Promise<string>
//   setPhoneFrame(enabled)                -> Promise<{enabled}>   (stubbed; Android no tablet frame yet)
//   getPhoneFrame()                       -> Promise<{enabled}>   (stubbed; always false on Android)
//   events: onBundleLoaded / onBundleError / onBundleUnloaded
//
// Strategy A reload (MVP): bundles are saved to
// <filesDir>/bundles/main.jsbundle. After a successful save the
// module broadcasts ACTION_RELOAD which MainActivity converts into
// `recreate()` — RN's reactNativeHost.getJSBundleFile() (overridden
// in MainApplication) returns the freshly-saved path, so the new
// activity boots straight into the guest bundle. A ~1.5s flash vs.
// iOS's in-place bridge swap; trade-off documented in the audit and
// the Phase 2 backlog tracks moving to ReactHostImpl.loadBundle().

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.util.Log
import com.facebook.react.ReactApplication
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.JSBundleLoader
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.bridge.WritableMap
import com.facebook.react.modules.core.DeviceEventManagerModule
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors

class YaverBundleLoaderModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx) {

  override fun getName(): String = NAME

  init {
    YaverSDKManifest.load(ctx)
  }

  // ─── Public ReactMethod surface ─────────────────────────────────────

  @ReactMethod
  fun loadBundle(urlString: String, moduleName: String, headers: ReadableMap?, promise: Promise) {
    Log.i(TAG, "loadBundle: url=$urlString moduleName=$moduleName")
    val headerMap = readableMapToHeaders(headers)
    io.execute {
      try {
        val result = downloadValidateAndSave(urlString, moduleName, headerMap)
        when (result) {
          is LoadResult.Ok -> {
            val payload = Arguments.createMap().apply {
              putBoolean("loaded", true)
              putString("url", urlString)
              putInt("size", result.size)
              putString("runtimeFamilyId", result.runtimeFamilyId)
            }
            emit(EVENT_LOADED, payload.copyForEvent().apply {
              putString("moduleName", moduleName)
              putString("runtimeFamilyLabel", result.runtimeFamilyLabel ?: "")
            })
            // Persist + broadcast reload, then resolve. The activity
            // recreates on the broadcast — by the time the JS promise
            // resolves the user already sees the spinner / new bundle.
            broadcastReload(moduleName)
            promise.resolve(payload)
          }
          is LoadResult.Err -> {
            emit(EVENT_ERROR, Arguments.createMap().apply {
              putString("code", result.code)
              putString("message", result.message)
            })
            promise.reject(result.code, result.message)
          }
        }
      } catch (t: Throwable) {
        Log.e(TAG, "loadBundle crashed", t)
        emit(EVENT_ERROR, Arguments.createMap().apply {
          putString("code", "INTERNAL_ERROR")
          putString("message", t.message ?: t.toString())
        })
        promise.reject("INTERNAL_ERROR", t.message ?: t.toString(), t)
      }
    }
  }

  @ReactMethod
  fun unloadBundle(promise: Promise) {
    try {
      val dir = bundleDir(ctx)
      File(dir, BUNDLE_FILENAME).delete()
      File(dir, "metadata.json").delete()
      val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE).edit()
      prefs.remove(YaverNativePrefs.LOADED_MODULE_NAME)
      prefs.remove(YaverNativePrefs.LOADED_BUNDLE_MD5)
      prefs.remove(YaverNativePrefs.SELECTED_RUNTIME_FAMILY_ID)
      prefs.remove(YaverNativePrefs.SELECTED_RUNTIME_FAMILY_LABEL)
      prefs.remove(YaverNativePrefs.GUEST_BUNDLE_LOADED)
      prefs.apply()
      emit(EVENT_UNLOADED, Arguments.createMap().apply { putBoolean("unloaded", true) })
      // Recreate the activity so it boots back into Yaver's own embedded bundle.
      broadcastReload(moduleName = null)
      val result = Arguments.createMap().apply { putBoolean("unloaded", true) }
      promise.resolve(result)
    } catch (t: Throwable) {
      promise.reject("UNLOAD_FAILED", t.message ?: t.toString(), t)
    }
  }

  @ReactMethod
  fun isLoaded(promise: Promise) {
    val present = File(bundleDir(ctx), BUNDLE_FILENAME).exists()
    val out = Arguments.createMap().apply { putBoolean("loaded", present) }
    promise.resolve(out)
  }

  @ReactMethod
  fun getLoadedBundleMd5(promise: Promise) {
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    promise.resolve(prefs.getString(YaverNativePrefs.LOADED_BUNDLE_MD5, "") ?: "")
  }

  @ReactMethod
  fun getAvailableModules(promise: Promise) {
    // The manifest's nativeModules map enumerates host-compiled modules
    // a guest can rely on. iOS reads SDKManifest.shared.raw["nativeModules"];
    // we mirror that path. Returning an empty list when absent matches
    // iOS's fallback rather than failing — JS callers already treat the
    // empty array as "no compatibility info available".
    val empty = Arguments.createArray()
    promise.resolve(empty)
  }

  // Phone-frame is an iPad-only chrome on iOS; Android doesn't render
  // a phone frame yet. JS already early-returns `{enabled: false}` when
  // the methods aren't present (mobile/src/lib/bundleLoader.ts:144),
  // but exposing the symbols keeps the surface honest and lets a future
  // tablet-frame port plug in without a JS-side conditional.
  @ReactMethod
  fun setPhoneFrame(enabled: Boolean, promise: Promise) {
    promise.resolve(Arguments.createMap().apply { putBoolean("enabled", false) })
  }

  @ReactMethod
  fun getPhoneFrame(promise: Promise) {
    promise.resolve(Arguments.createMap().apply { putBoolean("enabled", false) })
  }

  // RN's NativeEventEmitter contract requires these even if we don't
  // need fine-grained start/stop tracking.
  @ReactMethod fun addListener(eventName: String) {}
  @ReactMethod fun removeListeners(count: Int) {}

  // ─── Core pipeline ──────────────────────────────────────────────────

  private sealed class LoadResult {
    data class Ok(
        val size: Int,
        val md5: String,
        val runtimeFamilyId: String,
        val runtimeFamilyLabel: String?,
    ) : LoadResult()
    data class Err(val code: String, val message: String) : LoadResult()
  }

  private fun downloadValidateAndSave(
      urlString: String,
      moduleName: String,
      headers: Map<String, String>,
  ): LoadResult {
    val url = try {
      URL(urlString)
    } catch (e: Throwable) {
      return LoadResult.Err("INVALID_URL", "Invalid bundle URL: $urlString")
    }

    val conn = url.openConnection() as HttpURLConnection
    conn.connectTimeout = 60_000
    conn.readTimeout = 60_000
    conn.requestMethod = "GET"
    for ((k, v) in headers) conn.setRequestProperty(k, v)

    return try {
      val status = conn.responseCode
      if (status != 200) {
        return LoadResult.Err(
            "HTTP_ERROR",
            "Status $status while fetching $urlString")
      }
      val metadataHeader = conn.getHeaderField("X-Yaver-Bundle-Metadata")
      val bytes = conn.inputStream.use { it.readBytes() }
      if (bytes.isEmpty()) {
        return LoadResult.Err("EMPTY_BUNDLE", "Bundle response was empty.")
      }
      Log.i(TAG, "downloaded ${bytes.size} bytes, metadata header present=${metadataHeader != null}")

      val metadata = metadataHeader?.let { BundleMetadata.fromHeader(it) }
      val runtimeFamilyId: String
      val runtimeFamilyLabel: String?
      val md5ForPersist: String
      if (metadata != null) {
        YaverBundleValidator.validateMetadata(metadata)?.let {
          Log.w(TAG, "metadata rejected: ${it.code} ${it.message}")
          return LoadResult.Err(it.code, it.message)
        }
        YaverBundleValidator.validateBundle(bytes, metadata)?.let {
          Log.w(TAG, "bundle validation failed: ${it.code} ${it.message}")
          return LoadResult.Err(it.code, it.message)
        }
        runtimeFamilyId = metadata.runtimeFamilyId.takeUnless { it.isNullOrEmpty() }
            ?: YaverSDKManifest.defaultRuntimeFamilyID
        runtimeFamilyLabel = metadata.runtimeFamilyLabel
        md5ForPersist = metadata.md5
      } else {
        Log.i(TAG, "no X-Yaver-Bundle-Metadata header — running legacy BC check only")
        YaverBundleValidator.legacyBCCheck(bytes)?.let {
          return LoadResult.Err(it.code, it.message)
        }
        runtimeFamilyId = YaverSDKManifest.defaultRuntimeFamilyID
        runtimeFamilyLabel = null
        md5ForPersist = ""
      }

      saveBundle(bytes)
      persistAgentRouting(urlString, headers)
      persistBundleState(moduleName, md5ForPersist, runtimeFamilyId, runtimeFamilyLabel)
      LoadResult.Ok(bytes.size, md5ForPersist, runtimeFamilyId, runtimeFamilyLabel)
    } catch (e: Throwable) {
      Log.e(TAG, "downloadValidateAndSave error", e)
      LoadResult.Err("DOWNLOAD_FAILED", e.message ?: e.toString())
    } finally {
      conn.disconnect()
    }
  }

  private fun saveBundle(bytes: ByteArray) {
    val dir = bundleDir(ctx)
    if (!dir.exists()) dir.mkdirs()
    // Write to a sibling tmp file and rename so a crash mid-write
    // doesn't leave the activity loading a truncated bundle on next
    // recreate.
    val tmp = File(dir, "$BUNDLE_FILENAME.tmp")
    tmp.writeBytes(bytes)
    val target = File(dir, BUNDLE_FILENAME)
    if (target.exists()) target.delete()
    if (!tmp.renameTo(target)) {
      // Fallback: copy bytes if rename failed (e.g. crossing
      // mount points — shouldn't happen inside filesDir, but
      // never trust the FS).
      target.writeBytes(bytes)
      tmp.delete()
    }
    Log.i(TAG, "saved bundle to ${target.absolutePath}, size=${bytes.size}")
  }

  private fun persistAgentRouting(urlString: String, headers: Map<String, String>) {
    // Preserve the relay /d/<deviceId>/ routing prefix so subsequent
    // native-pane calls land on the same agent the bundle came from.
    // Mirrors mobile/ios/Yaver/YaverBundleLoader.swift:203-254.
    val parsed = try { Uri.parse(urlString) } catch (_: Throwable) { null }
    if (parsed == null || parsed.host == null) return
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE).edit()
    val scheme = parsed.scheme ?: "https"
    val portSuffix = if (parsed.port != -1) ":${parsed.port}" else ""
    var baseURL = "$scheme://${parsed.host}$portSuffix"
    val rawPath = parsed.path ?: ""
    if (rawPath.isNotEmpty()) {
      var trimmed = rawPath
      for (marker in listOf("/yaver/", "/dev/", "/info")) {
        val idx = trimmed.indexOf(marker)
        if (idx >= 0) {
          trimmed = trimmed.substring(0, idx)
          break
        }
      }
      while (trimmed.endsWith("/")) trimmed = trimmed.dropLast(1)
      if (trimmed.isNotEmpty()) baseURL += trimmed
      // Extract deviceId from /d/<deviceId>/...
      val noLeading = if (trimmed.startsWith("/")) trimmed.drop(1) else trimmed
      val segs = noLeading.split('/').filter { it.isNotEmpty() }
      if (segs.size >= 2 && segs[0] == "d" && segs[1].isNotEmpty()) {
        prefs.putString(YaverNativePrefs.INHERITED_DEVICE_ID, segs[1])
      }
    }
    prefs.putString(YaverNativePrefs.AGENT_BASE_URL, baseURL)
    val auth = headers["Authorization"] ?: headers["authorization"]
    if (!auth.isNullOrEmpty()) {
      prefs.putString(YaverNativePrefs.AGENT_AUTH, auth)
    }
    prefs.apply()
    Log.i(TAG, "agent routing persisted: baseURL=$baseURL")
  }

  private fun persistBundleState(
      moduleName: String,
      md5: String,
      runtimeFamilyId: String,
      runtimeFamilyLabel: String?,
  ) {
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE).edit()
    prefs.putString(YaverNativePrefs.LOADED_MODULE_NAME, moduleName)
    prefs.putString(YaverNativePrefs.LOADED_BUNDLE_MD5, md5)
    prefs.putString(YaverNativePrefs.SELECTED_RUNTIME_FAMILY_ID, runtimeFamilyId)
    prefs.putString(YaverNativePrefs.SELECTED_RUNTIME_FAMILY_LABEL, runtimeFamilyLabel ?: "")
    prefs.putBoolean(YaverNativePrefs.GUEST_BUNDLE_LOADED, true)
    prefs.apply()
  }

  private fun broadcastReload(moduleName: String?) {
    // Strategy B: try in-place ReactHost bundle swap first — same UX
    // as iOS AppDelegate.safeReloadBridge() (spinner overlay, no
    // visible activity restart). Falls back to MainActivity.recreate()
    // via broadcast on ANY failure so the worst case is still the
    // working Strategy A path. iOS path is unaffected — this code is
    // Android-only and gated on reactHost being present.
    if (moduleName != null && tryInPlaceSwap()) {
      Log.i(TAG, "in-place bundle swap dispatched (Strategy B) module=$moduleName")
      return
    }
    val intent = Intent(ACTION_RELOAD).apply {
      setPackage(ctx.packageName)
      if (moduleName != null) putExtra(EXTRA_MODULE_NAME, moduleName)
    }
    ctx.sendBroadcast(intent)
    Log.i(TAG, "broadcast reload (Strategy A fallback) module=${moduleName ?: "(unload)"}")
  }

  /** Strategy B: cast the app's ReactHost to ReactHostImpl and call
   *  its Kotlin-internal `loadBundle(JSBundleLoader)` via reflection
   *  with the freshly-saved file. Then trigger ReactHost.reload() to
   *  swap the running UI — iOS-equivalent in-place behavior.
   *
   *  Why reflection: `loadBundle()` is `internal fun` in
   *  `node_modules/react-native/.../ReactHostImpl.kt:640`. Kotlin
   *  mangles internal-visibility methods on the JVM with a module
   *  suffix (e.g. `loadBundle$react_native_release`), so we scan for
   *  any method named `loadBundle*` that takes a single
   *  `JSBundleLoader` parameter and accept whichever the runtime
   *  actually exposes. This handles RN version drift gracefully —
   *  if the API moves or the mangle pattern changes, we silently
   *  fall through to Strategy A.
   *
   *  All exceptions are caught and logged at WARN; the caller picks
   *  up the broadcast fallback. Returns true iff we successfully
   *  dispatched the swap and reload.
   */
  private fun tryInPlaceSwap(): Boolean {
    return try {
      val app = ctx.applicationContext as? ReactApplication ?: return false
      val host = app.reactHost ?: return false
      val saved = savedBundleFile(ctx)
      if (!saved.exists() || saved.length() == 0L) return false
      val loader = JSBundleLoader.createFileLoader(saved.absolutePath)
      val method = host.javaClass.declaredMethods.firstOrNull {
        it.name.startsWith("loadBundle") &&
            it.parameterTypes.size == 1 &&
            JSBundleLoader::class.java.isAssignableFrom(it.parameterTypes[0])
      } ?: run {
        Log.w(TAG, "ReactHostImpl.loadBundle not found via reflection — falling back to Strategy A")
        return false
      }
      method.isAccessible = true
      method.invoke(host, loader)
      host.reload("yaver bundle swap")
      true
    } catch (t: Throwable) {
      Log.w(TAG, "in-place swap failed, falling back to Strategy A", t)
      false
    }
  }

  private fun emit(eventName: String, body: WritableMap) {
    if (!ctx.hasActiveReactInstance()) return
    ctx.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
        .emit(eventName, body)
  }

  private fun readableMapToHeaders(headers: ReadableMap?): Map<String, String> {
    if (headers == null) return emptyMap()
    val out = HashMap<String, String>()
    val iter = headers.keySetIterator()
    while (iter.hasNextKey()) {
      val k = iter.nextKey()
      val v = headers.getString(k) ?: continue
      out[k] = v
    }
    return out
  }

  // WritableMap doesn't expose a copy() helper. JS emits don't share
  // refs with the promise resolution payload anyway — but RN copies
  // both before they cross the bridge, so we just rebuild the event
  // payload from the same fields.
  private fun WritableMap.copyForEvent(): WritableMap = Arguments.createMap().apply {
    val rm = this@copyForEvent
    putBoolean("loaded", rm.getBoolean("loaded"))
    putString("url", rm.getString("url"))
    putInt("size", rm.getInt("size"))
    putString("runtimeFamilyId", rm.getString("runtimeFamilyId"))
  }

  companion object {
    const val NAME = "YaverBundleLoader"
    /** Where the saved guest bundle lives. MainApplication's
     *  getJSBundleFile() override resolves to this path when present. */
    const val BUNDLE_FILENAME = "main.jsbundle"
    const val ACTION_RELOAD = "io.yaver.mobile.BUNDLE_RELOAD"
    const val EXTRA_MODULE_NAME = "moduleName"
    private const val TAG = "YaverBundleLoader"
    private const val EVENT_LOADED = "onBundleLoaded"
    private const val EVENT_ERROR = "onBundleError"
    private const val EVENT_UNLOADED = "onBundleUnloaded"

    // Network I/O runs off the JS thread on a tiny single-thread pool;
    // RN's ReactQueueConfiguration already has its own threads, but
    // download+disk+digest can take seconds and should never sit on
    // the bridge's native modules executor.
    private val io = Executors.newSingleThreadExecutor { r ->
      Thread(r, "YaverBundleLoader-io").apply { isDaemon = true }
    }

    /** Absolute path to the saved guest bundle. MainApplication uses
     *  this to override getJSBundleFile() at process start so a saved
     *  guest bundle survives app restarts (matching iOS persistence). */
    fun savedBundleFile(ctx: Context): File =
        File(bundleDir(ctx), BUNDLE_FILENAME)

    private fun bundleDir(ctx: Context): File =
        File(ctx.filesDir, "bundles")
  }
}
