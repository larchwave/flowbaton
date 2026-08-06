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

rootProject.name = "flowbaton-android-driver"

include(":core")
include(":agent")
// Development fixture for the DevTools WebView path; not part of the driver.
include(":webview-fixture")
