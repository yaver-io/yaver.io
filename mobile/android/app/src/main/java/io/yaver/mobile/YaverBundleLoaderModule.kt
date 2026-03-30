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
        val activity = currentActivity
        if (activity == null) {
            promise.reject("NO_ACTIVITY", "No current activity")
            return
        }

        try {
            // Build a secondary ReactInstanceManager with the remote bundle URL
            val packages: List<ReactPackage> = PackageList(activity.application).packages

            val instanceManager = ReactInstanceManager.builder()
                .setApplication(activity.application)
                .setJSBundleFile(urlString)
                .addPackages(packages)
                .setUseDeveloperSupport(true)
                .setInitialLifecycleState(LifecycleState.RESUMED)
                .build()

            loadedInstanceManager = instanceManager
            pendingBundleUrl = urlString
            pendingModuleName = moduleName

            // Launch the host activity
            val intent = Intent(activity, YaverBundleHostActivity::class.java)
            activity.startActivity(intent)

            promise.resolve(Arguments.createMap().apply {
                putBoolean("loaded", true)
                putString("url", urlString)
            })
        } catch (e: Exception) {
            promise.reject("LOAD_FAILED", "Failed to load bundle: ${e.message}", e)
        }
    }

    @ReactMethod
    fun unloadBundle(promise: Promise) {
        val activity = currentActivity
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
