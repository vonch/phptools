interface ActivityUpdatesOptions {
  document: Pick<Document, "hidden" | "addEventListener" | "removeEventListener">;
  onWindowActivity: (callback: (active: boolean) => void) => (() => void) | undefined;
  onStateChanged: (callback: () => void) => (() => void) | undefined;
  setInterval: (callback: () => void, milliseconds: number) => number;
  clearInterval: (timer: number) => void;
  refresh: () => Promise<void>;
  setActive: (active: boolean) => void;
}

export function startActivityUpdates(options: ActivityUpdatesOptions): () => void {
  let windowActive = true;
  let active: boolean | undefined;
  let timer: number | undefined;
  let stopped = false;

  function updateActivity() {
    if (stopped) return;
    const next = windowActive && !options.document.hidden;
    if (next === active) return;
    active = next;
    options.setActive(next);
    if (timer !== undefined) {
      options.clearInterval(timer);
      timer = undefined;
    }
    if (next) {
      void options.refresh();
      timer = options.setInterval(() => {
        if (active && !stopped) void options.refresh();
      }, 1500);
    }
  }

  options.document.addEventListener("visibilitychange", updateActivity);
  const stopWindowActivity = options.onWindowActivity((value) => {
    windowActive = value;
    updateActivity();
  });
  const stopStateChanged = options.onStateChanged(() => {
    if (active && !stopped) void options.refresh();
  });
  updateActivity();

  return () => {
    stopped = true;
    if (timer !== undefined) options.clearInterval(timer);
    options.document.removeEventListener("visibilitychange", updateActivity);
    stopWindowActivity?.();
    stopStateChanged?.();
    options.setActive(false);
  };
}
