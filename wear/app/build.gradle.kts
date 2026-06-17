// App module for the standalone Yaver Wear OS app (io.yaver.wear).
//
// Dependency versions are plausible-and-recent but MAY need a sync with the
// build machine's toolchain (Compose compiler ↔ Kotlin pairing especially).
// Source-only scaffold — not CI-wired. See wear/README.md.

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.yaver.wear"
    compileSdk = 34

    defaultConfig {
        applicationId = "io.yaver.wear"
        // Wear OS 3 (which is what almost every current watch runs) is API 30+.
        minSdk = 30
        targetSdk = 34
        versionCode = 1
        versionName = "1.0.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        compose = true
    }

    composeOptions {
        // Must match the Kotlin version in the top-level build.gradle.kts.
        // 1.5.14 pairs with Kotlin 1.9.24.
        kotlinCompilerExtensionVersion = "1.5.14"
    }

    // Kotlin sources live under src/main/kotlin (not the default src/main/java).
    sourceSets {
        getByName("main") {
            java.srcDirs("src/main/kotlin")
        }
    }
}

dependencies {
    // --- Core / lifecycle ---------------------------------------------------
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.9.1")
    implementation("androidx.fragment:fragment-ktx:1.8.2")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.4")

    // --- Jetpack Compose (BOM keeps the artifacts aligned) ------------------
    val composeBom = platform("androidx.compose:compose-bom:2024.06.00")
    implementation(composeBom)
    implementation("androidx.compose.runtime:runtime")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    debugImplementation("androidx.compose.ui:ui-tooling")

    // --- Wear Compose (watch-specific scaffolding, chips, scaling list) -----
    implementation("androidx.wear.compose:compose-material:1.3.1")
    implementation("androidx.wear.compose:compose-foundation:1.3.1")
    implementation("androidx.wear.compose:compose-navigation:1.3.1")

    // --- Wear Data Layer (MessageClient / NodeClient / CapabilityClient) ----
    // This is THE default transport: watch ⇄ paired Android phone Yaver app.
    implementation("com.google.android.gms:play-services-wearable:18.2.0")

    // --- Coroutines ---------------------------------------------------------
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
    // play-services Tasks ↔ coroutines bridge (Task<T>.await()).
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-play-services:1.8.1")

    // --- Standalone-mode HTTP (LAN /watch/turn + device-code auth) ----------
    // OkHttp keeps the standalone client small + readable. If you'd rather not
    // add a dependency, AgentClient/Backend can be rewritten on HttpURLConnection.
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // --- QR rendering for standalone sign-in (device-code short code + QR) ---
    implementation("com.google.zxing:core:3.5.3")
}
