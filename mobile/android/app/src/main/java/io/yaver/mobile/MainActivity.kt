package io.yaver.mobile
import expo.modules.splashscreen.SplashScreenManager

import android.os.Build
import android.os.Bundle

import com.facebook.react.ReactActivity
import com.facebook.react.ReactActivityDelegate
import com.facebook.react.defaults.DefaultNewArchitectureEntryPoint.fabricEnabled
import com.facebook.react.defaults.DefaultReactActivityDelegate

import expo.modules.ReactActivityDelegateWrapper

class MainActivity : ReactActivity() {

  // Shake detector — Android counterpart to ShakeDetectingWindow on iOS.
  // Fires the same 3-button overlay (Feedback / Agents / Back to Yaver)
  // routed to the same native panes we have in
  // mobile/android/app/src/main/java/io/yaver/mobile/Yaver*Pane.kt.
  private lateinit var shakeDetector: YaverShakeDetector

  override fun onCreate(savedInstanceState: Bundle?) {
    // Set the theme to AppTheme BEFORE onCreate to support
    // coloring the background, status bar, and navigation bar.
    // This is required for expo-splash-screen.
    // setTheme(R.style.AppTheme);
    // @generated begin expo-splashscreen - expo prebuild (DO NOT MODIFY) sync-f3ff59a738c56c9a6119210cb55f0b613eb8b6af
    SplashScreenManager.registerOnActivity(this)
    // @generated end expo-splashscreen
    super.onCreate(null)
    shakeDetector = YaverShakeDetector(this) { onShake() }
  }

  override fun onResume() {
    super.onResume()
    if (::shakeDetector.isInitialized) shakeDetector.start()
  }

  override fun onPause() {
    if (::shakeDetector.isInitialized) shakeDetector.stop()
    super.onPause()
  }

  private fun onShake() {
    // Only react when a guest bundle is loaded — Yaver's own UI doesn't
    // need a shake overlay (the user is already in Yaver).
    val prefs = getSharedPreferences(YaverNativePrefs.NAME, MODE_PRIVATE)
    if (!prefs.getBoolean(YaverNativePrefs.GUEST_BUNDLE_LOADED, false)) return
    YaverShakeOverlay.show(
        activity = this,
        onFeedback = { YaverFeedbackPane.show(this) },
        onAgents = { YaverAgentsPane.show(this) },
        onDeploy = { YaverDeployPane.show(this) },
        onBack = { restoreYaverBundle() }
    )
  }

  /** Mirrors AppDelegate.swift::handleBackTap — restores Yaver's main
   *  bundle after the user picks "Back to Yaver" on the shake overlay.
   *  Implementation hook lives in YaverBundleLoader (TBD on Android — we
   *  send a broadcast the bundle loader will subscribe to once that
   *  module exists; for now we just clear the flag so subsequent shakes
   *  don't fire the overlay). */
  private fun restoreYaverBundle() {
    val prefs = getSharedPreferences(YaverNativePrefs.NAME, MODE_PRIVATE)
    prefs.edit().putBoolean(YaverNativePrefs.GUEST_BUNDLE_LOADED, false).apply()
    sendBroadcast(android.content.Intent("io.yaver.mobile.RESTORE_YAVER_BUNDLE")
        .setPackage(packageName))
  }

  /**
   * Returns the name of the main component registered from JavaScript. This is used to schedule
   * rendering of the component.
   */
  override fun getMainComponentName(): String = "main"

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
}
