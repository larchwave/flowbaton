package dev.nohavewho.flowbaton.driver;

import static org.junit.Assert.assertArrayEquals;

import org.junit.Test;

/**
 * The platform's window order is not stable, and selection follows tree order. Sorting by layer
 * keeps matching deterministic when both an app and an overlay contain eligible nodes.
 *
 * <p>Sorting by ascending layer pins it: the app under test (base layer 21000 on the emulator)
 * comes before the status bar (151000).
 */
public final class WindowOrderTest {
    @Test
    public void ordersWindowsBottomUpSoTheAppPrecedesOverlays() {
        assertArrayEquals(new int[] {1, 0}, WindowOrder.ascendingLayerOrder(new int[] {151000, 21000}));
        assertArrayEquals(
                new int[] {3, 2, 1, 0},
                WindowOrder.ascendingLayerOrder(new int[] {251000, 241000, 151000, 21000}));
    }

    @Test
    public void keepsPlatformOrderAmongWindowsSharingALayer() {
        // ScreenDecorOverlay, SecondaryHomeHandle and EdgeBackGestureHandler all sit at 251000 on
        // the emulator, so equal layers have to stay in the order the platform gave them.
        assertArrayEquals(
                new int[] {3, 0, 1, 2},
                WindowOrder.ascendingLayerOrder(new int[] {251000, 251000, 251000, 21000}));
    }

    @Test
    public void emptyInputIsEmptyOrder() {
        assertArrayEquals(new int[] {}, WindowOrder.ascendingLayerOrder(new int[] {}));
    }
}
