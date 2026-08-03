import java.io.ByteArrayOutputStream

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.github.muelli.syncthingsocket"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.github.muelli.syncthingsocket"
        minSdk = 26
        targetSdk = 34
        versionCode = 1
        versionName = "1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables {
            useSupportLibrary = true
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }
    kotlinOptions {
        jvmTarget = "1.8"
    }
    buildFeatures {
        compose = true
    }
    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.1"
    }
}

// Custom task to compile Go code to an AAR library
tasks.register("buildGoMobile") {
    val outputAar = file("libs/syncthing-socket.aar")
    val goMobileDir = rootProject.projectDir.parentFile.resolve("mobile")

    inputs.dir(goMobileDir)
    outputs.file(outputAar)

    doLast {
        if (!file("libs").exists()) {
            file("libs").mkdirs()
        }
        
        println("Executing gomobile bind...")
        val stdout = ByteArrayOutputStream()
        val stderr = ByteArrayOutputStream()
        
        try {
            exec {
                // Ensure the gomobile binary is available in PATH or run it explicitly.
                // Assuming gomobile is installed in ~/go/bin or in PATH.
                val goBin = System.getenv("HOME") + "/go/bin/gomobile"
                var cmd = "gomobile"
                if (file(goBin).exists()) {
                    cmd = goBin
                }
                commandLine(cmd, "bind", "-target=android", "-androidapi", "26", "-v", "-ldflags=-checklinkname=0", "-o", outputAar.absolutePath, "syncthing-socket/mobile")
                workingDir(rootProject.projectDir.parentFile)
                standardOutput = stdout
                errorOutput = stderr
            }
            println(stdout.toString())
        } catch (e: Exception) {
            println(stderr.toString())
            throw GradleException("Failed to run gomobile bind: ${e.message}")
        }
    }
}

// Ensure GoMobile runs before compilation
tasks.whenTaskAdded {
    if (name.startsWith("preBuild") || name.startsWith("compile")) {
        dependsOn("buildGoMobile")
    }
}

dependencies {
    implementation(fileTree("libs") { include("*.jar", "*.aar") })
    
    implementation("androidx.core:core-ktx:1.12.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.6.2")
    implementation("androidx.activity:activity-compose:1.8.0")
    implementation(platform("androidx.compose:compose-bom:2023.03.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    
    // Biometric prompt
    implementation("androidx.biometric:biometric:1.1.0")

    // Security Crypto for EncryptedSharedPreferences
    implementation("androidx.security:security-crypto:1.1.0-alpha06")

    // CameraX for QR Code scanning
    val camerax_version = "1.3.0"
    implementation("androidx.camera:camera-core:${camerax_version}")
    implementation("androidx.camera:camera-camera2:${camerax_version}")
    implementation("androidx.camera:camera-lifecycle:${camerax_version}")
    implementation("androidx.camera:camera-view:${camerax_version}")

    // Barcode scanning (ML Kit)
    implementation("com.google.mlkit:barcode-scanning:17.2.0")

    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.5.1")
    androidTestImplementation(platform("androidx.compose:compose-bom:2023.03.00"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
