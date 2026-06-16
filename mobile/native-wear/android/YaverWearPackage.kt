// YaverWearPackage.kt — registers YaverWearBridgeModule with React Native.
// withWatchBridge.js adds `packages.add(new YaverWearPackage())` to
// MainApplication's getPackages() (mirrors how withAndroidAutoMessaging.js
// registers YaverCarMessagingPackage).

package io.yaver.mobile.wear

import android.view.View
import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ReactShadowNode
import com.facebook.react.uimanager.ViewManager

class YaverWearPackage : ReactPackage {
  override fun createNativeModules(
    reactContext: ReactApplicationContext,
  ): List<NativeModule> = listOf(YaverWearBridgeModule(reactContext))

  override fun createViewManagers(
    reactContext: ReactApplicationContext,
  ): List<ViewManager<View, ReactShadowNode<*>>> = emptyList()
}
