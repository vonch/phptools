import assert from "node:assert/strict";
import test from "node:test";
import { AUTO_UPDATE_CHECK_INTERVAL, createUpdateChecker } from "../src/updateCheck.ts";

const SOURCE = "https://updates.example.test/latest.json";
const release = { available: true, latestVersion: "1.2.0", error: "" };
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function setup(check = async () => release) {
  const results = [], errors = [], busy = [];
  let calls = 0, clock = 0, resets = 0;
  const controller = createUpdateChecker({
    now: () => clock,
    check: () => { calls++; return check(); },
    onResult: (result) => results.push(result),
    onError: (error) => errors.push(String(error)),
    onBusy: (value) => busy.push(value),
    onSourceChanged: () => { resets++; },
  });
  return {
    controller, results, errors, busy,
    advance: (duration) => { clock += duration; },
    get calls() { return calls; },
    get resets() { return resets; },
  };
}
async function settle() {
  for (let n = 0; n < 8; n++) await Promise.resolve();
}

test("unconfigured or hidden startup performs no network request", async () => {
  const state = setup();
  state.controller.setContext(true, "");
  await state.controller.checkNow();
  state.controller.setContext(false, SOURCE);
  await state.controller.checkNow();
  await settle();
  assert.equal(state.calls, 0);
  state.controller.setContext(true, SOURCE);
  await settle();
  assert.equal(state.calls, 1);
  assert.deepEqual(state.results, [release]);
  state.controller.stop();
});

test("foreground startup and duplicate changes share one check", async () => {
  const request = deferred();
  const state = setup(() => request.promise);
  state.controller.setContext(true, SOURCE);
  state.controller.setContext(true, SOURCE);
  const manual = state.controller.checkNow();
  const secondManual = state.controller.checkNow();
  assert.equal(manual, secondManual);
  await settle();
  assert.equal(state.calls, 1);
  request.resolve(release);
  await manual;
  assert.deepEqual(state.busy, [true, false]);
  state.controller.stop();
});

test("repeated show and hide within 30 seconds checks once", async () => {
  const state = setup();
  state.controller.setContext(true, SOURCE);
  await settle();
  for (let n = 0; n < 4; n++) {
    state.controller.setContext(false, SOURCE);
    state.advance(5000);
    await settle();
    assert.equal(state.calls, 1);
    state.controller.setContext(true, SOURCE);
    await settle();
    assert.equal(state.calls, 1);
  }
  state.controller.setContext(false, SOURCE);
  state.advance(AUTO_UPDATE_CHECK_INTERVAL - 20000);
  await settle();
  assert.equal(state.calls, 1);
  state.controller.setContext(true, SOURCE);
  await settle();
  assert.equal(state.calls, 2);
  state.controller.stop();
});

test("a failed check is an error and is throttled until a manual retry", async () => {
  let failing = true;
  const state = setup(async () => {
    if (failing) throw new Error("network unavailable");
    return release;
  });
  state.controller.setContext(true, SOURCE);
  await settle();
  assert.equal(state.calls, 1);
  assert.equal(state.results.length, 0);
  assert.match(state.errors[0], /network unavailable/);
  state.controller.setContext(false, SOURCE);
  state.controller.setContext(true, SOURCE);
  await settle();
  assert.equal(state.calls, 1);
  failing = false;
  await state.controller.checkNow();
  assert.equal(state.calls, 2);
  assert.deepEqual(state.results, [release]);
  state.controller.stop();
});

test("changing the source drops stale results and checks the new source after completion", async () => {
  const requests = [deferred(), deferred()];
  let next = 0;
  const state = setup(() => requests[next++].promise);
  state.controller.setContext(true, SOURCE);
  await settle();
  state.controller.setContext(true, "https://another.example.test/latest.json");
  assert.equal(state.calls, 1);
  requests[0].resolve({ ...release, latestVersion: "99.0.0" });
  await settle();
  assert.equal(state.results.length, 0);
  assert.equal(state.calls, 2);
  requests[1].resolve(release);
  await settle();
  assert.deepEqual(state.results, [release]);
  assert.equal(state.resets, 2);
  state.controller.stop();
});

test("clearing the source discards an outstanding result and stops checks", async () => {
  const request = deferred();
  const state = setup(() => request.promise);
  state.controller.setContext(true, SOURCE);
  await settle();
  state.controller.setContext(true, "");
  request.resolve(release);
  await settle();
  await state.controller.checkNow();
  assert.equal(state.calls, 1);
  assert.deepEqual(state.results, []);
  state.controller.stop();
});

test("an error field in a backend result is never treated as a successful check", async () => {
  const state = setup(async () => ({ ...release, available: false, error: "invalid manifest" }));
  state.controller.setContext(true, SOURCE);
  await settle();
  assert.equal(state.results.length, 0);
  assert.match(state.errors[0], /invalid manifest/);
  state.controller.stop();
});

test("unmount ignores outstanding results and prevents future checks", async () => {
  const request = deferred();
  const state = setup(() => request.promise);
  state.controller.setContext(true, SOURCE);
  await settle();
  state.controller.stop();
  request.resolve(release);
  await settle();
  state.advance(AUTO_UPDATE_CHECK_INTERVAL);
  state.controller.setContext(false, SOURCE);
  state.controller.setContext(true, SOURCE);
  await state.controller.checkNow();
  assert.equal(state.calls, 1);
  assert.deepEqual(state.results, []);
  assert.deepEqual(state.busy, [true, false]);
});
