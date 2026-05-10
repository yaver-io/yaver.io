package io.yaver.mobile

// Registers YaverBundleLoader with React Native. Wired into
// MainApplication's getPackages() override alongside YaverInfoPackage.

import android.view.View
import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ReactShadowNode
import com.facebook.react.uimanager.ViewManager

class YaverBundleLoaderPackage : ReactPackage {
  override fun createNativeModules(ctx: ReactApplicationContext): List<NativeModule> =
      listOf(YaverBundleLoaderModule(ctx))

  override fun createViewManagers(
      ctx: ReactApplicationContext
  ): List<ViewManager<View, ReactShadowNode<*>>> = emptyList()
}
