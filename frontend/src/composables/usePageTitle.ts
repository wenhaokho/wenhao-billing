import { onBeforeUnmount, ref, watch, type Ref } from "vue";

/**
 * A view's override of the topbar title, which App.vue otherwise takes from
 * the route name. null means "use the route's title".
 */
export const pageTitleOverride = ref<string | null>(null);
let owner: symbol | null = null;

/**
 * Sets the topbar title to `title` while the calling view is mounted; a null
 * value falls back to the route's title. Cleared when the view unmounts, so
 * the next page never inherits it, unless a newer view has set its own (the
 * next page's setup can run before this one unmounts).
 */
export function usePageTitle(title: Ref<string | null>) {
  const me = Symbol("page-title");
  watch(
    title,
    (t) => {
      owner = me;
      pageTitleOverride.value = t;
    },
    { immediate: true },
  );
  onBeforeUnmount(() => {
    if (owner === me) {
      owner = null;
      pageTitleOverride.value = null;
    }
  });
}
