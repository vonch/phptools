import assert from "node:assert/strict";
import test from "node:test";
import { startActivityUpdates } from "../src/activityUpdates.ts";

function setup({ hidden = false, initialActivity } = {}) {
  const document = new EventTarget();
  document.hidden = hidden;
  let windowActivity;
  let stateChanged;
  let refreshes = 0;
  let active;
  let windowUnsubscribed = false;
  let stateUnsubscribed = false;
  let nextTimer = 0;
  const timers = new Map();
  const stop = startActivityUpdates({
    document,
    onWindowActivity(callback) {
      windowActivity = callback;
      if (initialActivity !== undefined) callback(initialActivity);
      return () => { windowUnsubscribed = true; };
    },
    onStateChanged(callback) {
      stateChanged = callback;
      return () => { stateUnsubscribed = true; };
    },
    setInterval(callback, milliseconds) {
      assert.equal(milliseconds, 1500);
      const id = ++nextTimer;
      timers.set(id, callback);
      return id;
    },
    clearInterval(id) { timers.delete(id); },
    refresh() { refreshes++; return Promise.resolve(); },
    setActive(value) { active = value; },
  });
  return {
    get active() { return active; },
    get refreshes() { return refreshes; },
    get timerCount() { return timers.size; },
    get unsubscribed() { return windowUnsubscribed && stateUnsubscribed; },
    setWindowActive(value) { windowActivity(value); },
    setHidden(value) {
      document.hidden = value;
      document.dispatchEvent(new Event("visibilitychange"));
    },
    emitStateChanged() { stateChanged(); },
    tick() { for (const callback of timers.values()) callback(); },
    stop,
  };
}

test("visible startup refreshes immediately and keeps the 1.5 second poll", () => {
  const updates = setup();
  assert.equal(updates.active, true);
  assert.equal(updates.refreshes, 1);
  assert.equal(updates.timerCount, 1);
  updates.tick();
  updates.emitStateChanged();
  assert.equal(updates.refreshes, 3);
  updates.stop();
});

test("native hide stops polling and ignores state changes before document hide", () => {
  const updates = setup();
  updates.setWindowActive(false);
  assert.equal(updates.active, false);
  assert.equal(updates.timerCount, 0);
  updates.tick();
  updates.emitStateChanged();
  assert.equal(updates.refreshes, 1);
  updates.stop();
});

test("document hide pauses and becoming visible refreshes immediately", () => {
  const updates = setup();
  updates.setHidden(true);
  updates.emitStateChanged();
  assert.equal(updates.active, false);
  assert.equal(updates.timerCount, 0);
  assert.equal(updates.refreshes, 1);
  updates.setHidden(false);
  assert.equal(updates.active, true);
  assert.equal(updates.refreshes, 2);
  assert.equal(updates.timerCount, 1);
  updates.stop();
});

test("document visibility cannot override a native inactive window", () => {
  const updates = setup();
  updates.setWindowActive(false);
  updates.setHidden(true);
  updates.setHidden(false);
  assert.equal(updates.active, false);
  assert.equal(updates.refreshes, 1);
  assert.equal(updates.timerCount, 0);
  updates.setWindowActive(true);
  assert.equal(updates.active, true);
  assert.equal(updates.refreshes, 2);
  updates.stop();
});

test("native restore waits until the document is visible", () => {
  const updates = setup();
  updates.setWindowActive(false);
  updates.setHidden(true);
  updates.setWindowActive(true);
  assert.equal(updates.active, false);
  assert.equal(updates.refreshes, 1);
  updates.setHidden(false);
  assert.equal(updates.refreshes, 2);
  assert.equal(updates.timerCount, 1);
  updates.stop();
});

test("hidden startup does not start a timer or request state", () => {
  const updates = setup({ hidden: true });
  updates.emitStateChanged();
  assert.equal(updates.active, false);
  assert.equal(updates.refreshes, 0);
  assert.equal(updates.timerCount, 0);
  updates.setHidden(false);
  assert.equal(updates.refreshes, 1);
  updates.stop();
});

test("an initial inactive event is preserved while subscriptions are registered", () => {
  const updates = setup({ initialActivity: false });
  assert.equal(updates.active, false);
  assert.equal(updates.refreshes, 0);
  assert.equal(updates.timerCount, 0);
  updates.setWindowActive(true);
  assert.equal(updates.refreshes, 1);
  updates.stop();
});

test("duplicate restore and visibility events keep one poll timer", () => {
  const updates = setup();
  for (let i = 0; i < 5; i++) {
    updates.setWindowActive(true);
    updates.setHidden(false);
  }
  assert.equal(updates.refreshes, 1);
  assert.equal(updates.timerCount, 1);
  updates.setWindowActive(false);
  updates.setWindowActive(true);
  assert.equal(updates.refreshes, 2);
  assert.equal(updates.timerCount, 1);
  updates.stop();
});

test("cleanup removes subscriptions and timers, including queued callbacks", () => {
  const updates = setup();
  updates.stop();
  assert.equal(updates.active, false);
  assert.equal(updates.timerCount, 0);
  assert.equal(updates.unsubscribed, true);
  updates.setHidden(true);
  updates.setHidden(false);
  updates.setWindowActive(false);
  updates.setWindowActive(true);
  updates.emitStateChanged();
  updates.tick();
  assert.equal(updates.refreshes, 1);
  assert.equal(updates.timerCount, 0);
});
