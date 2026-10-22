# 前端隔离回归测试

在 `v2/web` 目录运行，使用 Node.js 22.12+（或 24 LTS）与 npm：

```sh
npm ci
npx playwright install chromium
npm test
```

Linux CI 缺少浏览器系统依赖时，可使用 `npx playwright install --with-deps chromium`。该安装步骤会下载浏览器，并可能需要系统包安装权限。

测试由 Node.js 内置测试运行器执行，通过 Playwright 打开本机 Vite 页面。三个测试文件分别使用 `127.0.0.1` 的 4197、4207、4217 端口；运行前请确保端口空闲。测试自动启动并关闭 Vite 与 Chromium，不需要启动 Go 服务、数据库或配置 `.env`。

所有 `/api/` 请求均被测试 fixture 拦截；浏览器的非本机请求被禁止。支付、生成、额度和草稿写入均为 mock，不会创建真实订单、调用模型或修改生产数据。请保留这些拦截规则，不要把测试入口改为生产地址。

覆盖范围：提案操作错误提示、首次状态读取失败后的恢复、402 解锁入口、支付订单恢复与终态、支付配置重试、草稿竞态与尾字保存、订单列表错误恢复，以及手机与桌面的按钮布局。测试验证页面行为，不替代微信实单支付、退款、真实模型输出或生产部署验收。

只运行某个文件或案例：

```sh
node --test tests/commercial-job.spec.mjs
node --test --test-name-pattern='Pay: failed stops' tests/commercial-pages.spec.mjs
```

通常不需要额外环境变量。特殊离线环境可显式指定已安装的 Chromium 可执行文件：

```sh
CHROMIUM_EXECUTABLE=/absolute/path/to/chromium npm test
```

`PLAYWRIGHT_MODULE` 可覆盖 Playwright 模块位置；默认使用项目 `node_modules/playwright`。锁文件固定 Playwright 版本，建议安装对应 Chromium，避免浏览器版本不匹配。
