import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { compileScript, parse } from "@vue/compiler-sfc";
import ts from "typescript";
import * as vue from "vue";

const source = readFileSync(new URL("../src/views/Settings.vue", import.meta.url), "utf8");
const { descriptor } = parse(source);
const compiled = compileScript(descriptor, { id: "auto-start-test" });
const { outputText } = ts.transpileModule(compiled.content, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
});

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function mountSettings(t, api) {
  const errors = [];
  const successes = [];
  const warnings = [];
  const uiActive = vue.ref(true);
  const exports = {};
  const imports = {
    vue,
    "naive-ui": {},
    "@vicons/ionicons5": {},
    "../components/SidebarManager.vue": { default: {} },
    "../components/AppUpdates.vue": { default: {} },
    "../api": { api },
    "../composables": {
      useAction: () => ({
        run: (action) => action(),
        confirm() {},
        message: {
          error: (value) => errors.push(value),
          success: (value) => successes.push(value),
          warning: (value) => warnings.push(value),
        },
      }),
    },
    "../state": { errorText: (error) => String(error), snapshot: vue.ref(null), uiActive },
  };
  new Function("require", "exports", outputText)((name) => {
    assert.ok(name in imports, `Unexpected Settings dependency: ${name}`);
    return imports[name];
  }, exports);
  const component = exports.default;
  const setup = component.setup;
  let state;
  component.setup = (props, context) => { state = setup(props, context); return state; };
  component.render = () => null;
  const renderer = vue.createRenderer({
    createComment: () => ({}),
    insert() {},
    remove() {},
    parentNode: () => null,
    nextSibling: () => null,
  });
  const app = renderer.createApp(component);
  app.mount({});
  t.after(() => app.unmount());
  return { state, errors, successes, warnings, uiActive };
}

async function settle() {
  await Promise.resolve();
  await vue.nextTick();
}

test("a failed initial read remains unknown and can be retried", async (t) => {
  let calls = 0;
  const { state, errors } = mountSettings(t, {
    GetAutoStart: async () => {
      if (++calls === 1) throw new Error("读取被拒绝");
      return { enabled: true, conflict: "" };
    },
  });
  assert.equal(state.autoStartLoading.value, true);
  await settle();
  assert.equal(state.autoStartStatus.value, null);
  assert.match(state.autoStartError.value, /读取被拒绝/);
  assert.equal(errors.length, 1);
  await state.loadAutoStart();
  assert.equal(state.autoStartStatus.value.enabled, true);
  assert.equal(state.autoStartError.value, "");
});

test("saving waits for confirmation and ignores duplicate operations", async (t) => {
  const save = deferred();
  let writes = 0;
  let reads = 0;
  const { state, successes } = mountSettings(t, {
    GetAutoStart: async () => { reads++; return { enabled: false, conflict: "" }; },
    SetAutoStart: () => { writes++; return save.promise; },
  });
  await settle();
  const request = state.setAutoStart(true);
  assert.equal(state.autoStartStatus.value.enabled, false);
  assert.equal(state.autoStartBusy.value, true);
  await state.setAutoStart(true);
  await state.loadAutoStart();
  assert.equal(writes, 1);
  assert.equal(reads, 1);
  save.resolve({ enabled: true, conflict: "" });
  await request;
  assert.equal(state.autoStartStatus.value.enabled, true);
  assert.equal(state.autoStartBusy.value, false);
  assert.deepEqual(successes, ["已开启开机启动"]);
});

test("a rejected save keeps the old value and permits a later retry", async (t) => {
  let writes = 0;
  const { state, errors } = mountSettings(t, {
    GetAutoStart: async () => ({ enabled: true, conflict: "" }),
    SetAutoStart: async () => {
      if (++writes === 1) throw new Error("保存被拒绝");
      return { enabled: false, conflict: "" };
    },
  });
  await settle();
  await state.setAutoStart(false);
  assert.equal(state.autoStartStatus.value.enabled, true);
  assert.equal(state.autoStartSaving.value, false);
  assert.match(state.autoStartError.value, /保存被拒绝/);
  assert.equal(errors.length, 1);
  await state.setAutoStart(false);
  assert.equal(state.autoStartStatus.value.enabled, false);
  assert.equal(state.autoStartError.value, "");
});

test("an unknown or conflicting status cannot modify auto start", async (t) => {
  const load = deferred();
  let writes = 0;
  const { state } = mountSettings(t, {
    GetAutoStart: () => load.promise,
    SetAutoStart: async () => { writes++; return { enabled: true, conflict: "" }; },
  });
  await state.setAutoStart(true);
  assert.equal(writes, 0);
  load.resolve({ enabled: false, conflict: "另一个程序正在使用该启动项" });
  await settle();
  await state.setAutoStart(true);
  assert.equal(writes, 0);
  assert.match(state.autoStartStatus.value.conflict, /另一个程序/);
});

test("restoring the window rereads changes made outside the application", async (t) => {
  let enabled = false;
  let reads = 0;
  const { state, uiActive } = mountSettings(t, {
    GetAutoStart: async () => { reads++; return { enabled, conflict: "" }; },
  });
  await settle();
  assert.equal(reads, 1);
  uiActive.value = false;
  await settle();
  enabled = true;
  assert.equal(reads, 1);
  assert.equal(state.autoStartStatus.value.enabled, false);
  uiActive.value = true;
  await settle();
  assert.equal(reads, 2);
  assert.equal(state.autoStartStatus.value.enabled, true);
  uiActive.value = true;
  await settle();
  assert.equal(reads, 2);
});

for (const status of [
  { enabled: false, conflict: "请在 Windows 启动应用中启用 PHPTools" },
  { enabled: false, conflict: "" },
  { enabled: true, conflict: "Windows 未允许启动此应用" },
]) {
  test(`a save reports the returned state without false success: ${JSON.stringify(status)}`, async (t) => {
    const { state, successes, warnings } = mountSettings(t, {
      GetAutoStart: async () => ({ enabled: false, conflict: "" }),
      SetAutoStart: async () => status,
    });
    await settle();
    await state.setAutoStart(true);
    assert.deepEqual({ ...state.autoStartStatus.value }, status);
    assert.deepEqual(successes, []);
    assert.equal(warnings.length, 1);
    assert.match(warnings[0], status.conflict ? /Windows/ : /尚未生效/);
  });
}
