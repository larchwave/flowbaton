plugins {
    id("com.android.application") version "8.3.2"
    id("org.jetbrains.kotlin.android") version "1.9.22"
}

android {
    namespace = "dev.larchwave.flowbaton"
    compileSdk = 34

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        applicationId = "dev.larchwave.flowbaton"
        minSdk = 26
        targetSdk = 34
        versionCode = 1
        versionName = "0.0.1-g001"

        testApplicationId = "dev.larchwave.flowbaton.test"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    packaging {
        resources {
            excludes += setOf(
                "META-INF/AL2.0",
                "META-INF/LGPL2.1",
                "META-INF/INDEX.LIST",
                "META-INF/io.netty.versions.properties",
            )
        }
    }

}

dependencies {
    implementation(project(":core"))

    androidTestImplementation("androidx.test:runner:1.5.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
}

dependencyLocking {
    lockAllConfigurations()
}
