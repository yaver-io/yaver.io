// App module for the standalone Yaver Android TV app.
//
// Native Kotlin/Compose counterpart of the tvOS app (tvos/YaverTV) — the same
// lean-back runtime-control surface, same LAN-first / relay-second transport,
// same FailureSignals copy, re-implemented for Android TV so the shared RN
// binary can stay a phone/tablet app. Depends on the mobile keystore for
// release signing (same owner), exactly like wear/.
//
// compileSdk 34 + AGP 8.2.x is the pairing this repo's toolchain is proven on.

import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val keystorePropertiesFile = rootProject.file("../mobile/android/keystore.properties")
val keystoreProperties = Properties()
if (keystorePropertiesFile.exists()) {
    keystorePropertiesFile.inputStream().use { keystoreProperties.load(it) }
}

android {
    namespace = "io.yaver.tv"
    compileSdk = 34

    defaultConfig {
        applicationId = (findProperty("yaverTvApplicationId") as String?) ?: "io.yaver.mobile"
        // Android TV devices: Leanback is API 21+; minSdk 23 is the floor the
        // modern Google TV / Android TV boxes ship on.
        minSdk = 23
        targetSdk = 34
        versionCode = ((findProperty("yaverTvVersionCode") as String?) ?: "1").toInt()
        versionName = (findProperty("yaverTvVersionName") as String?) ?: "1.0.0"
    }

    signingConfigs {
        create("release") {
            if (keystoreProperties["storeFile"] != null) {
                // `file()` is relative to :app, not the standalone root.
                // Resolve through rootProject so the shared mobile keystore
                // is found consistently from every Gradle invocation.
                // The shared properties file was authored relative to the
                // phone :app project (`mobile/android/app`). Preserve that
                // contract when the standalone TV project reads it.
                storeFile = rootProject.file("../mobile/android/app/${keystoreProperties["storeFile"]}")
                storePassword = keystoreProperties["storePassword"] as String?
                keyAlias = keystoreProperties["keyAlias"] as String?
                keyPassword = keystoreProperties["keyPassword"] as String?
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.getByName("release")
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
        buildConfig = true
        compose = true
    }

    composeOptions {
        // Must match the Kotlin version in the top-level build.gradle.kts.
        // 1.5.14 pairs with Kotlin 1.9.24.
        kotlinCompilerExtensionVersion = "1.5.14"
    }

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
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.4")

    // --- Jetpack Compose (BOM keeps the artifacts aligned) ------------------
    val composeBom = platform("androidx.compose:compose-bom:2024.06.00")
    implementation(composeBom)
    implementation("androidx.compose.runtime:runtime")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    debugImplementation("androidx.compose.ui:ui-tooling")

    // --- Coroutines ---------------------------------------------------------
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // --- Navigation (back-stack for pushed screens) --------------------------
    implementation("androidx.navigation:navigation-compose:2.7.7")

    // --- HTTP: LAN-first / relay-second transport (OpsClient, Backend) ------
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // --- QR rendering for device-code sign-in (zxing core QRCodeWriter) -----
    implementation("com.google.zxing:core:3.5.3")

    // --- Tests: FailureSignals parity (pure JVM, no emulator) ---------------
    testImplementation("junit:junit:4.13.2")
    // Android's platform org.json methods are stubbed in local JVM tests;
    // provide the real implementation so parity tests can construct wire JSON.
    testImplementation("org.json:json:20240303")
}
