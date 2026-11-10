import assert from "node:assert/strict";
import test from "node:test";
import { databaseNameFromSite, domainFromName } from "../src/siteDefaults.ts";

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
});
