import { computed } from "vue";
import { useRoute, useRouter, type RouteLocationRaw } from "vue-router";

/**
 * Resolve where a detail/edit page should navigate "back" to.
 *
 * List pages pass their current `fullPath` as a `?from=…` query param when
 * opening an item (e.g. `/invoices/:id/edit?from=/invoices%3Ftab%3D1`), so the
 * back action — and post-save redirect — returns to the exact list state,
 * including the active status tab. Falls back to `fallback` when there is no
 * `from` (direct link, page refresh, or navigation from somewhere that did not
 * set it).
 */
export function useReturnTo(fallback: RouteLocationRaw) {
  const route = useRoute();
  const router = useRouter();

  const backTo = computed<RouteLocationRaw>(() => {
    const from = route.query.from;
    // Only honour internal absolute paths to avoid open-redirects.
    return typeof from === "string" && from.startsWith("/") ? from : fallback;
  });

  function goBack() {
    router.push(backTo.value);
  }

  return { backTo, goBack };
}
