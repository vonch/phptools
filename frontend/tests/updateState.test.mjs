import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";
import * as vue from "vue";
import { createUpdateChecker } from "../src/updateCheck.ts";
import { isActiveTask } from "../src/types.ts";

const { outputText } = ts.transpileModule(readFileSync(new URL("../src/updateState.ts", import.meta.url), "utf8"), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
});
const release = {
  configured: true, currentVersion: "1.0.0", latestVersion: "1.0.1",
  available: true, error: "", checkedAt: "2026-10-21T00:00:00Z",
};

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function load(t, methods = {}) {
  const snapshot = vue.ref({ settings: {}, updateInfo: release });
  const tasks = vue.ref([]);
  const uiActive = vue.ref(true);
  const state = vue.reactive({ connectionError: "" });
  const api = { CheckForUpdate: async () => release, ...methods };
  const imports = {
    vue, "./api": { api },
    "./state": { snapshot, tasks, uiActive, state, errorText: String, refreshAfterAction: async () => {} },
    "./types": { isActiveTask },
    "./updateCheck": { createUpdateChecker },
  };
  const exports = {};
  new Function("require", "exports", outputText)((name) => {
    assert.ok(name in imports, `Unexpected update-state dependency: ${name}`);
    return imports[name];
  }, exports);
  const stop = exports.startAppUpdateChecks();
  t.after(stop);
  return { ...exports, snapshot, tasks, uiActive, state, api };
}

async function settle() {
  for (let n = 0; n < 12; n++) await Promise.resolve();
  await vue.nextTick();
}

test("repeated upgrade actions create one task and remain disabled while it runs", async (t) => {
  const request = deferred();
  let starts = 0;
  const current = load(t, { InstallUpdate: () => { starts++; return request.promise; } });
  await settle();
  const first = current.installAppUpdate();
  await current.installAppUpdate();
  assert.equal(starts, 1);
  assert.equal(current.updateInProgress.value, true);
  current.tasks.value = [{ id: "update-1", action: "app-update", state: "running" }];
  request.resolve("update-1");
  assert.equal(await first, "update-1");
  assert.equal(current.updateStarting.value, false);
  assert.equal(current.updateInProgress.value, true);
  await current.installAppUpdate();
  assert.equal(starts, 1);
});

test("a rejected upgrade exposes the failure and permits retry", async (t) => {
  let starts = 0;
  const current = load(t, { InstallUpdate: async () => {
    if (++starts === 1) throw new Error("磁盘空间不足");
    return "update-2";
  } });
  await settle();
  await assert.rejects(current.installAppUpdate(), /磁盘空间不足/);
  assert.equal(current.updateBusy.value, false);
  assert.match(current.updateOperationError.value, /磁盘空间不足/);
  assert.equal(current.updateAvailable.value, true);
  assert.equal(await current.installAppUpdate(), "update-2");
  assert.equal(current.updateOperationError.value, "");
});

test("failed checks cannot install a release from a previous successful check", async (t) => {
  let starts = 0;
  const current = load(t, { InstallUpdate: async () => { starts++; return "unexpected"; } });
  await settle();
  assert.equal(current.updateAvailable.value, true);
  current.api.CheckForUpdate = async () => { throw new Error("更新源不可用"); };
  await current.checkAppUpdate();
  assert.equal(current.updateAvailable.value, false);
  assert.match(current.updateCheckError.value, /更新源不可用/);
  await current.installAppUpdate();
  assert.equal(starts, 0);
});

test("only the active upgrade task is cancelled and repeated clicks are ignored", async (t) => {
  const cancelled = [];
  const request = deferred();
  const current = load(t, { CancelTask: (id) => { cancelled.push(id); return request.promise; } });
  await settle();
  current.tasks.value = [
    { id: "install-other", action: "install", state: "running" },
    { id: "update-3", action: "app-update", state: "running" },
    { id: "update-old", action: "app-update", state: "failed" },
  ];
  const first = current.cancelAppUpdate();
  await current.cancelAppUpdate();
  assert.deepEqual(cancelled, ["update-3"]);
  request.resolve();
  await first;
  assert.equal(current.updateCancelling.value, false);
});

test("snapshot refreshes and hidden checks do not schedule extra network requests", async (t) => {
  let checks = 0;
  const current = load(t, { CheckForUpdate: async () => { checks++; return release; } });
  await settle();
  assert.equal(checks, 1);
  for (let n = 0; n < 3; n++) {
    current.snapshot.value = { ...current.snapshot.value, updateInfo: { ...release } };
    await settle();
  }
  assert.equal(checks, 1);
  current.uiActive.value = false;
  await settle();
  await current.checkAppUpdate();
  assert.equal(checks, 1);
});
