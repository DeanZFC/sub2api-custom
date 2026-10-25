# Sub2API vm2api account bridge

这是独立部署的可选运行时桥接进程。Sub2API 通过受鉴权的 HTTP API 管理每个 OpenAI OAuth 账号对应的 vm2api 专属槽位，并通过同一进程的账号级 CONNECT 代理执行 OAuth 刷新、用量查询等辅助请求。Sub2API 本身和 bridge 都不需要 Docker socket；只有 vm2api 控制面按自身部署要求访问 Docker/KVM。

## 架构边界

- 一个 Sub2API 账号绑定一个 vm2api OpenAI 槽位，以及只允许访问该槽位的 managed API key。推理请求使用这个 key，不能使用 vm2api master key。
- vm2api 的 OpenAI `kin-codex-kernel` 在运行时宿主机执行；当前上游容器主要承担槽位生命周期，并不是在真实 VM 内运行官方 Codex CLI。这里提供的是账号槽位、资源管理和网络出口的一致性，不承诺虚拟机级推理进程隔离。
- bridge 必须与 vm2api 的实际执行宿主机具有相同出口。`upstream_proxy_url` 为空时，两者都使用该运行时的本地出口；不应把 bridge 放在另一个出口不同的服务器上并使用空代理。
- 当前 vm2api 导入代理接口只支持 SOCKS5/SOCKS5H；账号创建接受 `socks5://`、`socks5h://` 或空值。HTTP/HTTPS 账号上游代理会明确拒绝，不做静默转换或直连回退。
- CONNECT 只允许 `api.openai.com`、`auth.openai.com`、`chatgpt.com`、`chat.openai.com`、`platform.openai.com` 的 443 端口；不接受 IP 地址、通配域名、其他端口或普通 HTTP 通用转发。
- Refresh token 由 Sub2API 持有和刷新。bridge 管理 JSON 拒绝 `refresh_token` 和其他未声明字段。bridge 自身不将 access token 或 refresh token 写入状态文件；AT 仍会被交给 vm2api，由 vm2api 按其上游实现保存和使用。

## 构建和环境

在仓库 `backend` 目录中执行：

```sh
go build -o vm2api-bridge ./cmd/vm2api-bridge
go test -race ./internal/pkg/vmbridge ./internal/pkg/vm2api ./cmd/vm2api-bridge
```

部署目标为 Linux/Unix。使用单个非 root 服务进程、稳定的工作目录和私有环境文件启动二进制。以下值必须按部署配置设置，不在命令行、日志或工单中粘贴真实密钥。

| 环境变量 | 必需 / 默认值 | 用途 |
| --- | --- | --- |
| `VM2API_URL` | 必需 | vm2api 控制面实际 HTTP(S) 地址。使用私有网络或已验证证书的 HTTPS。 |
| `VM2API_MASTER_KEY` | 必需 | 仅供 bridge 管理 vm2api 的 master key。不要配置到 Sub2API 推理凭据中。 |
| `VM2API_BRIDGE_SECRET` | 必需 | 至少 32 字节的随机共享密钥，无首尾空白。Sub2API 连接配置使用同一值。 |
| `VM2API_BRIDGE_LISTEN` | `127.0.0.1:8097` | 管理 API 与 CONNECT 共用的监听地址。跨主机连接应配置私网 IP 或启用 TLS。 |
| `VM2API_BRIDGE_STATE_DIR` | `./vm2api-bridge-state` | 持久化目录；生产环境建议使用绝对路径。 |
| `VM2API_BRIDGE_NAMESPACE` | 由共享密钥派生的稳定指纹 | 独立安装标识；不同 Sub2API 安装不可共用同一 namespace。首次创建槽位后不可更改。 |
| `VM2API_BRIDGE_DRAIN_TIMEOUT` | `30s` | 删除等待推理流、CONNECT 与同账号操作结束的最长时间。超时保留待删除状态。 |
| `VM2API_BRIDGE_DIAL_TIMEOUT` | `15s` | CONNECT 出口建连超时。 |
| `VM2API_BRIDGE_MANAGEMENT_TIMEOUT` | `90s` | 单次 vm2api 控制面 HTTP 操作的超时。 |
| `VM2API_BRIDGE_TLS_CERT` | 可选 | PEM 服务端证书路径；须与 TLS_KEY 同时设置。 |
| `VM2API_BRIDGE_TLS_KEY` | 可选 | PEM 服务端私钥路径；启用后同一个监听器提供 HTTPS API 与 HTTPS CONNECT 代理。 |

默认监听只对本机开放。跨不可信网络时启用原生 TLS（最低 TLS 1.2），并通过网络访问规则将客户端限制为 Sub2API。TLS 模式明确使用 HTTP/1.1，以支持透明 CONNECT。使用其他 TLS 入口时，入口必须支持 HTTP/1.1 CONNECT 原始隧道；普通网站反向代理规则通常不能直接替代该能力。bridge 客户端不读取 `HTTP_PROXY` 等环境变量，避免控制面密钥被意外送往环境代理。

每个状态目录只运行一个进程，启动会持有 `.bridge.lock` 排他锁。账号请求租约保存在内存中，本实现不支持 active-active 多副本；不能通过复制状态目录或共用 namespace 绕过锁来扩容。应让一套 bridge 服务只属于一个 Sub2API 安装。

保留原有共享密钥、namespace 和状态目录。目录权限为 0700，账号状态文件为 0600，写入经临时文件、fsync 和 rename 完成。整个账号状态使用共享密钥派生密钥的 AES-GCM 加密，包含单槽位 API key 和上游代理凭据。直接更换共享密钥无法解密旧状态；更换 namespace 会改变槽位身份。当前没有在线密钥轮换或 namespace 迁移工具。不要通过删除本地状态文件代替解绑，否则会遗留外部资源。

## Sub2API 连接

在 Sub2API 的 OpenAI VM 设置中配置 bridge 管理地址、CONNECT 代理地址和共享密钥。两个地址指向上述同一个 bridge 监听器；启用 TLS 时使用 `https://`，私网明文部署使用 `http://`。管理连接测试调用鉴权 `/v1/health`，不会创建资源或执行推理。

为账号创建槽位时，Sub2API 取得最新 AT，传入固定 `chatgpt_account_id`、当前账号上游代理和运行时类型。创建成功后推理走该账号专属槽位；OAuth 刷新和辅助流量使用账号对应的 CONNECT 凭据。后续请求携带最新 AT 时先同步身份，再提交推理请求。

## API 契约

除公开健康检查和 CONNECT 外，全部请求必须使用 `Authorization: Bearer <VM2API_BRIDGE_SECRET>`。账号 ID 必须为正整数的标准十进制形式。管理请求最大 64 KiB，推理请求最大 64 MiB。错误只返回安全的 `error.code` 和 `error.message`，不转发控制面原始错误或凭据。

| 方法 / 路径 | 行为 |
| --- | --- |
| `GET /health` | 公开最小存活检查，返回 `{"status":"ok"}`。 |
| `GET /v1/health` | 鉴权最小连接测试，返回同样 JSON。 |
| `POST /v1/accounts/{id}` | 幂等创建或重试。JSON 字段为 `name`、`access_token`、`chatgpt_account_id`、`upstream_proxy_url`、`runtime_type`。新绑定 201、复用 200。 |
| `GET /v1/accounts/{id}` | 返回安全状态、资源 ID 和活动租约数；不返回 API key、代理 URL 或 AT。 |
| `PUT /v1/accounts/{id}/identity` | 用 `access_token` 和原绑定 `chatgpt_account_id` 同步身份；不允许切换 ChatGPT 账号。 |
| `POST /v1/accounts/{id}/responses` | 转发原始 Responses body、HTTP 状态和流；使用单槽位 API key。bridge 请求取消时向上游传播。 |
| `GET /v1/accounts/{id}/responses` | 原生 WebSocket 握手后透明转发双向字节流；使用相同单槽位 API key，保留中断、控制与关闭帧。 |
| `POST /v1/accounts/{id}/responses/compact` | 转发给 vm2api 的 `/v1/responses/compact`；上游不支持时保留其 404，不通过 CONNECT 绕开槽位。 |
| `POST /v1/accounts/{id}/images/generations`、`POST /v1/accounts/{id}/images/edits`、`POST /v1/accounts/{id}/alpha/search` | 转发对应 vm2api 公共端点、原始 body 与 Content-Type；仅允许这些固定路径。 |
| `DELETE /v1/accounts/{id}` | 先阻断新流量，等待活动租约，撤销绑定 key，停止并删除对应槽位，确认外部记录消失后删除本地绑定。成功或已删除返回 204。 |

`runtime_type` 支持 `docker`（默认）与 `kvm`。具体运行时是否可用由 vm2api 返回的实际创建和启动结果决定。创建中或失败的账号不会转发请求。部分创建失败保留已知资源，允许相同配置重试或删除；不同身份、代理或运行时类型返回 409，需要先完成解绑。

Responses 可携带 `X-VM2API-Access-Token`、`X-VM2API-ChatGPT-Account-ID` 同步新 AT。两个头必须配套，账号身份必须匹配；这两个头以及 bridge 管理 Authorization 在发往 vm2api 前被移除。同账号的身份同步和取得上游响应头串行执行，流式响应体可并发；不同账号独立。上游推理响应原样返回用量数据，bridge 不自行生成或推测 usage。

转发使用固定的 Codex 功能头白名单，保留会话、续轮与工具相关字段；不传递客户端 Cookie、其他 API key 或 vm2api 的管理路由覆盖头。原生 WebSocket 持有账号租约直至连接关闭，删除须等待该连接排空。Sub2API 可以在下游断连后继续读取 HTTP 上游终态以完成用量记录；这与 bridge 自身收到请求取消后关闭上游的行为属于两个不同层次。

这些公共端点的转发不代表不同模型都支持对应能力。原生 Codex 模型清单和 WHAM 用量接口仍按其原始 schema 经过受管 CONNECT；vm2api 的 `/v1/models`、`/v1/usage` 返回格式不同，不能直接替换。图片 CDN 域名仍受前述 CONNECT 白名单限制。

CONNECT 使用 `Proxy-Authorization: Basic ...`：用户名是账号 ID，密码是 `hex(HMAC-SHA256(sharedSecret, "account:" + decimalAccountID))`。该凭据只授权其账号允许的 OpenAI CONNECT 目的地，不授权管理 API。配置了上游代理的账号在代理失败时返回 502，不会退回本机直连。

## 删除与恢复

DELETE 首先把绑定持久化为 `deleting`，立即拒绝新的 Responses 和 CONNECT。已有透明 CONNECT 可能来自 HTTP 连接池，bridge 无法区分其中的空闲连接和活动 TLS 请求；只有隧道关闭才释放租约。默认 30 秒内未排空返回 409 `account_busy`，资源和绑定继续保留且仍阻断新请求，不会恢复 ready。等待调用端连接池释放隧道后重新 DELETE；必要时可提高 drain timeout，并同步调整调用方请求超时。

清理失败返回安全的 502，并保留 `deleting` 供重试。删除只操作绑定槽位及其专属 key，不停止全部 vm2api、不删除其他账号，也不删除可能被共享的上游代理记录。成功完成桥接删除后，由 Sub2API 完成自身账号代理恢复和绑定移除。

SIGINT/SIGTERM 取消活动 HTTP 请求与 CONNECT 隧道，等待最多 15 秒关闭 HTTP 服务；关闭 bridge 不会自动删除 vm2api 槽位或吊销其 key。重启从加密状态恢复；待删除绑定继续拒绝流量，需重试解绑。

## 验证范围

仓库测试覆盖鉴权、RT 字段拒绝、幂等创建与部分失败恢复、固定账号身份、加密状态恢复、单账号隔离、原始推理载荷/状态/usage、续轮与工具字段、SSE 及时刷新与取消、固定公共端点、原生 WebSocket 中断帧/子协议/关闭及租约排空、CONNECT 认证与目标白名单、真实本地 SOCKS5/HTTP CONNECT 字节隧道、代理失败不回退、删除等待和重试、目录排他锁。测试使用本地假控制面和隧道，不会消耗实际 OpenAI 额度或操作生产 Docker/KVM。

部署前仍需在目标环境核验 vm2api API 兼容性、Docker/KVM 可用性、证书信任、账号上游代理、真实刷新/用量/推理网络出口，以及解绑后资源确实被回收。
