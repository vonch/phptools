import assert from "node:assert/strict";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";

async function waitUntil(check, message, timeout = 12000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = await check();
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(message);
}

async function openSettings(page) {
  await page.locator(".sidebar").getByText("设置", { exact: true }).click();
  await page.locator(".app-updates").waitFor({ state: "visible" });
  await page.locator(".page-content").evaluate((element) => { element.scrollTop = 0; });
}

function assertNoUpdateAddress(text, label) {
  assert.doesNotMatch(text, /https?:\/\/|wuyouzhilian|更新清单地址|未配置更新源|更新源/i, label);
}

export async function checkAppUpdates(page, call, out, checks) {
  await openSettings(page);
  const updates = page.locator(".app-updates");
  const check = updates.getByRole("button", { name: "检查更新", exact: true });
  await check.waitFor();
  assert.equal(await updates.locator("#update-manifest-url").count(), 0);
  assert.equal(await updates.getByRole("button", { name: "保存", exact: true }).count(), 0);
  const initial = await call("GetUpdateInfo");
  assert.equal(initial.configured, true);
  assert.equal((await call("GetState")).settings.updateManifestUrl, undefined);
  assert.equal(initial.downloadUrl, undefined);
  checks.push("应用升级使用内置更新地址，界面不提供配置，状态里也不返回地址");

  const layouts = [];
  for (const [width, height] of [[1440, 850], [1040, 700]]) {
    await page.setViewportSize({ width, height });
    const metrics = await updates.evaluate((element) => {
      const bounds = element.getBoundingClientRect();
      const content = document.querySelector(".page-content");
      return { left: bounds.left, right: bounds.right, top: bounds.top,
        scrollWidth: content.scrollWidth, clientWidth: content.clientWidth };
    });
    assert.ok(metrics.left >= 0 && metrics.right <= width + 1, "update card overflows the viewport");
    assert.ok(metrics.scrollWidth <= metrics.clientWidth + 1, "settings page has horizontal overflow");
    layouts.push({ width, height, ...metrics });
  }
  await page.screenshot({ path: path.join(out, "updates-builtin-small.png") });
  checks.push("1440×850 与 1040×700 窗口中升级设置无横向溢出");

  await check.click();
  await waitUntil(async () => {
    const label = await updates.locator(".n-tag").innerText();
    return label !== "正在检查";
  }, "update check did not finish", 25000);
  const visible = await updates.innerText();
  assertNoUpdateAddress(visible, "update card shows an address");
  const checked = await call("GetUpdateInfo");
  assert.equal(checked.configured, true);
  assert.equal(checked.downloadUrl, undefined);
  if (checked.error) assertNoUpdateAddress(checked.error, "update error shows an address");
  if (checked.notes) assertNoUpdateAddress(checked.notes, "update notes show an address");
  assert.equal(await updates.getByText("未配置更新源", { exact: true }).count(), 0);
  await page.locator(".page-content").evaluate((element) => { element.scrollTop = 0; });
  await page.screenshot({ path: path.join(out, "updates-check-result-small.png") });
  checks.push("检查更新不显示域名或更新地址");
  return { currentVersion: initial.currentVersion, available: checked.available, error: checked.error || "",
    layouts, realReleaseSourceTested: true, installedUpdate: false };
}

export async function checkHealthRepairs(page, call, root, out, checks) {
  const fixture = JSON.parse(await readFile(path.join(root, "ui-health-fixture.json"), "utf8"));
  const logs = path.dirname(fixture.logPath);
  const instanceDir = path.dirname(logs);
  const temp = path.join(instanceDir, "temp");
  const dataFile = path.join(fixture.dataDir, "preserve.txt");
  const configBefore = await readFile(fixture.configPath);
  const dataBefore = await readFile(dataFile);
  for (const absent of [logs, temp, fixture.executable, path.join(instanceDir, "process.json")]) {
    await assert.rejects(stat(absent), { code: "ENOENT" });
  }
  await page.locator(".sidebar").getByText("检测", { exact: true }).click();
  await page.getByRole("button", { name: "检测环境", exact: true }).click();
  const inspected = await waitUntil(async () => {
    const snapshot = await call("GetState");
    return snapshot.healthReport?.trigger === "manual"
      && snapshot.tasks.some((task) => task.action === "environment-check" && task.state === "completed")
      && snapshot;
  }, "manual environment check did not complete");
  const findItem = (snapshot, code) => snapshot.healthReport.items.find((item) => item.id === `${fixture.id}:${code}`);
  const missingComponent = findItem(inspected, "package");
  assert.equal(missingComponent.status, "error");
  assert.equal(missingComponent.repairable, false);
  for (const code of ["logs", "temp"]) {
    assert.equal(findItem(inspected, code).status, "warning");
    assert.equal(findItem(inspected, code).repairable, true);
  }
  await assert.rejects(stat(logs), { code: "ENOENT" });
  await assert.rejects(stat(temp), { code: "ENOENT" });
  checks.push("手动检测报告组件程序缺失及两个可修复目录，检测自身不重建目录");

  const healthRow = (title) => page.locator(".health-item")
    .filter({ has: page.getByText(title, { exact: true }) })
    .filter({ hasText: fixture.name });
  const componentRow = healthRow("组件文件");
  await componentRow.getByRole("button", { name: "检查组件安装", exact: true }).waitFor();
  assert.match(await componentRow.innerText(), /原始发行包或备份/);
  assert.equal(await componentRow.getByRole("button", { name: "修复", exact: true }).count(), 0);
  await componentRow.scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(out, "health-component-method-small.png") });
  await componentRow.getByRole("button", { name: "检查组件安装", exact: true }).click();
  await page.getByRole("heading", { name: "组件中心", exact: true }).waitFor();
  assert.equal((await call("GetState")).tasks.some((task) => task.action === "install"), false);
  checks.push("组件缺失提供恢复原组件的具体方法，检查组件安装按钮可导航到组件中心");

  await page.locator(".sidebar").getByText("检测", { exact: true }).click();
  const logsRow = healthRow("日志目录");
  await logsRow.getByRole("button", { name: "修复", exact: true }).click();
  const repaired = await waitUntil(async () => {
    const snapshot = await call("GetState");
    const task = snapshot.tasks.find((item) => item.action === "environment-repair" && item.targetId === `${fixture.id}:logs`);
    if (task?.state === "failed") throw new Error(task.error);
    return task?.state === "completed" && findItem(snapshot, "logs")?.status === "repaired" && snapshot;
  }, "single-item log directory repair did not complete");
  assert.ok((await stat(logs)).isDirectory());
  assert.equal(findItem(repaired, "logs").repairable, false);
  assert.equal(findItem(repaired, "temp").status, "warning");
  assert.equal(findItem(repaired, "temp").repairable, true);
  assert.equal(repaired.healthReport.full, true);
  await assert.rejects(stat(temp), { code: "ENOENT" });
  await assert.rejects(stat(fixture.executable), { code: "ENOENT" });
  await assert.rejects(stat(path.join(instanceDir, "process.json")), { code: "ENOENT" });
  assert.deepEqual(await readFile(fixture.configPath), configBefore);
  assert.deepEqual(await readFile(dataFile), dataBefore);
  assert.ok(repaired.instances.every((instance) => instance.pid === 0 && instance.state === "stopped"));
  await logsRow.getByText("已修复", { exact: true }).waitFor();
  assert.equal(await logsRow.getByRole("button", { name: "修复", exact: true }).count(), 0);
  await healthRow("临时目录").scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(out, "health-repair-result-small.png") });
  checks.push("修复按钮成功创建选中的日志目录，临时目录仍待修复，配置与数据字节不变且服务未启动");
  return { report: repaired.healthReport, task: repaired.tasks.find((task) => task.action === "environment-repair"),
    serviceProcessesStarted: false, configurationUnchanged: true, dataUnchanged: true };
}
