# 自托管部署

1. 使用全新的 PostgreSQL 数据库。配置 `v2/.env` 中的 `MBRAND_DEPLOYMENT_ID=m-brand`、站点 HTTPS 地址、数据库与初始管理员密码。数据库 URL 必须明确填写，禁止连接旧 `zerobrand` 库或使用原运营站域名。`.env` 不进入 Git。
2. 执行 `bash scripts/build.sh`。编译产物位于 `.local/bin/m-brand`；构建不会重启任何服务。
3. 运行 `bash scripts/run.sh all`。拆分部署时，API 和 Worker 使用相同数据库、生成目录及同一份位于可执行文件旁的 `secret.key`。可在同一安装目录分别运行 `bash scripts/run.sh api` 与 `bash scripts/run.sh worker`。不要从临时路径启动二进制，否则会创建不同会话密钥。
4. 参考 `deploy/Caddyfile.example` 配置自己的域名与 TLS，确保 `/api/*`、`/gen/*` 和协议端点被转到后端 `/0brand` 前缀。后端只监听回环地址。
5. 配置自己的 SMTP/Resend、模型服务与微信商户；不要填写原站域名或借用原站密钥。未配置付款时页面会显示渠道不可用。
6. 访问 `/api/health` 核验 API 与 Worker；页面、管理端登录、邀请真实收信和渠道付款应在自己的测试环境单独验收。

保管数据库、`.local/generated` 与 `.local/bin/secret.key`。更新前备份自己的数据，按 Git 提交构建可追踪产物；保留可运行的上一版本。迁移启动时自动执行，旧二进制不代表数据库可安全降级。公开版本不携带原运营站的 systemd、发布策略、服务器地址或管理会话签发工具。

生产配置 `ZEROBRAND_ENV=production`，不能启用模拟付款。API/Worker 若分开运行，需要共同挂载图片目录；不要把私有客户图片目录直接作为公共静态目录暴露。

默认应用端口是 `18830`，示例 HTTPS 端口是 `8443`。Caddy 示例关闭管理 API，作为独立实例启动；不能用该示例替换既有共享站点配置。不要复用其他项目的 systemd 单元、工作目录、环境文件、二进制或数据库。直接启动二进制同样执行部署身份与数据库/域名检查，不能靠跳过 run.sh 省略配置校验。
