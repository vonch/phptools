import { ref } from "vue";
import { useDialog, useMessage } from "naive-ui";
import { errorText, refreshAfterAction } from "./state";

export function useAction() {
  const message = useMessage();
  const dialog = useDialog();
  const busy = ref(false);
  async function run<T>(
    action: () => Promise<T>,
    success?: string,
  ): Promise<T | undefined> {
    busy.value = true;
    try {
      const value = await action();
      if (success) message.success(success);
      await refreshAfterAction();
      return value;
    } catch (error) {
      message.error(errorText(error), { duration: 7000 });
      return undefined;
    } finally {
      busy.value = false;
    }
  }
  function confirm(
    title: string,
    content: string,
    action: () => Promise<unknown>,
  ) {
    dialog.warning({
      title,
      content,
      positiveText: "确认",
      negativeText: "取消",
      onPositiveClick: () => run(action),
    });
  }
  return { busy, run, confirm, message };
}
