import assert from "node:assert/strict";
import test from "node:test";
import { runtimeDownloadOffer } from "../src/runtimePrompt.ts";

test("a missing Visual C++ failure offers the official v14 installer", () => {
  const error = "PHP 8.2.34: php-cgi.exe 无法启动：系统缺少 Visual C++ 运行库。安装文件已保留。请到设置中的运行依赖，安装 Visual C++ v14 x64 后重新启动该实例";
  assert.equal(runtimeDownloadOffer(error), "vc14");
  assert.equal(runtimeDownloadOffer("端口 9100 已被占用"), "");
  assert.equal(runtimeDownloadOffer(""), "");
});
