import { computed, reactive, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { api } from "./api";
import { startActivityUpdates } from "./activityUpdates";
import { isActiveTask, type Page, type Snapshot } from "./types";

export const state = reactive({
  page: "overview" as Page,
  snapshot: null as Snapshot | null,
  loading: true,
  connectionError: "",
  taskPanel: false,
  logTarget: null as { kind: string; id: string } | null,
});
export const snapshot = computed(() => state.snapshot);
export const packages = computed(() => state.snapshot?.packages ?? []);
export const instances = computed(() => state.snapshot?.instances ?? []);
export const sites = computed(() => state.snapshot?.sites ?? []);
export const tasks = computed(() => state.snapshot?.tasks ?? []);
export const activeTasks = computed(() => tasks.value.filter(isActiveTask));
export const running = computed(() =>
  instances.value.filter((instance) => instance.state === "running"),
);
export const uiActive = ref(!document.hidden);

let inFlight: Promise<void> | null = null;
export function refresh(): Promise<void> {
  if (inFlight) return inFlight;
  inFlight = api
    .GetState()
    .then((value) => {
      state.snapshot = {
        ...value,
        packages: value.packages ?? [],
        instances: value.instances ?? [],
        sites: value.sites ?? [],
        tasks: value.tasks ?? [],
        diagnostics: value.diagnostics ?? [],
      };
      state.connectionError = "";
    })
    .catch((error) => {
      state.connectionError = errorText(error);
    })
    .finally(() => {
      state.loading = false;
      inFlight = null;
    });
  return inFlight;
}

export async function refreshAfterAction(): Promise<void> {
  // A request started before an action finished may contain the previous state.
  if (inFlight) await inFlight;
  await refresh();
}

export function startUpdates() {
  return startActivityUpdates({
    document,
    onWindowActivity(callback) {
      try {
        return Events.On("window:activity", (event) => {
          callback(event.data.active);
        });
      } catch {
        /* A browser preview has no desktop connection. */
      }
    },
    onStateChanged(callback) {
      try {
        return Events.On("state:changed", callback);
      } catch {
        /* A browser preview has no desktop connection. */
      }
    },
    setInterval: (callback, milliseconds) =>
      window.setInterval(callback, milliseconds),
    clearInterval: (timer) => window.clearInterval(timer),
    refresh,
    setActive: (value) => {
      uiActive.value = value;
    },
  });
}

export function navigate(page: Page) {
  state.page = page;
}
export function viewLog(kind: string, id: string) {
  state.logTarget = { kind, id };
  navigate("logs");
}
export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
