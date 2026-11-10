import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { compileScript, parse } from "@vue/compiler-sfc";
import ts from "typescript";
import * as vue from "vue";

const { descriptor } = parse(readFileSync(new URL("../src/components/RuntimeDependencies.vue", import.meta.url), "utf8"));
const { outputText } = ts.transpileModule(compileScript(descriptor, { id: "dependency-test" }).content, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
});

function mount(t, api) {
  const exports = {};
  const imports = {
    vue, "naive-ui": {}, "../api": { api },
    "../state": { errorText: String },
    "../composables": { useAction: () => ({ busy: vue.ref(false), run: (action) => action() }) },
  };
  new Function("require", "exports", outputText)((name) => {
    assert.ok(name in imports, `Unexpected dependency: ${name}`);
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
  return state;
}

test("missing optional and required runtimes have distinct actionable labels", (t) => {
  const s = mount(t, { GetRuntimeDependencies: async () => ({ items: [], warnings: [] }) });
  assert.equal(s.label({ state: "missing", requiredBy: [] }), "未安装 · 按需");
  assert.equal(s.label({ state: "missing", requiredBy: ["php-5.6"] }), "需要安装");
  assert.equal(s.label({ state: "error", requiredBy: [] }), "检测异常");
  assert.equal(s.label({ state: "ready", requiredBy: [] }), "已检测到");
});

test("opening the page does not inspect runtimes until requested", async (t) => {
  let calls = 0;
  const s = mount(t, { GetRuntimeDependencies: async () => { calls++; return { items: [], warnings: [] }; } });
  await vue.nextTick();
  assert.equal(calls, 0);
  assert.equal(s.report.value, null);
  assert.equal(s.shown.value, false);
  await s.check();
  assert.equal(calls, 1);
  assert.equal(s.shown.value, true);
});

test("successful runtimes stay separate from items that need attention", (t) => {
  const s = mount(t, { GetRuntimeDependencies: async () => ({ items: [], warnings: [] }) });
  s.report.value = { items: [
    { id: "ready", state: "ready" },
    { id: "missing", state: "missing", requiredBy: ["php"] },
  ], warnings: [] };
  assert.deepEqual(s.readyItems.value.map((item) => item.id), ["ready"]);
  assert.deepEqual(s.pendingItems.value.map((item) => item.id), ["missing"]);
  assert.equal(s.showReady.value, false);
});

test("failed reads retain the last report and retry after installation replaces it", async (t) => {
  let calls = 0;
  const s = mount(t, { GetRuntimeDependencies: async () => {
    if (++calls === 2) throw new Error("读取系统目录失败");
    return { checkedAt: String(calls), items: [], warnings: [] };
  } });
  await s.check();
  assert.equal(s.report.value.checkedAt, "1");
  await s.check();
  assert.equal(s.report.value.checkedAt, "1");
  assert.match(s.error.value, /读取系统目录失败/);
  await s.check();
  assert.equal(s.report.value.checkedAt, "3");
  assert.equal(s.error.value, "");
});

test("repeated refresh clicks do not start duplicate inspections", async (t) => {
  let finish;
  let calls = 0;
  const s = mount(t, { GetRuntimeDependencies: () => { calls++; return new Promise((resolve) => { finish = resolve; }); } });
  const first = s.check();
  const second = s.check();
  assert.equal(calls, 1);
  assert.equal(s.checking.value, true);
  finish({ items: [], warnings: [] });
  await first;
  await second;
  assert.equal(s.checking.value, false);
});

test("opening a selected installer passes only its catalog ID", async (t) => {
  const opened = [];
  const s = mount(t, {
    GetRuntimeDependencies: async () => ({ items: [], warnings: [] }),
    OpenRuntimeInstaller: async (id) => { opened.push(id); },
  });
  s.openInstaller();
  assert.deepEqual(opened, []);
  s.guide.value = { id: "vc11" };
  s.openInstaller();
  assert.deepEqual(opened, ["vc11"]);
});
