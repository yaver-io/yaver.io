// Settings for the standalone Yaver Wear OS app.
//
// This is a SELF-CONTAINED Gradle build that lives OUTSIDE mobile/android on
// purpose: Wear OS is plain Jetpack Compose (no React Native), and a `:wear`
// module inside mobile/android would be clobbered on every `expo prebuild
// --clean`. Keeping it standalone — like tvos/ for Apple TV — avoids that fight.
// See wear/README.md and docs/yaver-smartwatch-voice-terminal.md §6.

pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "YaverWear"
include(":app")
