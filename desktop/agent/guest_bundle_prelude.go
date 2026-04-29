package main

import (
	"fmt"
	"os"
	"strings"
)

const yaverGuestSafePrelude = `(function() {
  var g = typeof globalThis !== "undefined" ? globalThis : (typeof global !== "undefined" ? global : this);
  if (!g || g.__YAVER_GUEST_SAFE_PRELUDE__) return;
  g.__YAVER_GUEST_SAFE_PRELUDE__ = true;
  g.__YAVER_GUEST_SAFE_MODULES__ = ["ExpoHaptics", "RNCNetInfo"];

  function promiseWrap(fn, fallback) {
    return function() {
      try {
        if (typeof fn !== "function") return Promise.resolve(fallback);
        var out = fn.apply(this, arguments);
        if (out && typeof out.then === "function") {
          return out.catch(function() { return fallback; });
        }
        return Promise.resolve(out === undefined ? fallback : out);
      } catch (err) {
        return Promise.resolve(fallback);
      }
    };
  }

  function markWrapped(target, wrapped) {
    try {
      Object.defineProperty(wrapped, "__yaverGuestSafeWrapped", { value: true, enumerable: false });
    } catch (_err) {
      wrapped.__yaverGuestSafeWrapped = true;
    }
    return wrapped;
  }

  function wrapExpoHaptics(mod) {
    if (!mod || mod.__yaverGuestSafeWrapped) return mod;
    var wrapped = Object.assign({}, mod);
    wrapped.notificationAsync = promiseWrap(mod.notificationAsync, undefined);
    wrapped.impactAsync = promiseWrap(mod.impactAsync, undefined);
    wrapped.selectionAsync = promiseWrap(mod.selectionAsync, undefined);
    wrapped.performHapticsAsync = promiseWrap(mod.performHapticsAsync, undefined);
    return markWrapped(mod, wrapped);
  }

  function fallbackNetInfoState() {
    return { type: "unknown", isConnected: null, details: null };
  }

  function wrapNetInfo(mod) {
    if (!mod || mod.__yaverGuestSafeWrapped) return mod;
    var wrapped = Object.assign({}, mod);
    wrapped.getCurrentState = promiseWrap(mod.getCurrentState, fallbackNetInfoState());
    wrapped.configure = function() {};
    wrapped.addListener = typeof mod.addListener === "function" ? promiseWrap(mod.addListener, undefined) : function() {};
    wrapped.removeListeners = typeof mod.removeListeners === "function" ? promiseWrap(mod.removeListeners, undefined) : function() {};
    return markWrapped(mod, wrapped);
  }

  function patchContainer(container, name, wrapper) {
    if (!container || !container[name]) return;
    try {
      container[name] = wrapper(container[name]);
    } catch (_err) {}
  }

  patchContainer(g.nativeModuleProxy, "RNCNetInfo", wrapNetInfo);
  if (g.expo && g.expo.modules) {
    patchContainer(g.expo.modules, "ExpoHaptics", wrapExpoHaptics);
    patchContainer(g.expo.modules.NativeModulesProxy, "ExpoHaptics", wrapExpoHaptics);
  }

  var originalTurboProxy = g.__turboModuleProxy;
  if (typeof originalTurboProxy === "function") {
    g.__turboModuleProxy = function(name) {
      var mod = originalTurboProxy(name);
      if (name === "ExpoHaptics") return wrapExpoHaptics(mod);
      if (name === "RNCNetInfo") return wrapNetInfo(mod);
      return mod;
    };
  }
})();`

func injectGuestSafePrelude(bundlePath string) error {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "__YAVER_GUEST_SAFE_PRELUDE__") {
		return nil
	}
	combined := append([]byte(yaverGuestSafePrelude+"\n"), data...)
	if err := os.WriteFile(bundlePath, combined, 0o644); err != nil {
		return fmt.Errorf("write guest-safe prelude: %w", err)
	}
	return nil
}
