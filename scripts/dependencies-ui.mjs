import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { checkAppUpdates, checkHealthRepairs } from "./updates-health-ui.mjs";

const [port, root, moduleDir] = process.argv.slice(2);
const require = createRequire(path.join(moduleDir, "package.json"));
const { chromium } = require(moduleDir);
const binding = await readFile("frontend/bindings/phptools/internal/desktop/app.ts", "utf8");
const id = (method) => {
  const match = binding.match(new RegExp(`function ${method}\\([^]*?\\$Call.ByID\\((\\d+)`));
  assert.ok(match, `missing binding ${method}`);
  return Number(match[1]);
};
let browser, page;
const checks = [], pageErrors = [];
let updateResult, healthResult;
const call = (method, ...args) => page.evaluate(({ id, args }) => window.qaRuntime.Call.ByID(id, ...args), { id: id(method), args });
const out = path.resolve("build/qa");
await mkdir(out, { recursive: true });
try {
  for (let tries = 0; tries < 60; tries++) {
    try { browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`); break; }
    catch (error) { if (tries === 59) throw error; await new Promise((r) => setTimeout(r, 250)); }
  }
  for (let tries = 0; tries < 60; tries++) {
    page = browser.contexts().flatMap((c) => c.pages()).find((p) => p.url().startsWith("http://wails.localhost"));
    if (page) break;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  assert.ok(page, "missing app page");
  page.on("pageerror", (e) => pageErrors.push(String(e)));
  await page.evaluate(async () => { window.qaRuntime = await import("/wails/runtime.js"); });
  const initial = await call("GetRuntimeDependencies");
  assert.equal(initial.items.length, 4);
  assert.equal(initial.items[0].state, "ready");
  assert.equal(initial.warnings.length, 0);
  checks.push("环境未初始化时可读取真实系统依赖");
  await call("SetRoot", path.join(root, "独立 runtime"));
  await page.getByTitle("刷新状态").click();
  updateResult = await checkAppUpdates(page, call, out, checks);
  await page.locator(".sidebar").getByText("设置", { exact: true }).click();
  await page.getByRole("button", { name: "检查运行依赖", exact: true }).click();
  await page.getByRole("button", { name: "检测运行依赖", exact: true }).click();
  const passedToggle = page.locator(".runtime-dependencies .dependency-passed").getByRole("button", { name: "展开", exact: true });
  if (await passedToggle.count()) await passedToggle.click();
  await page.locator(".runtime-dependencies [data-dependency-id]").last().waitFor();
  assert.equal(await page.locator(".runtime-dependencies [data-dependency-id]").count(), 4);
  checks.push("设置入口导航到检测，显示四类依赖");
  await page.setViewportSize({ width:1040, height:700 });
  await page.locator('[data-dependency-id="vc11"]').getByRole("button", {name:/官方安装指引|修复方法/}).click();
  const modal = page.locator(".dependency-guide-modal");
  await modal.waitFor({ state:"visible" });
  assert.match(await modal.innerText(), /vcredist_x64.exe/);
  assert.match(await modal.innerText(), /不能用 v14 替代/);
  await modal.getByRole("button", {name:"安装后重新检测", exact:true}).click();
  await modal.getByRole("button", {name:"安装后重新检测", exact:true}).waitFor({state:"visible"});
  await page.waitForTimeout(250);
  const bounds = await modal.boundingBox();
  assert.ok(bounds.y >= 0 && bounds.y+bounds.height <= 700);
  await page.screenshot({ path:path.join(out,"dependencies-guide-small.png") });
  await modal.getByRole("button", { name:"关闭", exact:true }).click();
  await modal.waitFor({state:"hidden"});
  checks.push("x64 官方安装指引与重新检测可用，小窗口弹窗完整显示");
  await page.locator(".page-content").evaluate((e) => { e.scrollTop = 0; });
  await page.screenshot({ path:path.join(out,"dependencies-panel-small.png") });
  const after = await call("GetRuntimeDependencies");
  assert.equal(after.items[0].version, initial.items[0].version);
  await assert.rejects(call("OpenRuntimeInstaller", "https://example.com/evil.exe"), /未知运行依赖/);
  checks.push("任意安装链接被后端拒绝，检测没有修改系统依赖");
  healthResult = await checkHealthRepairs(page, call, root, out, checks);
  assert.deepEqual(pageErrors, []);
  await writeFile(path.join(out,"dependencies-ui.json"), JSON.stringify({ checkedAt:new Date().toISOString(), checks, report:after, cleanWindowsTested:false }, null, 2));
  await writeFile(path.join(out,"updates-health-ui.json"), JSON.stringify({ checkedAt:new Date().toISOString(), status:"passed", checks, update:updateResult, health:healthResult, pageErrors }, null, 2));
  console.log(JSON.stringify(checks));
} catch (error) {
  if (page && !page.isClosed()) await page.screenshot({ path:path.join(out,"updates-health-ui-failed.png") }).catch(() => {});
  await writeFile(path.join(out,"updates-health-ui.json"), JSON.stringify({ checkedAt:new Date().toISOString(), status:"failed", error:String(error), checks, update:updateResult, health:healthResult, pageErrors }, null, 2));
  throw error;
} finally {
  if (page && !page.isClosed()) {
    await call("Quit").catch(() => {});
  }
  await browser?.close().catch(() => {});
}
