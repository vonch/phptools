import { computed, ref, watch } from "vue";
import { api } from "./api";
import { errorText, refreshAfterAction, snapshot, state, tasks, uiActive } from "./state";
import { isActiveTask, type UpdateInfo } from "./types";
import { createUpdateChecker } from "./updateCheck";

const checkedInfo = ref<UpdateInfo | null>(null);
const checkError = ref("");
export const updateChecking = ref(false);
export const updateStarting = ref(false);
export const updateCancelling = ref(false);
export const updateOperationError = ref("");
const builtinUpdateSource = "builtin";
export const updateSource = computed(() => builtinUpdateSource);
export const updateInfo = computed(() => checkedInfo.value ?? snapshot.value?.updateInfo ?? null);
export const updateCheckError = computed(() => checkError.value || updateInfo.value?.error || "");
export const latestUpdateTask = computed(() => tasks.value.find((task) => task.action === "app-update"));
export const updateTask = computed(() => tasks.value.find((task) => task.action === "app-update" && isActiveTask(task)));
export const updateInProgress = computed(() => updateStarting.value || !!updateTask.value);
export const updateAvailable = computed(() => !!updateInfo.value?.available && !updateCheckError.value);
export const updateBusy = computed(() =>
  updateChecking.value || updateInProgress.value || updateCancelling.value,
);

const checker = createUpdateChecker({
  check: () => api.CheckForUpdate(),
  onResult: (value) => {
    checkedInfo.value = value;
    checkError.value = "";
    void refreshAfterAction();
  },
  onError: (error) => {
    checkError.value = errorText(error);
    void refreshAfterAction();
  },
  onBusy: (value) => {
    updateChecking.value = value;
    if (value) checkError.value = "";
  },
  onSourceChanged: () => {
    checkedInfo.value = null;
    checkError.value = "";
    updateOperationError.value = "";
  },
});

export function startAppUpdateChecks() {
  const stop = watch(
    [uiActive, updateSource, () => state.connectionError, updateInProgress],
    ([active, source, connectionError, installing]) => {
      checker.setContext(active && !connectionError && !installing, source);
    },
    { immediate: true },
  );
  return () => {
    stop();
    checker.stop();
  };
}

export function checkAppUpdate() {
  if (updateBusy.value) return Promise.resolve(undefined);
  updateOperationError.value = "";
  return checker.checkNow();
}

export async function installAppUpdate() {
  if (updateBusy.value || !updateAvailable.value) return;
  updateStarting.value = true;
  updateOperationError.value = "";
  try {
    const id = await api.InstallUpdate();
    await refreshAfterAction();
    return id;
  } catch (error) {
    updateOperationError.value = errorText(error);
    throw error;
  } finally {
    updateStarting.value = false;
  }
}

export async function cancelAppUpdate() {
  const task = updateTask.value;
  if (!task || updateCancelling.value) return;
  updateCancelling.value = true;
  try {
    await api.CancelTask(task.id);
    await refreshAfterAction();
  } finally {
    updateCancelling.value = false;
  }
}
