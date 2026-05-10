package io.yaver.mobile

import android.app.Application
import android.content.res.Configuration

import com.facebook.react.PackageList
import com.facebook.react.ReactApplication
import com.facebook.react.ReactNativeApplicationEntryPoint.loadReactNative
import com.facebook.react.ReactNativeHost
import com.facebook.react.ReactPackage
import com.facebook.react.ReactHost
import com.facebook.react.common.ReleaseLevel
import com.facebook.react.defaults.DefaultNewArchitectureEntryPoint
import com.facebook.react.defaults.DefaultReactNativeHost
import com.facebook.react.modules.network.OkHttpClientProvider

import expo.modules.ApplicationLifecycleDispatcher
import expo.modules.ReactNativeHostWrapper

class MainApplication : Application(), ReactApplication {

  override val reactNativeHost: ReactNativeHost = ReactNativeHostWrapper(
      this,
      object : DefaultReactNativeHost(this) {
        override fun getPackages(): List<ReactPackage> =
            PackageList(this).packages.apply {
              // Packages that cannot be autolinked yet can be added manually here, for example:
              // add(MyReactNativePackage())
              add(YaverInfoPackage())
              add(YaverBundleLoaderPackage())
            }

          override fun getJSMainModuleName(): String = ".expo/.virtual-metro-entry"

          override fun getUseDeveloperSupport(): Boolean = BuildConfig.DEBUG

          override val isNewArchEnabled: Boolean = BuildConfig.IS_NEW_ARCHITECTURE_ENABLED

          // Hermes-push guest bundles land at <filesDir>/bundles/main.jsbundle
          // (YaverBundleLoaderModule.saveBundle). When present, the next
          // time the React host boots — after MainActivity.recreate() fired
          // by the reload broadcast — RN loads the guest's bytecode
          // instead of Yaver's own embedded bundle. When absent, returning
          // null falls through to the default release path
          // (assets://index.android.bundle). Mirrors iOS AppDelegate's
          // bundle URL resolution in handleBundleReload.
          override fun getJSBundleFile(): String? {
            val saved = YaverBundleLoaderModule.savedBundleFile(this@MainApplication)
            return if (saved.exists() && saved.length() > 0) saved.absolutePath else null
          }
      }
  )

  override val reactHost: ReactHost
    get() = ReactNativeHostWrapper.createReactHost(applicationContext, reactNativeHost)

  override fun onCreate() {
    super.onCreate()
    // Install the IPv4-first OkHttpClient factory BEFORE
    // loadReactNative — the first JS fetch creates the singleton
    // and from then on `setOkHttpClientFactory` is a no-op (see
    // OkHttpClientProvider.getOkHttpClient memoization). Without
    // this, fetches stall for the whole AbortController budget on
    // Wi-Fi networks that advertise v6 prefixes but drop v6 packets
    // upstream — every auth/validate, refreshDevices, and
    // backend-config refresh aborts with "Couldn't reach the auth
    // server" on real consumer routers (e.g. AirTies Air4960R).
    OkHttpClientProvider.setOkHttpClientFactory(YaverOkHttpFactory())
    DefaultNewArchitectureEntryPoint.releaseLevel = try {
      ReleaseLevel.valueOf(BuildConfig.REACT_NATIVE_RELEASE_LEVEL.uppercase())
    } catch (e: IllegalArgumentException) {
      ReleaseLevel.STABLE
    }
    loadReactNative(this)
    ApplicationLifecycleDispatcher.onApplicationCreate(this)
  }

  override fun onConfigurationChanged(newConfig: Configuration) {
    super.onConfigurationChanged(newConfig)
    ApplicationLifecycleDispatcher.onConfigurationChanged(this, newConfig)
  }
}
