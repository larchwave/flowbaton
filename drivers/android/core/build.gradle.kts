plugins {
    `java-library`
}

group = "dev.nohavewho.flowbaton"
version = "0.0.1-g001"

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(17))
    }
}

val androidToolingVerification by configurations.creating {
    isCanBeConsumed = false
    isCanBeResolved = true
    description = "Resolves pinned Android build/test tooling for checksum and lock metadata."
}

dependencies {
    api("io.grpc:grpc-api:1.81.0")
    implementation("io.grpc:grpc-netty-shaded:1.81.0")
    implementation("io.grpc:grpc-stub:1.81.0")

    testImplementation("junit:junit:4.13.2")

    androidToolingVerification("com.android.tools.build:gradle:8.3.2")
    androidToolingVerification("org.jetbrains.kotlin:kotlin-gradle-plugin:1.9.22")
    androidToolingVerification("androidx.test:runner:1.5.2")
    androidToolingVerification("androidx.test.ext:junit:1.1.5")
}

tasks.test {
    useJUnit()
}

dependencyLocking {
    lockAllConfigurations()
}

tasks.register("resolveAndroidToolingVerification") {
    inputs.files(androidToolingVerification)
}
