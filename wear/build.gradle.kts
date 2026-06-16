// Top-level build file for the standalone Yaver Wear OS app.
//
// Plugin versions are declared here with `apply false` and applied per-module
// (see app/build.gradle.kts). They are pinned to a plausible, recent Wear-OS-
// capable toolchain, but MAY need aligning with the AGP / Kotlin / Compose
// compiler versions installed on the build machine — this is a source-only
// scaffold, not wired to CI. If Compose fails to compile, the usual culprit is
// the kotlinCompilerExtensionVersion ↔ Kotlin version pairing (see
// https://developer.android.com/jetpack/androidx/releases/compose-kotlin).

plugins {
    // AGP 8.x targets Wear OS (minSdk 30) fine.
    id("com.android.application") version "8.5.2" apply false
    id("org.jetbrains.kotlin.android") version "1.9.24" apply false
}
