package io.yaver.mobile
import expo.modules.splashscreen.SplashScreenManager

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.view.KeyEvent

import com.facebook.react.ReactActivity
import com.facebook.react.ReactActivityDelegate
import com.facebook.react.defaults.DefaultNewArchitectureEntryPoint.fabricEnabled
import com.facebook.react.defaults.DefaultReactActivityDelegate

import expo.modules.ReactActivityDelegateWrapper

class MainActivity : ReactActivity() {
  // YaverBundleLoaderModule.broadcastReload fires
  // io.yaver.mobile.BUNDLE_RELOAD after a successful guest-bundle save
  // (or after unload clears the saved file). We translate that into
  // recreate() — RN's host re-asks getJSBundleFile() on rebuild, so
  // the new activity loads the freshly-saved guest bytecode (or
  // falls back to Yaver's embedded bundle on unload).
  //
  // Strategy A (MVP). iOS invalidates the RCTBridge in place; porting
  // that to Android needs ReactHostImpl.loadBundle() (Kotlin-internal,
  // see node_modules/react-native/.../ReactHostImpl.kt:640). Tracked
  // as Phase 2; for now the user sees a brief splash flash on swap —
  // identical to how RN's own dev-menu "Reload" behaves.
  private val reloadReceiver = object : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
      val moduleName = intent.getStringExtra(EXTRA_MODULE_NAME) ?: "(unload)"
      Log.i(TAG, "BUNDLE_RELOAD received, moduleName=$moduleName — recreating")
      recreate()
    }
  }

  override fun onCreate(savedInstanceState: Bundle?) {
    // Set the theme to AppTheme BEFORE onCreate to support
    // coloring the background, status bar, and navigation bar.
    // This is required for expo-splash-screen.
    // setTheme(R.style.AppTheme);
    // @generated begin expo-splashscreen - expo prebuild (DO NOT MODIFY) sync-f3ff59a738c56c9a6119210cb55f0b613eb8b6af
    SplashScreenManager.registerOnActivity(this)
    // @generated end expo-splashscreen
    super.onCreate(null)
  }

  override fun onStart() {
    super.onStart()
    val filter = IntentFilter(ACTION_RELOAD)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
      // Tiramisu (33) requires explicit export flagging — this is an
      // intra-app broadcast only, so mark it NOT_EXPORTED.
      registerReceiver(reloadReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
    } else {
      @Suppress("UnspecifiedRegisterReceiverFlag")
      registerReceiver(reloadReceiver, filter)
    }
  }

  override fun onStop() {
    try {
      unregisterReceiver(reloadReceiver)
    } catch (_: IllegalArgumentException) {
      // unregister can race if the receiver was never attached (e.g.
      // activity stopped before onStart completed). Nothing to do.
    }
    super.onStop()
  }

  /**
   * Returns the name of the main component registered from JavaScript. This is used to schedule
   * rendering of the component.
   */
  override fun getMainComponentName(): String = "main"

  /**
   * Hand every hardware key down event to YaverKeyboardRouter BEFORE
   * the normal RN keyboard pipeline. When the router is grabbed it
   * forwards the event to JS as "YaverKey" and consumes the original
   * (returns true). When ungrabbed, the event flows through unchanged.
   */
  override fun dispatchKeyEvent(event: KeyEvent): Boolean {
    if (event.action == KeyEvent.ACTION_DOWN) {
      val mod = YaverKeyboardRouterModule.sharedRef
      if (mod != null && mod.dispatchKey(event)) {
        return true
      }
    }
    return super.dispatchKeyEvent(event)
  }

  /**
   * Returns the instance of the [ReactActivityDelegate]. We use [DefaultReactActivityDelegate]
   * which allows you to enable New Architecture with a single boolean flags [fabricEnabled]
   */
  override fun createReactActivityDelegate(): ReactActivityDelegate {
    return ReactActivityDelegateWrapper(
          this,
          BuildConfig.IS_NEW_ARCHITECTURE_ENABLED,
          object : DefaultReactActivityDelegate(
              this,
              mainComponentName,
              fabricEnabled
          ){})
  }

  /**
    * Align the back button behavior with Android S
    * where moving root activities to background instead of finishing activities.
    * @see <a href="https://developer.android.com/reference/android/app/Activity#onBackPressed()">onBackPressed</a>
    */
  override fun invokeDefaultOnBackPressed() {
      if (Build.VERSION.SDK_INT <= Build.VERSION_CODES.R) {
          if (!moveTaskToBack(false)) {
              // For non-root activities, use the default implementation to finish them.
              super.invokeDefaultOnBackPressed()
          }
          return
      }

      // Use the default back button implementation on Android S
      // because it's doing more than [Activity.moveTaskToBack] in fact.
      super.invokeDefaultOnBackPressed()
  }

  companion object {
    private const val TAG = "YaverMainActivity"
    private const val ACTION_RELOAD = "io.yaver.mobile.BUNDLE_RELOAD"
    private const val EXTRA_MODULE_NAME = "moduleName"
  }
}
