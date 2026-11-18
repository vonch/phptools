import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";

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
const checks = [], pageErrors = [], layoutChecks = [];
const call = (method, ...args) => page.evaluate(({ id, args }) =>
  window.qaRuntime.Call.ByID(id, ...args), { id: id(method), args });
const out = path.resolve("build/qa");
await mkdir(out, { recursive: true });
const names = () => page.locator(".site-table tbody .site-name-label strong").allTextContents();
async function expectNames(expected) {
  try {
    await page.waitForFunction((expected) =>
      JSON.stringify([...document.querySelectorAll(".site-table tbody .site-name-label strong")].map((e) => e.textContent)) === JSON.stringify(expected),
    expected, { timeout: 10000 });
  } catch (error) {
    console.error(JSON.stringify({ expected, actual: await names() }));
    throw error;
  }
}
async function openSites() {
  await page.locator(".sidebar").getByText("站点", { exact: true }).click();
  await page.locator(".site-table tbody .site-name-label strong").first().waitFor();
  await page.waitForFunction(() => !document.querySelector('button[aria-label="显示列"]')?.disabled);
  await page.evaluate(async () => { window.qaRuntime = await import("/wails/runtime.js"); });
}
const extras = Array.from({ length: 50 }, (_, index) => `ZZ Test ${String(index + 1).padStart(2, "0")}`);
const normal = ["喜慧通", "Demo 10", "Demo 2", "Alpha 项目", ...extras, "旧版站点"];
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
  page.on("pageerror", (error) => pageErrors.push(String(error)));
  await openSites();
  await expectNames(normal);
  assert.equal(await page.locator(".site-table").getByRole("button", { name: "日志", exact: true }).count(), 0);
  assert.equal(await page.locator(".site-table").getByRole("button", { name: /置顶/ }).count(), 0);
  assert.equal(await page.locator(".site-pin-tag").count(), 0);
  assert.equal("pinnedSiteIds" in (await call("GetState")).siteView, false);
  assert.deepEqual(await page.locator(".site-table tbody .site-domain").allTextContents(), [
    "xihuitong.test:8081", "demo10.test:8081", "demo2.test:8081", "alpha.test:8081",
    ...extras.map((_, index) => `fixture-${String(index + 1).padStart(2, "0")}.test:8081`), "legacy.test:8081",
  ]);
  checks.push("55 个站点按配置修改时间倒序；未知时间靠后；名称和域名同时显示；旧版日志和置顶设置不再生效");
  const header = page.locator(".site-table th").filter({ hasText: "站点 / 域名" });
  await header.click();
  await expectNames(["旧版站点", "喜慧通", "Alpha 项目", "Demo 2", "Demo 10", ...extras]);
  await page.mouse.move(0, 0);
  await page.waitForFunction(() => {
    const row = document.querySelector('.site-table tbody tr');
    const cells = row?.querySelectorAll('td');
    const heads = document.querySelectorAll('.site-table th');
    return cells?.length > 1 && heads.length > 1
      && getComputedStyle(cells[0]).backgroundColor === getComputedStyle(cells[1]).backgroundColor
      && getComputedStyle(heads[0]).backgroundColor === getComputedStyle(heads[1]).backgroundColor;
  });
  checks.push("名称排序时表头和单元格底色与普通列一致");
  await header.click();
  await expectNames([...extras].reverse().concat(["Demo 10", "Demo 2", "Alpha 项目", "喜慧通", "旧版站点"]));
  await header.click();
  await expectNames(normal);
  checks.push("名称排序支持升序、降序、恢复最近修改优先");
  await page.getByRole("textbox", { name: "搜索站点" }).fill("xihuitong");
  await expectNames(["喜慧通"]);
  await page.getByRole("textbox", { name: "搜索站点" }).fill("");
  await expectNames(normal);
  checks.push("按域名搜索正常");
  await page.getByRole("button", { name: "显示列", exact: true }).click();
  const options = page.locator(".site-column-settings");
  assert.equal(await options.getByRole("checkbox", { name: "显示日志按钮", exact: true }).count(), 0);
  await options.getByRole("checkbox", { name: "项目目录", exact: true }).uncheck();
  await options.getByRole("button", { name: "应用", exact: true }).click();
  await options.waitFor({ state: "hidden" });
  await page.reload();
  await openSites();
  await expectNames(normal);
  assert.deepEqual((await call("GetState")).siteView.hiddenColumns, ["root"]);
  await page.getByRole("button", { name: "显示列", exact: true }).click();
  await options.getByRole("button", { name: "恢复默认显示", exact: true }).click();
  await options.getByRole("button", { name: "应用", exact: true }).click();
  await options.waitFor({ state: "hidden" });
  assert.equal(await page.locator(".site-table").getByRole("button", { name: "日志", exact: true }).count(), 0);
  checks.push("日志按钮及其显示选项已移除；显示列设置保存、刷新、恢复默认正常");
  for (const [width, height] of [[1440, 850], [1040, 700]]) {
    await page.setViewportSize({ width, height });
    const content = page.locator('.page-content');
    await content.evaluate((el) => { el.scrollTop = 0; });
    const row = page.locator(".site-table tbody tr").first();
    const metrics = await page.evaluate(() => {
      const content = document.querySelector('.page-content');
      const table = document.querySelector('.site-table');
      return {
        viewportOverflow: document.documentElement.scrollWidth - innerWidth,
        pageHeight: content.clientHeight,
        pageScrollHeight: content.scrollHeight,
        tableHeight: table.getBoundingClientRect().height,
        innerVerticalOverflow: [...table.querySelectorAll('.n-scrollbar-container')]
          .map((el) => el.scrollHeight - el.clientHeight),
        fixedCells: [...table.querySelectorAll('th, td')]
          .filter((el) => ['sticky', 'fixed'].includes(getComputedStyle(el).position)).length,
      };
    });
    assert.ok(metrics.viewportOverflow <= 1, 'page must not overflow horizontally');
    assert.ok(metrics.tableHeight > height * 3, 'long table must expand with its rows');
    assert.ok(metrics.pageScrollHeight > metrics.pageHeight * 3, 'page must scroll vertically');
    assert.ok(metrics.innerVerticalOverflow.every((overflow) => overflow <= 1), 'table must not have its own vertical scrolling');
    assert.equal(metrics.fixedCells, 0, 'table columns must not be fixed');
    layoutChecks.push({ width, height, ...metrics });
    await page.screenshot({ path: path.join(out, `sites-default-top-${width}.png`) });
    const headerBefore = await header.boundingBox();
    await content.evaluate((el) => { el.scrollTop = 500; });
    await page.waitForFunction(() => document.querySelector('.page-content').scrollTop === 500);
    assert.ok(Math.abs((await header.boundingBox()).y - headerBefore.y + 500) < 1, 'header must scroll with the page');
    await content.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    const lastRow = page.locator('.site-table tbody tr').last();
    const lastBounds = await lastRow.boundingBox();
    assert.ok(lastBounds && lastBounds.y > 0 && lastBounds.y + lastBounds.height <= height - 29, 'last row must be reachable');
    const container = page.locator('.site-table .n-scrollbar-container').first();
    await container.evaluate((el) => { el.scrollLeft = 0; });
    const nameBefore = await row.locator('.site-identity').boundingBox();
    const actionBefore = await row.getByRole('button', { name: '目录', exact: true }).boundingBox();
    const moved = await container.evaluate((el) => {
      el.scrollLeft = el.scrollWidth - el.clientWidth;
      return el.scrollLeft;
    });
    assert.ok(moved > 10, 'fixture must allow horizontal scrolling');
    assert.ok(Math.abs((await row.locator('.site-identity').boundingBox()).x - nameBefore.x + moved) < 1);
    assert.ok(Math.abs((await row.getByRole('button', { name: '目录', exact: true }).boundingBox()).x - actionBefore.x + moved) < 1);
    const deleteBounds = await lastRow.getByRole('button', { name: '删除', exact: true }).boundingBox();
    assert.ok(deleteBounds && deleteBounds.x > 0 && deleteBounds.x + deleteBounds.width < width, 'actions must be reachable');
    await page.screenshot({ path: path.join(out, `sites-default-bottom-${width}.png`) });
    await container.evaluate((el) => { el.scrollLeft = 0; });
    await content.evaluate((el) => { el.scrollTop = 0; });
    await page.getByRole('textbox', { name: '搜索站点' }).fill('xihuitong');
    await expectNames(['喜慧通']);
    const singleHeight = (await page.locator('.site-table').boundingBox()).height;
    assert.ok(singleHeight < metrics.tableHeight / 5, 'filtered table must shrink to fit its rows');
    await page.screenshot({ path: path.join(out, `sites-default-single-${width}.png`) });
    await page.getByRole('textbox', { name: '搜索站点' }).fill('');
    await expectNames(normal);
  }
  checks.push("1440×850 和 1040×700 下列表随 55 行自然展开，页面可滚至最后一行，筛选为一行后高度收缩");
  checks.push("所有列随横向滚动移动，没有固定列，操作按钮可达");
  await page.getByRole('textbox', { name: '搜索站点' }).fill('不存在的站点');
  await page.getByText('没有找到匹配的站点', { exact: true }).waitFor();
  const clear = page.getByRole('button', { name: '清除搜索', exact: true });
  const clearBounds = await clear.boundingBox();
  assert.ok(clearBounds && clearBounds.y > 0 && clearBounds.y + clearBounds.height < 700);
  await clear.click();
  await expectNames(normal);
  checks.push('搜索无结果时空状态和清除搜索按钮仍可见');
  await assert.rejects(call("OpenDirectory", path.join(root, "不存在")), /无法访问目录/);
  await page.locator(".site-table tbody tr").filter({ hasText: "旧版站点" })
    .getByRole("button", { name: "目录", exact: true }).click();
  await page.locator(".n-message").filter({ hasText: "无法访问目录" }).waitFor();
  checks.push("点击目录按钮实际调用后端；目录不存在时显示错误");
  assert.deepEqual(pageErrors, []);
  await writeFile(path.join(out, "sites-default-ui.json"), JSON.stringify({
    checkedAt: new Date().toISOString(), checks, layoutChecks, siteCount: (await names()).length, pageErrors,
  }, null, 2));
  console.log(JSON.stringify(checks));
} catch (error) {
  if (page && !page.isClosed()) await page.screenshot({ path: path.join(out, "sites-default-failure.png") }).catch(() => {});
  throw error;
} finally {
  if (page && !page.isClosed()) await call("Quit").catch(() => {});
  await browser?.close().catch(() => {});
}
