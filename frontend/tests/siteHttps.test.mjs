import assert from "node:assert/strict";
import test from "node:test";
import { httpsFormStatus } from "../src/siteHttps.ts";

test("editing a site created with HTTPS shows the applied port and addresses", () => {
  const status = httpsFormStatus({
    id: "shop",
    https: true,
    port: 80,
    domain: "shop.test",
    secondDomain: "api.shop.test",
  }, true);
  assert.match(status.text, /HTTP 80 保持不变/);
  assert.match(status.text, /HTTPS 443/);
  assert.match(status.text, /第一域名和第二域名/);
  assert.deepEqual(status.addresses, ["https://shop.test", "https://api.shop.test"]);

  const securePort = httpsFormStatus({
    id: "shop",
    https: true,
    port: 443,
    domain: "shop.test",
  }, true);
  assert.match(securePort.text, /端口 443 已改为 HTTPS/);
  assert.deepEqual(securePort.addresses, ["https://shop.test"]);
});

test("turning HTTPS off while editing explains the pending removal", () => {
  const status = httpsFormStatus({
    id: "shop",
    https: false,
    port: 80,
    domain: "shop.test",
  }, true);
  assert.match(status.text, /关闭 HTTPS/);
  assert.deepEqual(status.addresses, []);
});

test("a new site still describes HTTPS before it is saved", () => {
  const status = httpsFormStatus({
    id: "",
    https: true,
    port: 80,
    domain: "shop.test",
  }, false);
  assert.match(status.text, /保存后/);
  assert.deepEqual(status.addresses, ["https://shop.test"]);
});
