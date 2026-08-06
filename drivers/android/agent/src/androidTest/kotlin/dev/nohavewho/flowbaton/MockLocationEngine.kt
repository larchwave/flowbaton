package dev.nohavewho.flowbaton

import android.content.Context
import android.location.Criteria
import android.location.Location
import android.location.LocationManager
import android.os.SystemClock
import android.util.Log
import java.util.Timer
import kotlin.concurrent.fixedRateTimer

/**
 * Mock-location plumbing behind setLocation / enableMockLocationProviders /
 * disableLocationUpdates: test providers on gps+network+fused (spec 02 line 54: "Fused +
 * LocationManager mock providers"), and a 2000ms repeating push (spec 04 §1). Requires the
 * app to hold the mock-location appop; without it the calls throw and the error contract
 * carries that to the host.
 */
class MockLocationEngine(private val context: Context) {
    private val providers =
        listOf(
            LocationManager.GPS_PROVIDER,
            LocationManager.NETWORK_PROVIDER,
            // LocationManager.FUSED_PROVIDER spelled out: the constant is API 31,
            // minSdk is 26, and the provider name predates the constant. Without a
            // test provider under this name, apps on the fused path keep reading
            // the device's real location while setLocation reports success.
            // ponytail: play-services FusedLocationProviderClient.setMockMode is
            // not called (no play-services dependency); add it via the dependency
            // ritual if a GmsCore FLP client ever bypasses the platform mock.
            "fused",
        )
    private var timer: Timer? = null

    private val manager: LocationManager
        get() = context.getSystemService(Context.LOCATION_SERVICE) as LocationManager

    @Synchronized
    fun enableProviders() {
        providers.forEach { provider ->
            try {
                @Suppress("DEPRECATION")
                manager.addTestProvider(
                    provider,
                    false,
                    false,
                    false,
                    false,
                    true,
                    true,
                    true,
                    Criteria.POWER_LOW,
                    Criteria.ACCURACY_FINE,
                )
            } catch (_: IllegalArgumentException) {
                // Already registered from an earlier call; enabling below is what matters.
            }
            manager.setTestProviderEnabled(provider, true)
        }
    }

    @Synchronized
    fun push(latitude: Double, longitude: Double) {
        timer?.cancel()
        // The first push is synchronous so a broken mock setup fails the RPC itself,
        // not a background timer nobody hears about.
        pushOnce(latitude, longitude)
        timer =
            fixedRateTimer(
                name = "flowbaton-mock-location",
                initialDelay = 2_000,
                period = 2_000,
            ) {
                try {
                    pushOnce(latitude, longitude)
                } catch (failure: RuntimeException) {
                    Log.w("FlowBaton", "repeating mock location push failed", failure)
                }
            }
    }

    @Synchronized
    fun disable() {
        timer?.cancel()
        timer = null
        providers.forEach { provider ->
            try {
                manager.setTestProviderEnabled(provider, false)
                manager.removeTestProvider(provider)
            } catch (_: IllegalArgumentException) {
                // Never registered; nothing to disable.
            }
        }
    }

    private fun pushOnce(latitude: Double, longitude: Double) {
        providers.forEach { provider ->
            val location =
                Location(provider).apply {
                    this.latitude = latitude
                    this.longitude = longitude
                    accuracy = 1f
                    time = System.currentTimeMillis()
                    elapsedRealtimeNanos = SystemClock.elapsedRealtimeNanos()
                }
            manager.setTestProviderLocation(provider, location)
        }
    }
}
