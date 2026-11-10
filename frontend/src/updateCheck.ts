import type { UpdateInfo } from "./types";

export const AUTO_UPDATE_CHECK_INTERVAL = 30 * 1000;

interface UpdateCheckerOptions {
  check: () => Promise<UpdateInfo>;
  onResult: (value: UpdateInfo) => void;
  onError: (error: unknown) => void;
  onBusy: (value: boolean) => void;
  onSourceChanged: () => void;
  now?: () => number;
}

// Each time the window is shown, check for an update. Showing and hiding
// again within 30 seconds shares that one request. No timer runs while hidden.
export function createUpdateChecker(options: UpdateCheckerOptions) {
  const now = options.now ?? Date.now;
  let source = "";
  let active = false;
  let stopped = false;
  let generation = 0;
  let attemptedAt: number | null = null;
  let inFlight: Promise<UpdateInfo | undefined> | null = null;

  function check(force = false): Promise<UpdateInfo | undefined> {
    if (stopped || !active || !source) return Promise.resolve(undefined);
    if (inFlight) return inFlight;
    if (!force && attemptedAt !== null && now() - attemptedAt < AUTO_UPDATE_CHECK_INTERVAL) {
      return Promise.resolve(undefined);
    }
    const requestGeneration = generation;
    attemptedAt = now();
    options.onBusy(true);
    inFlight = Promise.resolve()
      .then(options.check)
      .then((value) => {
        if (stopped || requestGeneration !== generation) return undefined;
        if (value.error) throw new Error(value.error);
        options.onResult(value);
        return value;
      })
      .catch((error: unknown) => {
        if (!stopped && requestGeneration === generation) options.onError(error);
        return undefined;
      })
      .finally(() => {
        inFlight = null;
        options.onBusy(false);
        // A changed source waits for the old request to finish so requests
        // cannot race against the backend's single update cache.
        if (!stopped && requestGeneration !== generation) void check();
      });
    return inFlight;
  }

  return {
    setContext(nextActive: boolean, nextSource: string) {
      if (stopped) return;
      nextSource = nextSource.trim();
      const changed = nextSource !== source;
      const restored = nextActive && !active;
      if (changed) {
        source = nextSource;
        generation++;
        attemptedAt = null;
        options.onSourceChanged();
      }
      active = nextActive;
      if (active && (changed || restored)) void check();
    },
    checkNow: () => check(true),
    stop() {
      stopped = true;
    },
  };
}
