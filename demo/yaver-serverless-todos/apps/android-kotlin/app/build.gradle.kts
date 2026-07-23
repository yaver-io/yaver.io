plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.yaver.serverlesstodo"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.yaver.serverlesstodo"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }
}
