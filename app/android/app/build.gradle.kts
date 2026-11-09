import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application") version "8.13.2"
    id("org.jetbrains.kotlin.android") version "2.2.21"
    id("org.jetbrains.kotlin.plugin.compose") version "2.2.21"
}

android {
    namespace = "org.lumenfolio.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "org.lumenfolio.android"
        minSdk = 26
        targetSdk = 36
        versionCode = providers.gradleProperty("lumenfolioVersionCode").map { it.toInt() }.getOrElse(1)
        versionName = providers.gradleProperty("lumenfolioVersionName").getOrElse("0.1.0")
    }

    buildTypes {
        release {
            ndk {
                abiFilters.add("arm64-v8a")
            }
        }
    }

    buildFeatures {
        compose = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

androidComponents {
    onVariants(selector().withBuildType("release")) { variant ->
        variant.packaging.jniLibs.useLegacyPackaging.set(true)
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

val releaseAar = layout.buildDirectory.file("generated/mobilepanel/release/mobilepanel.aar")
val bindReleaseMobilePanel = tasks.register<Exec>("bindReleaseMobilePanel") {
    val bindingDir = rootProject.file("mobilepanel")
    workingDir(bindingDir)
    inputs.dir(bindingDir)
    inputs.files(rootProject.fileTree("../pc") {
        include("**/*.go", "go.mod", "go.sum", "panel/blue_noise_64.bin")
    })
    outputs.file(releaseAar)
    val aar = releaseAar.get().asFile
    val toolPath = providers.environmentVariable("PATH")
    commandLine(
        "gomobile", "bind", "-target=android/arm64", "-androidapi=26",
        "-javapkg=org.lumenfolio", "-ldflags=-s -w", "-o", aar.absolutePath, ".",
    )
    doFirst {
        // Gradle daemon 可能先于工具安装启动，从当前构建的 PATH 查找可执行文件。
        val path = toolPath.orNull.orEmpty()
        val name = if (File.separatorChar == '\\') "gomobile.exe" else "gomobile"
        val tool = path.split(File.pathSeparatorChar).map { File(it, name) }.firstOrNull { it.isFile }
            ?: error("PATH 中找不到 gomobile，请先在 mobilepanel 目录执行 go install tool")
        executable = tool.absolutePath
        environment("PATH", path)
        aar.parentFile.mkdirs()
    }
}

dependencies {
    debugImplementation(files("libs/mobilepanel.aar"))
    releaseImplementation(files(releaseAar).builtBy(bindReleaseMobilePanel))
    implementation("androidx.activity:activity-compose:1.11.0")
    implementation("androidx.activity:activity-ktx:1.11.0")
    implementation("androidx.compose.material3:material3:1.4.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.9.4")
}
