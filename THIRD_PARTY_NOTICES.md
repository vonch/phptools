# 第三方组件

PHPTools 使用以下项目，具体锁定版本见 `go.mod`、`go.sum` 和 `frontend/package-lock.json`。上游许可证与声明归原作者所有。

Wails 运行时基于 v3.0.0-beta.32，在项目内补充了 WebView2 挂起和恢复接口，修改说明见 `third_party/WAILS_PATCHES.md`。发行包保留其 Apache-2.0 许可证于 `licenses/Wails-LICENSE.txt`。

| 项目 | 用途 | 上游 |
| --- | --- | --- |
| Wails v3 | 桌面窗口、托盘、Go/JavaScript 绑定 | https://github.com/wailsapp/wails |
| Vue | 界面框架 | https://github.com/vuejs/core |
| Naive UI | Vue 界面组件库 | https://github.com/tusen-ai/naive-ui |
| TypeScript、Vite | 前端类型检查与构建 | https://github.com/microsoft/TypeScript / https://github.com/vitejs/vite |
| modernc.org/sqlite | SQLite Go 驱动 | https://gitlab.com/cznic/sqlite |
| go-keyring、wincred | Windows 凭据管理器访问 | https://github.com/zalando/go-keyring / https://github.com/danieljoos/wincred |
| ProtonMail go-crypto | OpenPGP 发布签名校验 | https://github.com/ProtonMail/go-crypto |
| Go x/sys | Windows 系统接口 | https://go.googlesource.com/sys |

PHP、Nginx、MySQL、Redis 与 Cygwin 运行库由用户通过各自发行渠道按需下载，不包含在 PHPTools 发行 ZIP 内。下载后保留上游发行包的许可证文件。Redis Windows 项目自身的 Apache-2.0 许可证不等同于 Redis 上游或 Cygwin 的许可证，参见各自发行包与上游说明。
