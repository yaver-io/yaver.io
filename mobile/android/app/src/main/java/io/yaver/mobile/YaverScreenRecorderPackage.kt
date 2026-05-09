package io.yaver.mobile

// YaverScreenRecorderPackage — registers the ScreenRecorder native
// module with React Native's package list. Wired into MainApplication's
// getPackages() override.

import android.view.View
import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ReactShadowNode
import com.facebook.react.uimanager.ViewManager

class YaverScreenRecorderPackage : ReactPackage {
  override fun createNativeModules(ctx: ReactApplicationContext): List<NativeModule> =
      listOf(YaverScreenRecorderModule(ctx))

  override fun createViewManagers(
      ctx: ReactApplicationContext
  ): List<ViewManager<View, ReactShadowNode<*>>> = emptyList()
}
