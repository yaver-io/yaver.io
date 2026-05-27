package io.yaver.mobile

// YaverKeyboardRouterModule — Android counterpart to
// mobile/ios/Yaver/YaverKeyboardRouter.swift.
//
// Forwards hardware keyboard events to JS as the "YaverKey" event so
// the TS-side `keyboardRouter` can dispatch to the active sink. We
// can't do full HID-grab here either — Android intentionally routes
// system shortcuts (e.g. Recent Apps, Home) past app focus — but
// every keystroke that does reach the activity is captured.
//
// Wiring:
//
//   1. Activity calls YaverKeyboardRouterModule.dispatchKey from
//      onKeyDown / onKeyUp. The module decides whether the event
//      was consumed (router is grabbed) and returns to the activity.
//   2. When grabbed, plain printable characters and named keys
//      (Enter/Tab/arrows/…) become a single JS event.
//
// JS surface matches the iOS module:
//
//   await NativeModules.YaverKeyboardRouter.grab({ exclusive: false })
//   const sub = new NativeEventEmitter(NativeModules.YaverKeyboardRouter)
//                 .addListener("YaverKey", (ev) => keyboardRouter.handleKey(ev))
//   …
//   await NativeModules.YaverKeyboardRouter.release()
//   sub.remove()

import android.hardware.input.InputManager
import android.view.KeyEvent
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableMap
import com.facebook.react.modules.core.DeviceEventManagerModule

class YaverKeyboardRouterModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx), InputManager.InputDeviceListener {

  override fun getName(): String = "YaverKeyboardRouter"

  @Volatile private var grabbed: Boolean = false
  private var inputManager: InputManager? = null

  init {
    // Static instance pointer so the MainActivity bridge can reach us
    // from onKeyDown / onKeyUp without round-tripping through the
    // ReactInstanceManager (which can be null during cold start).
    sharedRef = this
  }

  override fun onCatalystInstanceDestroy() {
    super.onCatalystInstanceDestroy()
    try {
      inputManager?.unregisterInputDeviceListener(this)
    } catch (_: Throwable) {}
    if (sharedRef === this) {
      sharedRef = null
    }
  }

  @ReactMethod
  fun grab(opts: com.facebook.react.bridge.ReadableMap?, promise: Promise) {
    grabbed = true
    val im = ctx.getSystemService(android.content.Context.INPUT_SERVICE) as? InputManager
    inputManager = im
    try {
      im?.registerInputDeviceListener(this, null)
    } catch (_: Throwable) {
      // Some devices throw if listener already registered — non-fatal.
    }
    val result = Arguments.createMap()
    result.putBoolean("alreadyGrabbed", false)
    result.putBoolean("supportsHardwareKeyboard",
        im?.inputDeviceIds?.any { isHardwareKeyboard(it) } == true)
    promise.resolve(result)
  }

  @ReactMethod
  fun release(promise: Promise) {
    grabbed = false
    try {
      inputManager?.unregisterInputDeviceListener(this)
    } catch (_: Throwable) {}
    inputManager = null
    promise.resolve(null)
  }

  @ReactMethod
  fun isAttached(promise: Promise) {
    val im = inputManager ?: ctx.getSystemService(android.content.Context.INPUT_SERVICE) as? InputManager
    val present = im?.inputDeviceIds?.any { isHardwareKeyboard(it) } == true
    promise.resolve(present)
  }

  /**
   * Called from MainActivity.dispatchKeyEvent. Returns true when the
   * router consumed the event (caller should NOT pass it on to the
   * normal RN keyboard pipeline).
   */
  fun dispatchKey(event: KeyEvent): Boolean {
    if (!grabbed) return false
    if (event.action != KeyEvent.ACTION_DOWN) return false
    val payload = payload(event) ?: return false
    sendEvent("YaverKey", payload)
    return true
  }

  private fun payload(event: KeyEvent): WritableMap? {
    val key = namedFor(event) ?: return null
    val map = Arguments.createMap()
    map.putString("key", key)
    val modifiers = Arguments.createMap()
    modifiers.putBoolean("shift", event.isShiftPressed)
    modifiers.putBoolean("ctrl", event.isCtrlPressed)
    modifiers.putBoolean("alt", event.isAltPressed)
    modifiers.putBoolean("meta", event.isMetaPressed)
    map.putMap("modifiers", modifiers)
    return map
  }

  private fun namedFor(event: KeyEvent): String? {
    val code = event.keyCode
    when (code) {
      KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> return "Enter"
      KeyEvent.KEYCODE_TAB -> return "Tab"
      KeyEvent.KEYCODE_DEL -> return "Backspace"
      KeyEvent.KEYCODE_ESCAPE -> return "Escape"
      KeyEvent.KEYCODE_DPAD_LEFT -> return "ArrowLeft"
      KeyEvent.KEYCODE_DPAD_RIGHT -> return "ArrowRight"
      KeyEvent.KEYCODE_DPAD_UP -> return "ArrowUp"
      KeyEvent.KEYCODE_DPAD_DOWN -> return "ArrowDown"
      KeyEvent.KEYCODE_MOVE_HOME -> return "Home"
      KeyEvent.KEYCODE_MOVE_END -> return "End"
      KeyEvent.KEYCODE_PAGE_UP -> return "PageUp"
      KeyEvent.KEYCODE_PAGE_DOWN -> return "PageDown"
      KeyEvent.KEYCODE_FORWARD_DEL -> return "Delete"
    }
    // getUnicodeChar gives us the printable character with shift/alt
    // applied — same Unicode codepoint a TextInput would receive.
    val uc = event.unicodeChar
    if (uc == 0) return null
    return uc.toChar().toString()
  }

  // InputDeviceListener — emit JS events when the user pairs /
  // unpairs a Bluetooth keyboard while grabbed.
  override fun onInputDeviceAdded(deviceId: Int) {
    if (isHardwareKeyboard(deviceId)) {
      sendEvent("YaverKeyboardConnected", Arguments.createMap())
    }
  }

  override fun onInputDeviceRemoved(deviceId: Int) {
    sendEvent("YaverKeyboardDisconnected", Arguments.createMap())
  }

  override fun onInputDeviceChanged(deviceId: Int) {}

  private fun isHardwareKeyboard(deviceId: Int): Boolean {
    val dev = android.view.InputDevice.getDevice(deviceId) ?: return false
    return dev.keyboardType == android.view.InputDevice.KEYBOARD_TYPE_ALPHABETIC &&
        !dev.isVirtual
  }

  private fun sendEvent(name: String, body: WritableMap) {
    try {
      ctx.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
          .emit(name, body)
    } catch (_: Throwable) {
      // RN bridge can be down during teardown — drop the event quietly.
    }
  }

  companion object {
    /// Static accessor so MainActivity.dispatchKeyEvent (which doesn't
    /// hold a reference to the ReactContext) can reach the module.
    @Volatile var sharedRef: YaverKeyboardRouterModule? = null
      private set
  }
}
