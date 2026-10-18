plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.github.muelli.syncthingsocket"
    compileSdk = 34

    defaultConfig {
        // One app instance unlocks one computer. To unlock a second computer, build a
        // second APK with its own application id, which Android then installs alongside
        // the first instead of replacing it. See "Unlocking more than one computer" in
        // README.md. `namespace` deliberately stays fixed: it names the code, not the
        // installed app.
        applicationId = (findProperty("syncthingSocket.appId") as String?)
            ?: "com.github.muelli.syncthingsocket"

        // The launcher label lives here rather than in strings.xml so a variant build can
        // override it, which is what makes two installed instances tellable apart.
        resValue(
            "string",
            "app_name",
            (findProperty("syncthingSocket.appLabel") as String?) ?: "Syncthing LUKS"
        )

        minSdk = 26
        targetSdk = 34
        
        // versionCode must stay monotonic even when built from a release
        // source tarball that has no .git directory (F-Droid's build
        // infrastructure does this). Preference order:
        //   1. `git rev-list --count HEAD` when a repo is present.
        //   2. A committed VERSION file at the repo root. build.yml writes
        //      one into the CLI source tarball before archiving it
        //      (see build-cli's "Build Binaries" step), and scripts/version.sh
        //      accepts the same file as its own fallback, so a tag-built
        //      tarball always carries a matching VERSION.
        //   3. 1, only if neither source is available.
        val repoRoot = rootProject.projectDir.parentFile

        val gitVersionCount = try {
            providers.exec {
                commandLine("git", "rev-list", "--count", "HEAD")
                workingDir = repoRoot
                isIgnoreExitValue = true
            }.standardOutput.asText.get().trim().toIntOrNull()
        } catch (e: Exception) { null }

        val versionFile = repoRoot.resolve("VERSION")
        val fileVersionCount = if (versionFile.exists()) {
            // Contents are whatever scripts/version.sh printed: "vN" on a
            // release tag, "N" or "N-dirty" otherwise.
            versionFile.readText().trim()
                .removePrefix("v")
                .removeSuffix("-dirty")
                .toIntOrNull()
        } else null

        // Deliberately no numeric default. versionCode is how Android and F-Droid decide
        // whether a build is an update; shipping 1 because git happened to be absent is
        // worse than not building at all, and it is silent. F-Droid builds from the source
        // tarball, which carries VERSION, so this only fires for an unpacked archive with
        // neither git nor VERSION, where the right answer is to say so.
        val appVersionCode = gitVersionCount ?: fileVersionCount ?: error(
            "Cannot determine versionCode: no git history and no VERSION file at the " +
                "repository root. Write the build's version number to VERSION, for " +
                "example: sh scripts/version.sh > VERSION"
        )

        // isIgnoreExitValue means a failed script yields an empty string rather than an
        // exception, so the catch alone is not enough: an APK shipped with an empty
        // versionName was what this produced before the fallback was added.
        val scriptVersionName = try {
            providers.exec {
                commandLine("sh", "scripts/version.sh")
                workingDir = repoRoot
                isIgnoreExitValue = true
            }.standardOutput.asText.get().trim()
        } catch (e: Exception) { "" }

        val gitVersionName = scriptVersionName.ifBlank {
            if (versionFile.exists()) versionFile.readText().trim() else ""
        }.ifBlank { appVersionCode.toString() }

        versionCode = appVersionCode
        versionName = gitVersionName
        
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables {
            useSupportLibrary = true
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    
    lint {
        abortOnError = false
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

// Compiles the Go side of the app into an AAR with gomobile.
//
// A typed Exec task rather than exec {} inside doLast: Gradle can then see its inputs and
// outputs, skip it when nothing changed, cache its configuration, and fail with the
// command's own output when gomobile exits non-zero.
//
// The inputs are every Go source the bind can compile, not just mobile/. mobile imports
// the root package, which imports discovery and identity, so watching only mobile/ let a
// change anywhere else ship inside a stale AAR. Watching all non-test Go files outside
// android/ errs towards an unnecessary rebuild, never towards a stale one, and needs no
// edit when a new package is added. go.mod and go.sum are inputs because a dependency bump
// changes the binary without touching a single .go file.
val goMobileAar = layout.projectDirectory.file("libs/syncthing-socket.aar")
val goMobileFlags = listOf(
    "-target=android", "-androidapi", "26", "-v", "-trimpath",
    "-ldflags=-s -w -checklinkname=0",
)

val buildGoMobile = tasks.register<Exec>("buildGoMobile") {
    description = "Builds libs/syncthing-socket.aar from the Go sources with gomobile bind."
    group = "build"

    val repoRoot = rootProject.projectDir.parentFile
    inputs.files(
        fileTree(repoRoot) {
            include("**/*.go", "go.mod", "go.sum")
            exclude("**/*_test.go", "android/**", "vendor/**", "local/**")
        }
    ).withPathSensitivity(PathSensitivity.RELATIVE).withPropertyName("goSources")
    inputs.property("gomobileFlags", goMobileFlags)
    outputs.file(goMobileAar).withPropertyName("aar")

    // Prefer a gomobile in ~/go/bin, where `go install` puts it, over whatever PATH offers.
    val home = providers.environmentVariable("HOME").orNull
    val installed = home?.let { File(it, "go/bin/gomobile") }
    executable = if (installed != null && installed.exists()) installed.absolutePath else "gomobile"
    args(listOf("bind") + goMobileFlags +
        listOf("-o", goMobileAar.asFile.absolutePath, "github.com/muelli/syncthing-socket/mobile"))
    workingDir(repoRoot)

    // A local, so the action captures a File rather than the build script.
    val libsDir = goMobileAar.asFile.parentFile
    doFirst { libsDir.mkdirs() }
}

// preBuild is the hook the Android Gradle Plugin provides for work that must precede every
// variant's compilation, so the AAR exists before anything resolves libs/.
tasks.named("preBuild") { dependsOn(buildGoMobile) }

dependencies {
    implementation(fileTree("libs") { include("*.jar", "*.aar") })
    
    implementation("androidx.core:core-ktx:1.12.0")
    implementation("androidx.fragment:fragment-ktx:1.6.2")
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

    // Barcode scanning (FLOSS ZXing)
    implementation("com.google.zxing:core:3.5.3")

    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.5.1")
    androidTestImplementation(platform("androidx.compose:compose-bom:2023.03.00"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
