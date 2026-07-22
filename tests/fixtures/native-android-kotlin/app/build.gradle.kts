plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.yaver.fixture.nativeandroid"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.yaver.fixture.nativeandroid"
        minSdk = 23
        targetSdk = 35
        versionCode = 1
        versionName = "0.0.1"
    }
}
