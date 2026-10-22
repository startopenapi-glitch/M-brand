# M-brand

开源、自托管的 AI 品牌设计平台。提供品牌提案、Logo 方向选择、按项购买与设计交付，以及运营后台和代理分销管理。基于 Go、PostgreSQL、React 和 Vite，采用 [MIT 许可证](LICENSE)。

M-brand 是从 0brand 最新业务源码整理的独立开源版本；线上 0brand 的名称、服务、客户数据和生产配置不受影响。此仓库不包含生产 Git 历史、密钥、数据库、客户作品、账号或内部运维记录。

## 功能

- 填写品牌信息、生成并比较 Logo 方向、确认标志、查看与下载交付项。
- 按项定价、支付结果核对、订单恢复、权益校验和交付恢复。
- 管理后台：工作台、任务、订单、邀请码、售后、统计与审计。
- 代理邮箱邀请、一次性链接开通、登录与退出；管理员可在邀请前、待开通及开通后设置佣金，范围 1–90 整数百分比，默认 40%。
- 佣金变更保留审计；已有订单比例快照和历史佣金不追溯重算。
- API 与 Worker 可一起运行，也可使用 `api` / `worker` 参数分别启动。

## 本地启动

需要 Go 1.26+、Node.js 22.12+、npm、PostgreSQL 16+、Python 3、gzip、zstd 和 Caddy。图片交付还需要 `cwebp`；按运行环境安装 WebP 工具。

```bash
git clone https://github.com/startopenapi-glitch/M-brand.git
cd M-brand
cp v2/.env.example v2/.env
# 配置数据库、站点 HTTPS 地址、初始管理员及自己的服务凭据
bash scripts/build.sh
bash scripts/run.sh
```

在 PostgreSQL 中创建独立的 `mbrand` 数据库和登录角色，将连接写入 `v2/.env`。首次启动自动执行迁移并创建初始管理员；务必先配置强密码。不要将这个新开源版本直接连接原 0brand 数据库，独立导出的迁移历史与生产库不同。

另一个终端启动 HTTPS 入口：

```bash
caddy run --config deploy/Caddyfile.example --adapter caddyfile
```

访问 `https://localhost:8443/`，管理端 `/admin`，代理端 `/agent`。本地使用 Caddy 开发证书；需要信任该证书。登录 Cookie 保留 Secure 与 HttpOnly，不能用取消 Cookie 安全属性来绕过 HTTPS。

后端监听本机 `127.0.0.1:8830`，内部 API 前缀保留 `/0brand`；浏览器使用根路径 `/api`，Caddy 示例负责转换。详情见 [部署说明](docs/DEPLOYMENT.md)。

## 邮件、模型与支付

需要部署者配置自己的模型 API、发信服务及支付商户。没有配置时，开源代码不会获得原站额度、密钥或支付能力。

邀请邮件支持 SMTP 或 Resend。SMTP 发件人必须是已验证的纯邮箱地址；不要填写 `品牌名 <email>`。未配置或发送失败时，后台提供邀请链接和明确的邮件状态。

真实微信支付默认关闭。`WXPAY_MOCK=1` 只能用于隔离开发环境；生产环境禁止 mock。正式经营前请填写自己的服务协议、退款和隐私条款；仓库中的页面只是模板。

## 测试

```bash
# 前端构建必须先完成，Go 会内嵌构建后的资源
bash scripts/build.sh
cd v2/web && npx playwright install chromium && cd ../..
# 仅允许本机、明确命名为 zerobrand_test 的独立测试数据库
export ZEROBRAND_PG_URL='postgres://YOUR_TEST_USER:YOUR_TEST_PASSWORD@127.0.0.1:5432/zerobrand_test'
bash scripts/test.sh
```

测试入口会拒绝非本机或非测试库连接，并清除支付、邮件和模型服务环境变量。前端回归拦截外部请求；后端使用本机替身，不需要真实付款、发信或生图。GitHub Actions 使用独立 PostgreSQL 服务执行相同检查。模拟测试不等于真实渠道验收。

## 目录

`v2/server`：Go API、Worker、迁移及后端回归；`v2/web`：React 前端及浏览器回归；`scripts`：独立构建、启动、测试；`deploy`：自托管配置示例。

内置 M-brand 矢量演示素材随项目采用 MIT 许可。第三方依赖维持各自许可证，见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。反馈问题请使用 GitHub Issues；贡献方式见 [CONTRIBUTING.md](CONTRIBUTING.md)。

首版的验证范围与边界见 [隔离验证记录](docs/VERIFICATION.md)。
