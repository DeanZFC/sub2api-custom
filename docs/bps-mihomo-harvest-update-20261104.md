# BPS、Mihomo 与打票合并记录（2026-11-04）

本次按功能合入 [ranxi2001/sub2api production](https://github.com/ranxi2001/sub2api/commit/981f3645a6df2a099b8dde1acdbbb8a77875a68a) 截至 `981f3645a6df2a099b8dde1acdbbb8a77875a68a` 的相关更新。自定义代码基线为 `653e6397b1f872da8a47b77bf311fd55771727ec`。未整体替换上游分支，保留当前 VM、PAT、Agent Identity、影子凭据及其他自定义模块。

## 合入功能

| 模块 | 新增和更新 |
| --- | --- |
| BPS 授权 | 手动 Excel OAuth / Refresh Token；已有 Codex 账号的独立加密 Excel grant；Password/TOTP 自动授权；授权状态、材料管理、Worker 状态与凭据加密初始化 |
| BPS 测试诊断 | 实时模型权限目录；真实上游 HTTP 状态、模型、Request ID、安全错误代码；文本与工具三步往返验证；全站协议开关；可选的 403 定时探测恢复 |
| Mihomo | 节点连通性与质量检测；国家/状态筛选；节点绑定固定代理；固定监听持久化与失效处理 |
| 打票 | 780 两轮采集验收、定向采集与换节点策略、覆盖率、SSE 快速复用、来源/目标准备和历史、三种票池调度模式、可选糖果题观察 |

## 合并时补充的兼容修复

- Excel grant 与原生 Codex 凭据分开保存。BPS 401 只标记本次使用的 Excel AT，保留 RT 和原生凭据；过期授权刷新和重新授权采用并发保护。
- 主凭据为 Excel OAuth 的账号不能进入原生 Codex 模型、图片、WebSocket 或影子凭据路径；普通 Codex 账号在全站 BPS 关闭时仍可走原生路径。
- 403 恢复使用持久化抢占、随机文本验证及账号/配置/代理/授权版本检查。刷新授权产生的诊断更新不会误阻止恢复，管理员的并发修改仍受到保护。
- 票池调度自身暂停或移组的账号仍可后台补票；写入票据和同身份 Token 刷新保留调度所有权，管理员的手动修改不会被覆盖。
- 自动准备、采样、恢复与调度任务在服务关闭时取消并等待退出。

## 使用入口

打票总开关位于「系统设置 → 功能开关 → Codex 打票功能」，默认关闭。开启并保存后才显示打票管理及采票相关入口；关闭时隐藏这些入口并停止打票页面轮询。菜单与打票运行共用 `openai_codex_ticket_enabled`，原有明确启用的配置继续生效。

- [Excel / BPS 授权与诊断](excel-bps-authorization-and-diagnostics.md)：协议开关、两种授权方式、错误定位与 403 恢复。
- [Mihomo 与打票更新](mihomo-ticket-updates-20261104.md)：固定代理、两轮验收、快速复用和调度。
- [授权 Worker 配置](../tools/reauth-runtime/README.md)：托管与外部 Worker、依赖和持久化目录。

托管 Worker 支持 Linux amd64/arm64，固定使用经 SHA256 校验的上游 `v2.19.4`，与本项目版本号独立。macOS/Windows 或自备 Worker 使用外部模式；手动 Excel OAuth 不依赖 Worker。部署时需持久化 `DATA_DIR` 中的凭据加密密钥。

快速复用保存启用配置后会执行准备和验证；工具测试及自动恢复也会发送模型请求，按需开启。

## 数据与验证

新增迁移 `335_mihomo_static_proxy_bindings.sql`、`336_astra_gateway_history.sql`、`337_astra_scheduling_states.sql`、`338_excel_oauth_authorization.sql`。原有 422 个迁移文件保持逐字节一致。

验证使用本地模拟上游和 PostgreSQL/Redis 测试容器，未执行真实账号登录或真实 BPS/OpenAI 请求。本次为本地源码合并，不代表已发布或已安装到生产服务器。

### 已完成验证

- 后端全量：`go test -p 2 -tags=unit ./...` 通过。
- 前端全量：466 个测试文件、3997 项测试通过；同步回原目录后 `pnpm build` 再次通过（包含 i18n、类型检查和 Vite）。
- 原目录 Linux amd64 后端嵌入前端构建通过，QQ 机器人已有修改的定向兼容测试通过。
- PostgreSQL/Redis：独立授权、403 并发抢占/授权版本校验、Astra 历史与调度恢复集成测试通过；新增授权迁移重复执行验证通过。
- BPS 授权、403 恢复与票池调度定向 `-race` 检查通过；Wire 重生成一致；`git diff --check` 通过。
- 自动授权脚本：Python 12 项、Node 3 项离线测试通过；Linux arm64 隔离容器中的 Worker 解包、摘要、最小进程环境及停止测试通过。

默认包并发的全量运行曾出现 Mihomo 异步操作等待超时；该用例连续执行、Mihomo 整包复跑及最终 `-p 2` 全量均通过。测试补充了超时现场堆栈，未放宽断言或业务超时。验证日志位于本地忽略目录 `.dev/bps-mihomo-harvest-update-20261104/`。
