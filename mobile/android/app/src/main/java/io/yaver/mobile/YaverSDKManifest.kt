package io.yaver.mobile

// Android counterpart of mobile/ios/Yaver/YaverBundleValidator.swift's
// `SDKManifest` final class. Reads assets/sdk-manifest.json once on
// first use and exposes the Hermes bytecode version the agent's HBC
// bundle MUST match for this Yaver build. The manifest is the source
// of truth (mobile/sdk-manifest.json) — Gradle bundles a copy into
// app/src/main/assets/. CLAUDE.md's "5-place version contract" keeps
// these in sync; the CI drift check covers the JSON copies.

import android.content.Context
import android.util.Log
import org.json.JSONObject

object YaverSDKManifest {
  private const val TAG = "YaverSDKManifest"
  private var raw: JSONObject = JSONObject()
  private var loaded = false

  /** Hermes bytecode version baked into this Yaver binary. The agent's
   *  X-Yaver-Bundle-Metadata `hermesBCVersion` field must match this
   *  exactly — otherwise the JS engine would crash on an unknown
   *  opcode at load time. 0 means "manifest absent / unparseable";
   *  callers treat 0 as "skip the BC check" (legacy-friendly). */
  var hermesBytecodeVersion: Int = 0
    private set

  fun load(ctx: Context) {
    if (loaded) return
    try {
      val text = ctx.assets.open("sdk-manifest.json").bufferedReader().use { it.readText() }
      raw = JSONObject(text)
      val hermes = raw.optJSONObject("hermes")
      hermesBytecodeVersion = hermes?.optInt("bytecodeVersion", 0) ?: 0
      Log.i(TAG, "loaded sdk-manifest.json, hermesBCVersion=$hermesBytecodeVersion")
    } catch (e: Throwable) {
      Log.w(TAG, "sdk-manifest.json not bundled or unparseable: ${e.message}")
    }
    loaded = true
  }

  val sdkVersion: String?
    get() = raw.optString("sdkVersion").ifEmpty { null }

  /** Default runtime family ID — first compiledIn=true entry in
   *  runtimeFamilies, falling back to a synthesized "default" so
   *  the JS-side metadata persistence has something to write. */
  val defaultRuntimeFamilyID: String
    get() {
      val families = raw.optJSONArray("runtimeFamilies") ?: return "default"
      for (i in 0 until families.length()) {
        val f = families.optJSONObject(i) ?: continue
        if (f.optBoolean("compiledIn", false)) {
          val id = f.optString("id")
          if (id.isNotEmpty()) return id
        }
      }
      return "default"
    }
}
