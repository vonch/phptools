import assert from "node:assert/strict";
import test from "node:test";
import { databaseNameFromSite, domainFromName, folderFromName, parseBatchSites } from "../src/siteDefaults.ts";

test("a site name fills the local domain and database name", () => {
  assert.equal(domainFromName("ttttt"), "ttttt.test");
  assert.equal(domainFromName("  My Site  "), "my-site.test");
  assert.equal(domainFromName("我的项目"), "wodexiangmu.test");
  assert.equal(domainFromName("PHP项目"), "phpxiangmu.test");
  assert.equal(domainFromName("我的 PHP"), "wode-php.test");
  assert.equal(domainFromName("，。"), "");
  assert.equal(databaseNameFromSite("ttttt"), "ttttt");
  assert.equal(databaseNameFromSite("My Site"), "MySite");
  assert.equal(databaseNameFromSite("我的"), "wode");
  assert.equal(databaseNameFromSite("a".repeat(40)).length, 32);
  assert.equal(folderFromName("  我的项目  "), "wodexiangmu");
  assert.equal(folderFromName("My Site"), "mysite");
  assert.equal(folderFromName("a/b:c"), "abc");
  assert.equal(folderFromName("重庆"), "chongqing");
  assert.equal(folderFromName("绿"), "lv");
  assert.equal(folderFromName("，。"), "");
});

test("batch lines accept a name, a domain, or both", () => {
  assert.deepEqual(parseBatchSites("我的项目\nblog.test\n演示 demo.test\n"), [
    { name: "我的项目", domain: "wodexiangmu.test", projectFolder: "wodexiangmu" },
    { name: "blog.test", domain: "blog.test", projectFolder: "blog" },
    { name: "演示", domain: "demo.test", projectFolder: "yanshi" },
  ]);
  assert.throws(() => parseBatchSites("，。"), /无法生成域名/);
  assert.throws(() => parseBatchSites("商店 shop.test 多余"), /第 1 行只能写名称/);
  assert.throws(() => parseBatchSites("a.test\n\nA.TEST"), /第 3 行的域名 A.TEST 与第 1 行重复/);
  assert.throws(() => parseBatchSites("   "), /请至少填写一个站点/);
  assert.throws(() => parseBatchSites(Array.from({ length: 51 }, (_, index) => `s${index}.test`).join("\n")), /一次最多创建 50 个站点/);
});
