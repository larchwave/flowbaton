package dev.larchwave.flowbaton.driver;

import static org.junit.Assert.assertTrue;

import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.regex.Pattern;
import org.junit.Test;

public final class AndroidProjectConfigTest {
    @Test
    public void targetManifestAndInstrumentationConfigDeclareNetworkServerRequirements()
            throws Exception {
        String manifest =
                Files.readString(
                        Path.of("..", "agent", "src", "main", "AndroidManifest.xml"));
        assertTrue(
                "target manifest must grant INTERNET to the instrumentation process",
                manifest.contains(
                        "<uses-permission android:name=\"android.permission.INTERNET\" />"));

        String build = Files.readString(Path.of("..", "agent", "build.gradle.kts"));
        for (String token :
                new String[] {
                    "applicationId = \"dev.larchwave.flowbaton\"",
                    "testApplicationId = \"dev.larchwave.flowbaton.test\"",
                    "testInstrumentationRunner = \"androidx.test.runner.AndroidJUnitRunner\""
                }) {
            assertTrue("missing Android configuration token: " + token, build.contains(token));
        }
    }

    @Test
    public void wrapperDependenciesAndCiArePinnedAndAuditable() throws Exception {
        Path wrapperJar = Path.of("..", "gradle", "wrapper", "gradle-wrapper.jar");
        assertTrue("Gradle wrapper JAR must be checked in", Files.isRegularFile(wrapperJar));
        assertTrue(
                "unexpected Gradle wrapper JAR",
                sha256(wrapperJar)
                        .equals(
                                "d3b261c2820e9e3d8d639ed084900f11f4a86050a8f83342ade7b6bc9b0d2bdd"));

        Path verificationMetadata = Path.of("..", "gradle", "verification-metadata.xml");
        assertTrue(
                "dependency verification metadata must be checked in",
                Files.isRegularFile(verificationMetadata) && Files.size(verificationMetadata) > 0);
        assertTrue(
                "core dependency lock must be checked in",
                Files.isRegularFile(Path.of("gradle.lockfile")));
        assertTrue(
                "agent dependency lock must be checked in",
                Files.isRegularFile(Path.of("..", "agent", "gradle.lockfile")));

        String wrapper =
                Files.readString(
                        Path.of("..", "gradle", "wrapper", "gradle-wrapper.properties"));
        assertTrue(wrapper.contains("gradle-8.5-bin.zip"));
        assertTrue(
                wrapper.contains(
                        "distributionSha256Sum=9d926787066a081739e8200858338b4a69e837c3a821a33aca9db09dd4a41026"));

        String coreBuild = Files.readString(Path.of("build.gradle.kts"));
        String agentBuild = Files.readString(Path.of("..", "agent", "build.gradle.kts"));
        for (String token :
                new String[] {
                    "io.grpc:grpc-netty-shaded:1.81.0",
                    "com.android.tools.build:gradle:8.3.2",
                    "org.jetbrains.kotlin:kotlin-gradle-plugin:1.9.22",
                    "androidx.test:runner:1.5.2",
                    "androidx.test.ext:junit:1.1.5"
                }) {
            assertTrue(
                    "dependency audit does not pin " + token,
                    coreBuild.contains(token) || agentBuild.contains(token));
        }

        String ci =
                Files.readString(
                        Path.of("..", "..", "..", ".github", "workflows", "ci.yml"));
        for (String token :
                new String[] {
                    "platforms;android-34",
                    "--dependency-verification strict",
                    ":core:test",
                    ":agent:assembleDebugAndroidTest"
                }) {
            assertTrue("Android CI does not contain " + token, ci.contains(token));
        }
        // The policy is an immutable commit pin, not one frozen revision
        // (docs/dependency-policy.md). Asserting the exact SHA turned every
        // accepted Dependabot bump into a red Android job.
        for (String action : new String[] {"actions/setup-java", "android-actions/setup-android"}) {
            assertTrue(
                    "Android CI must pin " + action + " to a full commit SHA",
                    Pattern.compile(
                                    "uses:\\s+"
                                            + Pattern.quote(action)
                                            + "@[0-9a-f]{40}(\\s|$)",
                                    Pattern.MULTILINE)
                            .matcher(ci)
                            .find());
        }
    }

    private static String sha256(Path path) throws Exception {
        MessageDigest digest = MessageDigest.getInstance("SHA-256");
        return HexFormat.of().formatHex(digest.digest(Files.readAllBytes(path)));
    }
}
