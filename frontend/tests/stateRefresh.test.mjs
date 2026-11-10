import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";
import * as vue from "vue";

const source = readFileSync(new URL("../src/state.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
});

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

function loadState(getState) {
  const exports = {};
  const imports = {
    vue,
    "@wailsio/runtime": { Events: {} },
    "./api": { api: { GetState: getState } },
    "./activityUpdates": { startActivityUpdates() {} },
    "./types": { isActiveTask: () => false },
  };
  new Function("require", "exports", "document", outputText)((name) => {
    assert.ok(name in imports, `Unexpected state dependency: ${name}`);
    return imports[name];
  }, exports, { hidden: false });
  return exports;
}

test("action refresh waits for the old snapshot and then fetches saved preferences", async () => {
  const oldRead = deferred();
  const savedRead = deferred();
  let reads = 0;
  const state = loadState(() => (++reads === 1 ? oldRead.promise : savedRead.promise));
  const oldRequest = state.refresh();
  const afterSave = state.refreshAfterAction();
  assert.equal(reads, 1);
  oldRead.resolve({ siteView: { pinnedSiteIds: [] } });
  await oldRequest;
  assert.equal(reads, 2);
  savedRead.resolve({ siteView: { pinnedSiteIds: ["saved-site"] } });
  await afterSave;
  assert.deepEqual(state.snapshot.value.siteView.pinnedSiteIds, ["saved-site"]);
});

test("regular polling continues sharing a single pending read", async () => {
  const pending = deferred();
  let reads = 0;
  const state = loadState(() => { reads++; return pending.promise; });
  const first = state.refresh();
  const second = state.refresh();
  assert.equal(first, second);
  assert.equal(reads, 1);
  pending.resolve({ siteView: { sortBy: "manual" } });
  await first;
});
