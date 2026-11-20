import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";

const source = readFileSync(new URL("../src/certificateTable.ts", import.meta.url), "utf8");
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
  throw new Error(`Unexpected certificate table dependency: ${name}`);
}, exports);
const {
  defaultCertificateView,
  matchingCertificates,
  nextCertificateSort,
  orderedCertificates,
} = exports;

function row(siteId, extra = {}) {
  return {
    siteId,
    siteName: extra.siteName ?? siteId,
    domains: extra.domains ?? [`${siteId}.test`],
    kind: extra.kind ?? "none",
    issuer: extra.issuer ?? "",
    notAfter: extra.notAfter ?? "",
    eligible: true,
    reason: extra.reason ?? "",
  };
}
const ids = (rows) => rows.map((item) => item.siteId);

test("certificate header cycles default, ascending, descending, then restores site order", () => {
  const rows = [row("c"), row("a"), row("b")];
  const manual = ["b", "c", "a"];
  let preferences = defaultCertificateView();
  assert.deepEqual(ids(orderedCertificates(rows, preferences, manual)), ["b", "c", "a"]);
  preferences = nextCertificateSort(preferences);
  assert.equal(preferences.sortBy, "siteName");
  assert.equal(preferences.sortDirection, "asc");
  assert.deepEqual(ids(orderedCertificates(rows, preferences, manual)), ["a", "b", "c"]);
  preferences = nextCertificateSort(preferences);
  assert.equal(preferences.sortDirection, "desc");
  assert.deepEqual(ids(orderedCertificates(rows, preferences, manual)), ["c", "b", "a"]);
  preferences = nextCertificateSort(preferences);
  assert.deepEqual(preferences, defaultCertificateView());
  assert.deepEqual(ids(orderedCertificates(rows, preferences, manual)), ["b", "c", "a"]);
});

test("certificate kind and expiry use a fixed order and keep missing values last", () => {
  const rows = [
    row("invalid", { kind: "invalid", notAfter: "2026-01-01T00:00:00Z" }),
    row("empty", { kind: "none", notAfter: "" }),
    row("public", { kind: "public", notAfter: "2027-02-01T00:00:00Z" }),
    row("local", { kind: "local", notAfter: "2026-06-01T00:00:00Z" }),
  ];
  const byKind = nextCertificateSort(defaultCertificateView(), "kind");
  assert.deepEqual(ids(orderedCertificates(rows, byKind)), ["empty", "local", "public", "invalid"]);
  assert.deepEqual(ids(orderedCertificates(rows, nextCertificateSort(byKind, "kind"))), ["invalid", "public", "local", "empty"]);
  const byDate = nextCertificateSort(defaultCertificateView(), "notAfter");
  assert.deepEqual(ids(orderedCertificates(rows, byDate)), ["invalid", "local", "public", "empty"]);
  assert.deepEqual(ids(orderedCertificates(rows, nextCertificateSort(byDate, "notAfter"))), ["public", "local", "invalid", "empty"]);
});

test("certificate search matches the site, domain, status, issuer and reason", () => {
  const rows = [
    row("a", { siteName: "演示", domains: ["demo.test"], kind: "local", issuer: "PHPTools CA" }),
    row("b", { siteName: "商店", domains: ["shop.example"], kind: "public", reason: "证书即将到期" }),
    row("c", { siteName: "空站点", domains: [], kind: "none" }),
  ];
  assert.deepEqual(ids(matchingCertificates(rows, " 演示 ")), ["a"]);
  assert.deepEqual(ids(matchingCertificates(rows, "SHOP")), ["b"]);
  assert.deepEqual(ids(matchingCertificates(rows, "公网证书")), ["b"]);
  assert.deepEqual(ids(matchingCertificates(rows, "即将到期")), ["b"]);
  assert.deepEqual(ids(matchingCertificates(rows, "phptools")), ["a"]);
  assert.deepEqual(ids(matchingCertificates(rows, "")), ["a", "b", "c"]);
});
