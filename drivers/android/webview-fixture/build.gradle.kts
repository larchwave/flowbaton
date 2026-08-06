// A debuggable WebView, and nothing else.
//
// The DevTools WebView path (specs/02-device-drivers.md:44) uses an abstract
// socket created by WebView.setWebContentsDebuggingEnabled(true). This fixture
// is never shipped and is not a dependency of :agent or :core.
plugins {
    id("com.android.application") version "8.3.2"
}

android {
    namespace = "dev.nohavewho.flowbaton.fixture"
    compileSdk = 34

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        applicationId = "dev.nohavewho.flowbaton.fixture"
        minSdk = 26
        targetSdk = 34
        versionCode = 1
        versionName = "0.0.1"
    }
}
