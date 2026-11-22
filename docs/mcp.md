# 平台管理 MCP（OAuth）

平台内置远程 MCP 服务，通过 **Streamable HTTP + OAuth 授权码 / S256 PKCE** 接入。客户端只需配置 MCP 地址；连接时打开浏览器登录平台，再明确同意个人或管理员范围、只读或读写权限。无需创建、复制或保存静态令牌。

## 部署

MCP 默认关闭。只有显式配置稳定、可信的公开 MCP 地址后，才会开放 MCP 资源端点、OAuth 发现及客户端注册接口，侧栏也才会显示 **MCP 接入**。例如：

```sh
MCP_PUBLIC_URL=https://api.example.com/api/v1/mcp
```

也可以在配置文件中设置：

```yaml
mcp:
  public_url: https://api.example.com/api/v1/mcp
```

地址必须是站点根路径下的 `/api/v1/mcp`，不带尾斜杠、查询参数或片段；OAuth 发现端点同样位于站点根路径，因此不支持挂在子路径（如 `https://example.com/sub2api/`）下部署。生产环境使用 HTTPS；本机开发允许 localhost 或回环 IP 的 HTTP 地址。服务不会从前端 URL、请求 Host 或转发头推断该地址；未配置时 MCP 及其 OAuth 接口均返回 503。清空该配置即可关闭 MCP，已签发的令牌随之无法使用。

同域部署只需上述 MCP 地址。前后端分域时，另外配置平台前端 URL（管理设置或 `SERVER_FRONTEND_URL`），用于定位浏览器授权页。反向代理需转发 `/api/v1/mcp`、`/api/v1/mcp/oauth/*` 及下列 `/.well-known/*` 发现端点，保留 Authorization、Accept 和 MCP 协议请求头。浏览器客户端访问 MCP 资源仍需符合平台的 CORS/Origin 白名单。

## 连接

1. 在支持远程 MCP OAuth 的客户端中添加 `https://api.example.com/api/v1/mcp`。
2. 客户端发现授权服务器，注册公共客户端，并自动生成 PKCE 参数。
3. 浏览器打开平台登录与授权页面。检查应用名称和返回地址，选择授权范围及权限，再点击同意。
4. 客户端自动交换授权码并刷新访问令牌。

适用于使用 `mcpServers` 和 `url` 配置的客户端：

```json
{
  "mcpServers": {
    "sub2api": {
      "url": "https://api.example.com/api/v1/mcp"
    }
  }
}
```

不同客户端的配置结构可能不同；传输使用 Streamable HTTP，认证使用 OAuth。平台不接受模型调用 API Key、浏览器 JWT、管理员 API Key 或静态 MCP 令牌作为 MCP 凭证。

## 授权与撤销

默认授权为**个人范围、只读**。管理员可以在明确同意时选择管理员范围；启用平台敏感操作二次验证时，授予管理员范围同样需要 step-up。授予**管理员范围的读写**权限时，无论是否启用该开关，都必须已开启 TOTP 并完成一次 step-up 验证。

- **个人范围**：只操作当前用户的数据，即使授权人是管理员也不会开放管理工具。
- **管理员范围**：仅管理员可授予，可使用目录内管理工具及自己的个人工具。
- **只读**：不列出写工具，直接调用写工具同样会被拒绝。
- **读写**：开放对应范围内的修改工具，包括删除 API Key、调整余额、停用账户和撤销订阅等操作。

OAuth scope 为 `mcp`，实际工具权限由浏览器中确认的授权范围与读写选项决定。刷新不会扩大权限，改变权限需要重新授权。客户端应用名来自注册请求，并不代表平台验证了该应用；授权页面同时显示其回调地址。

授权码有效期 5 分钟且只能兑换一次。访问令牌有效期最多 10 分钟；授权及刷新令牌最长 30 天，刷新令牌每次使用都会轮换。重复使用授权码或已消费的刷新令牌会撤销该授权。服务端仅保存凭证摘要。

登录平台后，侧栏 **MCP 接入**（`/settings/mcp`）可查看并撤销已授权应用。每用户最多 20 个有效授权。撤销、密码或登录标识变更、用户停用都会阻止后续调用及刷新；管理员失去角色后，其管理员授权也失效。“撤销全部会话”会同时撤销该用户的全部 MCP 授权。停用用户会持久撤销其全部授权，取消管理员角色会持久撤销其管理员授权，之后重新启用或重新授予角色也不会恢复旧授权。页面不显示访问或刷新令牌，MCP 工具无法自行授予新授权。

## 工具目录

客户端通过 `tools/list` 获取当前 OAuth 授权可用的工具及完整参数说明。

| 范围 | 工具 | 用途 |
| --- | --- | --- |
| 个人 | `user_get_profile`、`user_get_platform_quotas` | 查看个人资料、余额、限额和平台额度 |
| 个人 | `user_list_groups`、`user_list_channels` | 查看可用分组、渠道及模型 |
| 个人 | `user_list_api_keys`、`user_get_api_key` | 查询自己的 API Key 元数据和限额 |
| 个人 | `user_create_api_key`、`user_update_api_key`、`user_delete_api_key` | 创建、修改、停用或删除自己的 API Key |
| 个人 | `user_list_usage`、`user_get_usage_stats`、`user_get_dashboard` | 查询请求明细、用量统计和仪表盘 |
| 个人 | `user_list_subscriptions`、`user_get_subscription_summary` | 查看自己的订阅和剩余额度 |
| 管理员 | `admin_get_dashboard`、`admin_get_ops_overview` | 平台经营统计和运维概览 |
| 管理员 | `admin_list_users`、`admin_get_user`、`admin_update_user`、`admin_update_user_balance` | 查询用户，调整状态、限额、分组权限和余额 |
| 管理员 | `admin_list_groups`、`admin_get_group`、`admin_create_group`、`admin_update_group`、`admin_delete_group` | 管理分组基础配置、价格倍率、额度和模型白名单 |
| 管理员 | `admin_list_accounts`、`admin_get_account`、`admin_update_account`、`admin_set_account_schedulable`、`admin_clear_account_error` | 管理上游账户的状态、调度、并发、优先级和有效期 |
| 管理员 | `admin_list_usage`、`admin_get_usage_stats` | 查询全站请求明细和用量统计 |
| 管理员 | `admin_list_subscriptions`、`admin_get_subscription`、`admin_assign_subscription`、`admin_extend_subscription`、`admin_revoke_subscription` | 查询、分配、延长和撤销订阅 |

工具只开放明确列出的字段；不提供任意 HTTP 请求、任意 URL、密码/角色修改、身份绑定、上游凭证管理、备份恢复或支付工具。普通查询和修改结果会去除 API Key、上游令牌等凭证。`user_create_api_key` 的创建结果包含新 API Key，便于配置应用；不要将该结果公开分享。

现有业务权限、资源归属、平台模式限制、管理合规确认、限流和操作审计继续生效。模型返回的建议不会绕过这些规则。MCP 的每次工具调用（包括只读查询）都会写入操作审计，认证方式为 `mcp_oauth`，并附带 `mcp_grant_id` 和 `mcp_tool`。

## 参数示例

工具参数使用 `id`、`query`、`body` 三种字段。路径中的资源 ID 放在顶层 `id`，查询条件放在 `query`，修改字段放在 `body`。未提供的更新字段保持原值；未知字段会被拒绝。分页从 1 开始，每页最多 100 条。

创建 API Key、调整用户余额和延长订阅还要求顶层 `idempotency_key`，可使用 UUID。同一次操作重试时保持该值和参数一致；新的操作使用新值。平台沿用已有幂等机制，避免超时重试造成重复创建或重复扣加款。

查询自己的用量：

```json
{
  "name": "user_list_usage",
  "arguments": {
    "query": {
      "page": 1,
      "page_size": 20,
      "start_date": "2026-10-01",
      "end_date": "2026-10-28",
      "timezone": "Asia/Shanghai"
    }
  }
}
```

日期格式为 `YYYY-MM-DD`，起止日期均包含在统计范围内；时区使用 IANA 名称。管理员自定义统计范围请同时提供起止日期。

创建一个有额度和有效期的个人 API Key：

```json
{
  "name": "user_create_api_key",
  "arguments": {
    "idempotency_key": "create-development-key-20261028-1",
    "body": {
      "name": "development",
      "group_id": 12,
      "quota": 10,
      "expires_in_days": 7
    }
  }
}
```

先使用 `user_list_groups` 确认可用分组 ID。上述操作要求个人或管理员范围的读写授权。

暂停一个上游账户的调度：

```json
{
  "name": "admin_set_account_schedulable",
  "arguments": {
    "id": 42,
    "body": { "schedulable": false }
  }
}
```

余额调整和订阅分配会改变实际计费权益。发送前确认目标和金额/天数；客户端超时后先查询结果，避免盲目重复调用。

## 协议与发现

| 端点 | 用途 |
| --- | --- |
| `GET /.well-known/oauth-protected-resource/api/v1/mcp` | RFC 9728 资源元数据；根路径 `/.well-known/oauth-protected-resource` 也可发现 |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 授权服务器元数据 |
| `POST /api/v1/mcp/oauth/register` | RFC 7591 公共客户端动态注册，返回 HTTP 201 |
| `/oauth/mcp/authorize` | 前端登录与明确同意页面 |
| `POST /api/v1/mcp/oauth/token` | 授权码兑换与刷新，表单编码 |
| `POST /api/v1/mcp/oauth/revoke` | RFC 7009 撤销，表单编码 |

未认证 MCP 请求返回 401，`WWW-Authenticate` 提供 `resource_metadata` 和 `scope="mcp"`。客户端只支持 `token_endpoint_auth_method=none`、`response_type=code`、S256 PKCE，以及授权码/刷新两种 grant type。回调 URI 必须与注册值精确匹配，使用 HTTPS 或本机 HTTP；不支持通配符。当前不提供远程 client metadata URL 自动抓取。注册请求体不超过 4 KiB；`client_name` 最多 100 个字符，且不得包含控制字符、双向文本控制符或零宽字符等不可见字符。

授权请求和授权码兑换必须携带精确匹配 MCP 地址的 `resource`。刷新可省略 `resource`，此时沿用原授权；传入不同资源会被拒绝。`state` 会原样返回客户端。授权码与客户端、回调、资源和 PKCE 绑定；客户端应安全保管 verifier 和刷新令牌。

查看发现信息：

```sh
curl 'https://api.example.com/.well-known/oauth-protected-resource/api/v1/mcp'
curl 'https://api.example.com/.well-known/oauth-authorization-server'
```

服务使用官方 Go MCP SDK 的无状态 Streamable HTTP 模式，POST 返回 JSON；GET 事件流和 DELETE 会话操作返回 405。每个 HTTP 请求只能包含一个 JSON-RPC 对象，JSON-RPC 批量请求（包括未声明 `MCP-Protocol-Version` 的旧版本请求）一律返回 400。每次 HTTP 请求重新校验授权，响应禁止缓存。工具结果以单个文本块返回 JSON `{"http_status": …, "result": …}`，不再重复写入 `structuredContent`；编码后超过 2 MiB 的结果会被拒绝，请缩小查询范围或分页大小。业务 API 失败以工具结果 `isError: true` 返回，原 HTTP 状态位于 `http_status`。工具执行限制 30 秒并传递客户端断开取消；写操作超时后应查询状态再决定是否重试。

`/api/v1/mcp` 在认证前先按客户端 IP 限流（每分钟 600 次），认证后再使用独立的按用户入口限流桶；每个实际工具调用只消耗一次管理面全局额度，聚合统计仍受 Heavy 限流。授权的最近使用时间每分钟最多写入一次。动态注册、兑换和撤销接口有独立 IP 限流。上述 IP 限流把同一 IPv6 /64 网段视为一个客户端，Redis 不可用时拒绝请求。

## 开发

工具目录位于 `backend/internal/managementmcp/catalog.go`。新增工具应绑定已有管理路由，使用封闭参数 schema，并覆盖权限、资源归属、参数边界及凭证脱敏测试。

OAuth 存储由 `249_mcp_oauth.sql` 创建。动态注册不写数据库：`client_id` 是服务端签名的无状态标识，自带客户端名称、回调 URI 摘要和 grant type，签名密钥首次使用时生成并保存在 `security_secrets`，多实例共享。只有用户同意授权后才写入授权记录，因此匿名注册不会占用存储，也没有可被占满的全站名额。每次新授权时有界清理过期 7 天以上的授权。浏览器使用 JWT 保护的 `/api/v1/user/mcp/config` 获取服务端配置的连接地址、`/api/v1/user/mcp/authorize` 预览/决定授权，以及 `/api/v1/user/mcp/grants` 列表、撤销接口。客户端协议端点返回标准 OAuth JSON，浏览器接口沿用平台响应格式。

## 个人消费对账

在用户面板的「使用记录」页选择日期、模型和 API Key，即可查看逐笔明细或下载 CSV。「全部密钥」覆盖登录用户的所有 Key。Key 筛选器包括已停用和已删除的 Key。历史名称以查询时仍保存的 Key 元数据为准。

| 个人只读 MCP 工具 | 登录用户 REST 接口 | 用途 |
| --- | --- | --- |
| `user_list_billing_keys` | `GET /api/v1/usage/keys` | 按 `page`、`page_size` 取 Key ID、名称、状态及删除时间，包括未产生消费的 Key |
| `user_sync_usage` | `GET /api/v1/usage/sync` | 按游标读取本人已落库的消费明细，省略 `api_key_id` 即可跨 Key 同步 |
| `user_get_usage_retention` | `GET /api/v1/usage/retention` | 读取当前原始明细的自动清理天数，0 表示关闭自动到期清理 |
| — | `GET /api/v1/usage/export` | 以 CSV 下载同一数据库快照中的明细，支持与页面相同的筛选及排序 |

第三方程序集成使用站点启用的 MCP 和个人只读授权。REST 接口使用用户面板登录凭证。MCP 授权期限和续期方式见上文「授权与撤销」。

首次调用 `user_sync_usage` 可传 `{"query":{"page_size":100}}`。从返回的业务数据中读取 `items`、`next_cursor` 和 `has_more`。将每行按明细 `id` 写入本地，并在同一本地事务中保存 `next_cursor`。下一次调用携带同一组筛选条件，以及 `query.cursor`。当 `has_more` 为真时继续翻页，为假时保存游标，稍后再次轮询。相同游标可用于失败重试。遇到 429 时按限流提示延迟重试。长期连续同步请省略结束日期。切换账号、筛选条件或服务端签名密钥后，从首批重新读取，按明细 ID 去重。

每条明细包括请求 ID、Key ID 和名称、时间、请求模型、输入和输出 Token、缓存写入和读取 Token，以及客户计费金额 `actual_cost`（USD）。金额使用十进制字符串，汇总时使用十进制运算。已采集的 `http_status` 表示实际发送的 HTTP 响应头状态。历史缺失值、WebSocket 独立轮次显示为空；流式请求还可能在响应头发出后失败。失败请求详情继续遵循站点的用户错误日志可见性设置。

同步按数据库写入事务及明细 ID 推进，同一请求时间可以对应多条记录。存在较早未提交事务时，新记录可能延迟到后续轮询。用户只能读取仍保留的原始记录；延长保留时长无法恢复已清理数据。CSV 在生成成功后开始下载，包含 UTF-8 BOM。单次文件上限 128 MiB，生成上限 2 分钟；超出时可以按日期、Key 或模型分批，或使用游标同步。
