package dev.nohavewho.flowbaton.driver;

/**
 * Window order for the hierarchy dump.
 *
 * <p>UiAutomation does not guarantee a stable window order. Selection follows tree order, so an
 * unstable platform order would make identical selectors resolve inconsistently.
 *
 * <p>Ordering by ascending layer is deterministic and puts the app under test ahead of overlays.
 */
public final class WindowOrder {
    private WindowOrder() {}

    /**
     * Returns indices into {@code layers}, ordered by ascending layer. Windows sharing a layer keep
     * the order the platform gave them.
     */
    public static int[] ascendingLayerOrder(int[] layers) {
        int[] order = new int[layers.length];
        for (int index = 0; index < layers.length; index++) {
            order[index] = index;
        }
        // Insertion sort: the window count is a handful, and it is stable, which is what keeps
        // equal layers in platform order.
        for (int index = 1; index < order.length; index++) {
            int candidate = order[index];
            int position = index - 1;
            while (position >= 0 && layers[order[position]] > layers[candidate]) {
                order[position + 1] = order[position];
                position--;
            }
            order[position + 1] = candidate;
        }
        return order;
    }
}
