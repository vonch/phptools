import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { compileScript, parse } from "@vue/compiler-sfc";
import ts from "typescript";
import * as vue from "vue";

const { descriptor } = parse(readFileSync(new URL("../src/views/Health.vue", import.meta.url), "utf8"));
const { outputText } = ts.transpileModule(compileScript(descriptor, { id: "health-repair-test" }).content, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
});

function mount(t, api) {
  const exports = {};
  const activeTasks = vue.ref([]);
  const snapshot = vue.ref({ settings: { root: "test-runtime" }, instances: [{ id: "instance-1" }] });
  const errors = [];
  const busy = vue.ref(false);
  const imports = {
    vue,
    "naive-ui": {},
    "@vicons/ionicons5": {},
    "../api": { api },
    "../components/RuntimeDependencies.vue": { default: {} },
    "../types": { progressPercent: () => 0 },
    "../state": { activeTasks, snapshot, state: {}, navigate() {}, viewLog() {} },
    "../composables": { useAction: () => ({ busy, run: async (action) => {
      busy.value = true;
      try { return await action(); }
      catch (error) { errors.push(String(error)); }
      finally { busy.value = false; }
    } }) },
  };
  new Function("require", "exports", outputText)((name) => {
    assert.ok(name in imports, `Unexpected Health dependency: ${name}`);
    return imports[name];
  }, exports);
  const component = exports.default;
  const setup = component.setup;
  let state;
  component.setup = (props, context) => { state = setup(props, context); return state; };
  component.render = () => null;
  const app = vue.createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null }).createApp(component);
  app.mount({});
  t.after(() => app.unmount());
  return { state, activeTasks, errors, snapshot };
}

const item = { id: "instance-1:logs", targetId: "instance-1", repairId: "instance-1:logs", repairable: true, status: "warning" };

test("repair sends the selected report ID once and blocks duplicate clicks", async (t) => {
  const calls = [];
  let finish;
  const { state } = mount(t, { RepairHealthItem: (id) => {
    calls.push(id);
    return new Promise((resolve) => { finish = resolve; });
  } });
  state.repair(item);
  state.repair(item);
  assert.deepEqual(calls, ["instance-1:logs"]);
  assert.equal(state.repairing.value, "instance-1:logs");
  finish("repair-task");
  await vue.nextTick();
  await vue.nextTick();
  assert.equal(state.repairing.value, null);
});

test("in-progress check or repair disables new repair dispatch", (t) => {
  let calls = 0;
  const { state, activeTasks } = mount(t, { RepairHealthItem: async () => { calls++; } });
  for (const action of ["environment-check", "environment-repair"]) {
    activeTasks.value = [{ id: "busy-task", action, targetId: item.id }];
    state.repair(item);
    assert.equal(state.detectionTask.value.id, "busy-task");
  }
  activeTasks.value = [];
  state.repair({ ...item, repairable: false });
  assert.equal(calls, 0);
});

test("repair failure clears busy state and keeps the error available for display", async (t) => {
  const { state, errors } = mount(t, { RepairHealthItem: async () => { throw new Error("检测结果已变化"); } });
  state.repair(item);
  await vue.nextTick();
  await vue.nextTick();
  assert.equal(state.repairing.value, null);
  assert.match(errors[0], /检测结果已变化/);
});

test("passing checks stay collapsed until expanded", (t) => {
  const { state, snapshot } = mount(t, {});
  snapshot.value = {
    ...snapshot.value,
    healthReport: { items: [
      { id: "port", status: "ok", check: "端口" },
      { id: "config", status: "error", check: "配置" },
    ] },
  };
  assert.equal(state.showPassed.value, false);
  assert.deepEqual(state.visibleItems.value.map((item) => item.id), ["config"]);
  state.showPassed.value = true;
  assert.deepEqual(state.visibleItems.value.map((item) => item.id), ["port", "config"]);
});

test("manual issues route to their relevant management page and offer known instance logs", (t) => {
  const { state } = mount(t, {});
  assert.equal(state.treatmentEntry({ id: "environment:hosts" }).page, "sites");
  assert.equal(state.treatmentEntry({ id: "site:site-1:config" }).page, "sites");
  assert.equal(state.treatmentEntry({ id: "site:site-1:php" }).page, "instances");
  assert.equal(state.treatmentEntry({ id: "site:site-1:nginx" }).page, "packages");
  assert.equal(state.treatmentEntry({ id: "instance-1:package" }).page, "packages");
  assert.equal(state.treatmentEntry({ id: "environment:root" }).page, "settings");
  assert.equal(state.treatmentEntry({ id: "instance-1:config-test" }).page, "instances");
  assert.equal(state.canViewLog({ targetId: "instance-1" }), true);
  assert.equal(state.canViewLog({ targetId: "site-1" }), false);
});
