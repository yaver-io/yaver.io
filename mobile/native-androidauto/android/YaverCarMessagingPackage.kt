// YaverCarMessagingPackage.kt — registers YaverCarMessagingModule with RN.
//
// REFERENCE IMPLEMENTATION (see YaverCarMessagingModule.kt). The Expo config
// plugin withAndroidAutoMessaging.js copies this into the app source set and
// appends it to the generated MainApplication's package list when activated.

package io.yaver.mobile.car

import android.view.View
import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ReactShadowNode
import com.facebook.react.uimanager.ViewManager

class YaverCarMessagingPackage : ReactPackage {
    override fun createNativeModules(
        reactContext: ReactApplicationContext,
    ): List<NativeModule> = listOf(YaverCarMessagingModule(reactContext))

    override fun createViewManagers(
        reactContext: ReactApplicationContext,
    ): List<ViewManager<View, ReactShadowNode<*>>> = emptyList()
}
