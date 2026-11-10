import assert from "node:assert/strict";
import test from "node:test";
import { compareServiceInstances } from "../src/serviceOrder.ts";

test("service instances follow Nginx, MySQL, Redis, then PHP version", () => {
  const instances = [
    { kind: "php", name: "PHP 8.2.34", version: "8.2.34" },
    { kind: "redis", name: "Redis 8.10.2", version: "8.10.2" },
    { kind: "php", name: "PHP 5.6.40", version: "5.6.40" },
    { kind: "mysql", name: "MySQL 5.7", version: "5.7.44" },
    { kind: "nginx", name: "Nginx 1.31.7", version: "1.31.7" },
    { kind: "php", name: "PHP 7.3.33", version: "7.3.33" },
    { kind: "php", name: "PHP 8.10.1", version: "8.10.1" },
  ];
  const names = instances
    .slice()
    .sort(compareServiceInstances)
    .map((instance) => instance.name);
  assert.deepEqual(names, [
    "Nginx 1.31.7",
    "MySQL 5.7",
    "Redis 8.10.2",
    "PHP 5.6.40",
    "PHP 7.3.33",
    "PHP 8.2.34",
    "PHP 8.10.1",
  ]);
});
