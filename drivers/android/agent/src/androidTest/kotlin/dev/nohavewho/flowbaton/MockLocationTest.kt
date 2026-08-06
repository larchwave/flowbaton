package dev.nohavewho.flowbaton

import android.app.Instrumentation
import android.content.Context
import android.location.Location
import android.location.LocationManager
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Connected coverage for specs/02-device-drivers.md line 54: setLocation mocks
 * fused and LocationManager providers. An app reading through the fused path
 * must see the mock coordinate, not the device's real location.
 */
@RunWith(AndroidJUnit4::class)
class MockLocationTest {
    @Test
    fun mocksTheFusedProviderAlongsideGpsAndNetwork() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        allowMockLocation(instrumentation)
        val context = instrumentation.targetContext
        val manager = context.getSystemService(Context.LOCATION_SERVICE) as LocationManager
        val engine = MockLocationEngine(context)

        // Coordinates unique to this run: a stale fix from an earlier run, or an
        // organic GmsCore fusion of older mocks, must not pass for this push.
        val latitude = 37.0 + (System.currentTimeMillis() % 100_000) * 1e-7
        val longitude = -122.0 - (System.currentTimeMillis() % 90_000) * 1e-7
        val providers =
            listOf(LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER, "fused")

        engine.enableProviders()
        try {
            // The registration is what the platform can prove: setTestProviderLocation
            // refuses any provider nobody registered as a test provider, so a fused
            // provider left unregistered fails here — the readback alone cannot catch
            // it, because the platform fuses the gps mock into "fused" on its own.
            providers.forEach { provider ->
                manager.setTestProviderLocation(
                    provider,
                    Location(provider).apply {
                        this.latitude = latitude
                        this.longitude = longitude
                        accuracy = 1f
                        time = System.currentTimeMillis()
                        elapsedRealtimeNanos = SystemClock.elapsedRealtimeNanos()
                    },
                )
            }
            engine.push(latitude, longitude)
            // Reading a location back needs ACCESS_FINE_LOCATION, which the agent
            // itself never needs; the shell's identity covers only the readback.
            instrumentation.uiAutomation.adoptShellPermissionIdentity()
            try {
                providers.forEach { provider ->
                    val location = manager.getLastKnownLocation(provider)
                    assertNotNull("no mock location on $provider", location)
                    assertEquals("latitude on $provider", latitude, location!!.latitude, 1e-9)
                    assertEquals("longitude on $provider", longitude, location.longitude, 1e-9)
                }
            } finally {
                instrumentation.uiAutomation.dropShellPermissionIdentity()
            }
        } finally {
            engine.disable()
        }
    }

    private fun allowMockLocation(instrumentation: Instrumentation) {
        val descriptor =
            instrumentation.uiAutomation.executeShellCommand(
                "appops set ${instrumentation.targetContext.packageName} " +
                    "android:mock_location allow",
            )
        ParcelFileDescriptor.AutoCloseInputStream(descriptor).use { it.readBytes() }
    }
}
