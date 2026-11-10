import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";

const source = readFileSync(new URL("../src/siteTable.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
});
const exports = {};
new Function("require", "exports", outputText)((name) => {
  if (name === "./tableSort") {
    const { outputText } = ts.transpileModule(
      readFileSync(new URL("../src/tableSort.ts", import.meta.url), "utf8"),
      { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } },
    );
    const sorting = {};
    new Function("exports", outputText)(sorting);
    return sorting;
  }
  throw new Error(`Unexpected site table dependency: ${name}`);
}, exports);
const { defaultSiteView, nextSiteSort, orderedSites } = exports;

function site(id, name = id) {
  return {
    id, name, domain: `${id}.test`, root: `D:\\Sites\\${id}`,
    entry: "", port: 8081, phpInstanceId: "", enabled: true, rewrite: "",
  };
}
const ids = (sites) => sites.map((item) => item.id);

test("header cycles default, ascending, descending, then restores original order", () => {
  const sites = [site("c"), site("a"), site("b")];
  let preferences = defaultSiteView();
  assert.deepEqual(ids(orderedSites(sites, preferences)), ["c", "a", "b"]);
  preferences = nextSiteSort(preferences);
  assert.equal(preferences.sortBy, "name");
  assert.equal(preferences.sortDirection, "asc");
  assert.deepEqual(ids(orderedSites(sites, preferences)), ["a", "b", "c"]);
  preferences = nextSiteSort(preferences);
  assert.equal(preferences.sortDirection, "desc");
  assert.deepEqual(ids(orderedSites(sites, preferences)), ["c", "b", "a"]);
  preferences = nextSiteSort(preferences);
  assert.deepEqual(preferences, defaultSiteView());
  assert.deepEqual(ids(orderedSites(sites, preferences)), ["c", "a", "b"]);
});

test("name sorting handles letters, Chinese and numbers", () => {
  for (const [names, expected] of [
    [["Charlie", "alpha", "Bravo10", "bravo2"], ["alpha", "bravo2", "Bravo10", "Charlie"]],
    [["上海1", "北京10", "广州", "北京2"], ["北京2", "北京10", "广州", "上海1"]],
  ]) {
    const sites = names.map((name) => site(name));
    const ascending = nextSiteSort(defaultSiteView());
    assert.deepEqual(ids(orderedSites(sites, ascending)), expected);
    assert.deepEqual(ids(orderedSites(sites, nextSiteSort(ascending))), [...expected].reverse());
  }
});

test("removed pins do not affect the selected sort order", () => {
  const sites = [site("c"), site("b"), site("d"), site("a")];
  let preferences = {
    ...defaultSiteView(), hiddenColumns: ["root"], pinnedSiteIds: ["a", "d", "deleted"],
  };
  for (const expected of [
    ["c", "b", "d", "a"], ["a", "b", "c", "d"],
    ["d", "c", "b", "a"], ["c", "b", "d", "a"],
  ]) {
    assert.deepEqual(ids(orderedSites(sites, preferences)), expected);
    assert.deepEqual(preferences.hiddenColumns, ["root"]);
    preferences = nextSiteSort(preferences);
  }
});

test("removed sort fields and manual positions do not silently reorder the default view", () => {
  const sites = [site("c"), site("b"), site("a")];
  for (const sortBy of ["manual", "domain", "root", "environment", "enabled"]) {
    const preferences = {
      ...defaultSiteView(), sortBy, sortDirection: "desc", siteOrder: ["a", "c", "b"],
    };
    assert.deepEqual(ids(orderedSites(sites, preferences)), ["c", "b", "a"]);
    assert.deepEqual(nextSiteSort(preferences), { ...defaultSiteView(), sortBy: "name" });
  }
});

test("equal names retain their original order in both directions", () => {
  const sites = [site("b", "Example"), site("a", "example"), site("c", "EXAMPLE")];
  for (const sortDirection of ["asc", "desc"]) {
    assert.deepEqual(ids(orderedSites(sites, {
      ...defaultSiteView(), sortBy: "name", sortDirection,
    })), ["b", "a", "c"]);
  }
});

test("sorting does not mutate records, preferences or shared defaults", () => {
  const sites = Object.freeze([Object.freeze(site("b")), Object.freeze(site("a"))]);
  const preferences = Object.freeze({
    ...defaultSiteView(), hiddenColumns: Object.freeze(["root"]),
    siteOrder: Object.freeze([]),
  });
  const original = JSON.stringify({ sites, preferences });
  orderedSites(sites, preferences);
  nextSiteSort(preferences);
  assert.equal(JSON.stringify({ sites, preferences }), original);
  const changed = defaultSiteView();
  changed.hiddenColumns.push("name");
  assert.deepEqual(defaultSiteView().hiddenColumns, []);
});

for (const key of ["createdAt", "lastActiveAt"]) {
  test(`${key} sorts real timestamps across offsets and keeps unknown dates last`, () => {
    const sites = [
      { ...site("unknown"), [key]: "" },
      { ...site("new"), [key]: "2026-10-20T02:00:00Z" },
      { ...site("old"), [key]: "2026-10-20T09:00:00+08:00" },
      { ...site("invalid"), [key]: "invalid" },
    ];
    const initial = defaultSiteView();
    const ascending = nextSiteSort(initial, key);
    assert.deepEqual(ids(orderedSites(sites, ascending)), ["old", "new", "unknown", "invalid"]);
    const descending = nextSiteSort(ascending, key);
    assert.deepEqual(ids(orderedSites(sites, descending)), ["new", "old", "unknown", "invalid"]);
    const reset = nextSiteSort(descending, key);
    assert.equal(reset.sortBy, "manual");
    assert.deepEqual(ids(orderedSites(sites, reset)), key === "createdAt"
      ? ["new", "old", "unknown", "invalid"]
      : ["unknown", "new", "old", "invalid"]);
    const switched = nextSiteSort(descending, "name");
    assert.equal(switched.sortBy, "name");
    assert.equal(switched.sortDirection, "asc");
    const switchedBack = nextSiteSort(switched, key);
    assert.equal(switchedBack.sortBy, key);
    assert.equal(switchedBack.sortDirection, "asc");
  });
}

test("default order puts the latest configuration save first, ignoring access activity", () => {
  const rows = [
    { ...site("unknown"), createdAt: "", updatedAt: "", lastActiveAt: "2099-01-01T00:00:00Z" },
    { ...site("old"), updatedAt: "2026-10-20T09:00:00.100Z" },
    { ...site("new"), updatedAt: "2026-10-20T17:00:00.200+08:00" },
    { ...site("created"), createdAt: "2026-10-20T08:00:00Z" },
  ];
  const initial = JSON.stringify(rows);
  assert.deepEqual(ids(orderedSites(rows, defaultSiteView())), ["new", "old", "created", "unknown"]);
  const preferences = { ...defaultSiteView(), pinnedSiteIds: ["unknown"] };
  assert.deepEqual(ids(orderedSites(rows, preferences)), ["new", "old", "created", "unknown"]);
  const sorted = nextSiteSort(nextSiteSort(preferences));
  assert.deepEqual(ids(orderedSites(rows, nextSiteSort(sorted))), ["new", "old", "created", "unknown"]);
  assert.deepEqual(ids(orderedSites([...rows].reverse(), defaultSiteView())), ["new", "old", "created", "unknown"]);
  assert.equal(JSON.stringify(rows), initial);
});

