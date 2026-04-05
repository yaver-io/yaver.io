package io.yaver.mobile

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import com.facebook.react.PackageList
import com.facebook.react.ReactInstanceManager
import com.facebook.react.ReactPackage
import com.facebook.react.ReactRootView
import com.facebook.react.bridge.*
import com.facebook.react.common.LifecycleState
import com.facebook.react.modules.core.DefaultHardwareBackBtnHandler

/**
 * Native module that loads external React Native JS bundles into a secondary ReactInstanceManager.
 * Android equivalent of the iOS YaverBundleLoader — enables the "super host" feature.
 *
 * Flow:
 * 1. JS calls loadBundle(url, moduleName)
 * 2. We create a secondary ReactInstanceManager with the Metro bundle URL
 * 3. We launch a new Activity with a ReactRootView using that instance
 * 4. The loaded app runs with all native modules available in the Yaver binary
 * 5. HMR works through the same URL (Metro WebSocket)
 * 6. JS calls unloadBundle() or user presses back to return to Yaver
 */
class YaverBundleLoaderModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    companion object {
        var pendingBundleUrl: String? = null
        var pendingModuleName: String? = null
        var loadedInstanceManager: ReactInstanceManager? = null

        // Known native modules shipped with Yaver
        val AVAILABLE_MODULES = listOf(
            "expo-camera", "expo-location", "expo-sensors", "expo-haptics",
            "expo-brightness", "expo-battery", "expo-device", "expo-constants",
            "expo-barcode-scanner", "expo-notifications", "expo-file-system",
            "expo-asset", "expo-font", "expo-clipboard", "expo-linking",
            "expo-secure-store", "expo-av", "expo-image-picker", "expo-speech",
            "expo-web-browser",
            "react-native-maps", "react-native-ble-plx",
            "react-native-reanimated", "react-native-gesture-handler",
            "react-native-screens", "react-native-safe-area-context",
            "react-native-webview", "@react-native-async-storage/async-storage",
            "@react-native-community/netinfo"
        )
    }

    override fun getName(): String = "YaverBundleLoader"

    @ReactMethod
    fun loadBundle(urlString: String, moduleName: String, promise: Promise) {
        val activity = reactApplicationContext.currentActivity
        if (activity == null) {
            promise.reject("NO_ACTIVITY", "No current activity")
            return
        }

        // Download bundle to local file first, then load from disk
        // (setJSBundleFile expects a file path, not a URL)
        Thread {
            try {
                val bundleDir = java.io.File(reactApplicationContext.filesDir, "bundles")
                bundleDir.mkdirs()
                val bundleFile = java.io.File(bundleDir, "main.jsbundle")

                // Download from URL (works through relay)
                val connection = java.net.URL(urlString).openConnection() as java.net.HttpURLConnection
                connection.connectTimeout = 30000
                connection.readTimeout = 120000
                connection.connect()

                if (connection.responseCode != 200) {
                    promise.reject("HTTP_ERROR", "Download failed: HTTP ${connection.responseCode}")
                    return@Thread
                }

                connection.inputStream.use { input ->
                    java.io.FileOutputStream(bundleFile).use { output ->
                        input.copyTo(output)
                    }
                }

                android.util.Log.d("YaverBundleLoader", "Bundle downloaded: ${bundleFile.length()} bytes")

                // Now load from LOCAL file path
                val app = activity.application
                val packages: List<ReactPackage> = PackageList(app).packages

                val instanceManager = ReactInstanceManager.builder()
                    .setApplication(app)
                    .setJSBundleFile(bundleFile.absolutePath)
                    .addPackages(packages)
                    .setUseDeveloperSupport(false)
                    .setInitialLifecycleState(LifecycleState.RESUMED)
                    .build()

                loadedInstanceManager = instanceManager
                pendingBundleUrl = urlString
                pendingModuleName = moduleName

                // Launch host activity on main thread
                android.os.Handler(android.os.Looper.getMainLooper()).post {
                    val intent = Intent(activity, YaverBundleHostActivity::class.java)
                    activity.startActivity(intent)
                }

                promise.resolve(Arguments.createMap().apply {
                    putBoolean("loaded", true)
                    putString("url", urlString)
                    putInt("size", bundleFile.length().toInt())
                })
            } catch (e: Exception) {
                promise.reject("LOAD_FAILED", "Failed to load bundle: ${e.message}", e)
            }
        }.start()
    }

    @ReactMethod
    fun unloadBundle(promise: Promise) {
        @Suppress("UNUSED_VARIABLE")
        val activity = reactApplicationContext.currentActivity
        // Send broadcast to close the host activity
        loadedInstanceManager?.destroy()
        loadedInstanceManager = null
        pendingBundleUrl = null
        pendingModuleName = null
        promise.resolve(Arguments.createMap().apply { putBoolean("unloaded", true) })
    }

    @ReactMethod
    fun getAvailableModules(promise: Promise) {
        val array = Arguments.createArray()
        AVAILABLE_MODULES.forEach { array.pushString(it) }
        promise.resolve(array)
    }

    @ReactMethod
    fun isLoaded(promise: Promise) {
        promise.resolve(Arguments.createMap().apply {
            putBoolean("loaded", loadedInstanceManager != null)
        })
    }
}
