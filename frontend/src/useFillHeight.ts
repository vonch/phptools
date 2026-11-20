import { onUnmounted, ref, watch } from "vue";

export function useFillHeight() {
  const frame = ref<HTMLElement | null>(null);
  const height = ref(0);
  let observer: ResizeObserver | null = null;
  watch(frame, (element) => {
    observer?.disconnect();
    observer = null;
    if (!element) {
      height.value = 0;
      return;
    }
    const update = () => {
      height.value = element.clientHeight;
    };
    update();
    observer = new ResizeObserver(update);
    observer.observe(element);
  });
  onUnmounted(() => observer?.disconnect());
  return { frame, height };
}
