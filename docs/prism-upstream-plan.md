# Prism 账号通道（实验性）

已实现基于 2026-09-17 所提供 HAR 的文本通道，包括账号配置、网关转发、账号连接测试及渠道定时测试。已用用户提供的 Prism OpenAI Access Token 单独请求 `GET /auth/session`，确认可取得 Prism 会话，不必预先提供 `prism_session_token`。普通 Codex 客户端签发的 OAuth Token 是否同样被 Prism 接受，尚未用真实账号验证；不能由“同一个 OpenAI 账号”推断不同 OAuth 客户端的 Token 完全互通。完整生成流程的协议与集成验证仍使用本地模拟响应。

## 开启方式

1. 在账号管理编辑一个独立的 OpenAI OAuth / setup-token 账号，开启「Prism 通道（实验性）」。
2. 新开启的账号默认选择「使用账号认证」，使用账号已保存的 OpenAI Access Token 请求 Prism 会话，无需手填 Cookie。OAuth 账号有 Refresh Token 时沿用原 OAuth 客户端配置刷新；setup-token 账号使用已有 Access Token，过期后需要更新该凭证。此模式不支持 Codex Personal Access Token。
3. 默认超时 180 秒，可配置 30–600 秒。Conversation Action ID 留空使用捕获版本的默认值；网站更新后如需覆盖，填入新版本的 42 位十六进制标识。
4. 保存后运行该账号的文本测试，默认模型 `gpt-5.6-sol`。通过后再使用本站 API Key 调用。

开关默认关闭，旧账号缺少配置时仍走原有逻辑。关闭仅退出 Prism 分支，不覆盖原代理、并发、指纹、Codex 配置和模型映射。已开启 Prism 的旧配置如果没有 `auth_mode`，继续按手动 Cookie 模式运行；可以编辑后显式切换为账号认证。

也可以选择「手动 Cookie」，填入该账号已登录 Prism 的 Cookie，包含 `prism_oai_access_token` 和 `prism_session_token`。普通编辑不回显 Cookie，留空保留；复制或导出账号不携带 Prism 会话且默认关闭。账号认证失败会显示失败原因，不会自动退回旧 Cookie 或 Codex 通道。

配置存于 `extra.prism`，`auth_mode` 可取 `account` 或 `cookie`。账号认证使用已有 `credentials.access_token` / `credentials.refresh_token`；手动 Cookie 单独存于 `credentials.prism_cookie`。常规账号接口不返回凭证内容，候选调度缓存只带非敏感配置。账号同步、导入通过相同有效性校验：OAuth 账号认证至少需要 Access Token 或 Refresh Token，setup-token 账号认证必须有 Access Token，手动模式则必须提供完整 Cookie。

```json
{
  "extra": {
    "prism": {
      "enabled": true,
      "version": 1,
      "auth_mode": "account",
      "timeout_seconds": 180,
      "conversation_action_id": ""
    }
  }
}
```

共享或影子账号不支持此通道。默认仅公布 HAR 实际请求过的 `gpt-5.6-sol`；可通过原账号模型映射显式配置其他模型，但不代表已验证上游支持。该 HAR 没有证明 Astra 可用、不限量、独立额度或模型质量保证。

## 客户端使用

继续使用本站原地址和 API Key。支持文本形式的 `/v1/responses`、`/v1/chat/completions`，两者都支持 `stream: false` 和 `stream: true`。

Responses 示例（请求发往本站，不是直接发往 Prism）：

```json
{
  "model": "gpt-5.6-sol",
  "instructions": "请用中文回答。",
  "input": "你好",
  "reasoning": {"effort": "medium"},
  "stream": true
}
```

Chat Completions 示例：

```json
{
  "model": "gpt-5.6-sol",
  "messages": [{"role": "user", "content": "你好"}],
  "reasoning_effort": "medium",
  "stream": false
}
```

多轮请求需要每次发送完整文本历史。每次调用都会在 Prism 创建独立项目、会话及沙箱，不复用不同用户的上下文。本次未实现项目清理，生成的项目会保留在上游。

推理强度原样传入 `metadata.reasoning_effort`，缺省为 `medium`。只验证过 HAR 中的 `medium`；其他级别由上游决定是否接受，不将 `ultra` 静默改成另一级别。系统、开发者、用户、助手文本都保持原角色转发，不注入抓包中的私人提示或网页系统提示。

Responses 支持 `text.format.type: "text"` 和 `text.verbosity: "low" | "medium" | "high"`。省略或 `medium` 保持原对话不变；`low` / `high` 通过附加的开发者文本表达简洁/详细偏好，不覆盖调用者的原始消息。此为尽力实现的提示映射，并非已验证 Prism 支持原生 verbosity 参数。`json_object` / `json_schema` 仍明确拒绝，并通过 `text.format.type` 指出不支持的配置。

## 支持范围与返回行为

- 已接通项目创建、访问权校验、当前登录身份读取、沙箱、文档同步及资源凭证初始化、Conversation Server Action、提交生成、状态轮询、文本结果解析。
- 提交生成支持 `started` 后轮询，也支持直接返回 `completed`（成功或失败），后者不要求存在 `turn_state`。上游明确返回失败时展示安全的错误类别和 HTTP 状态，不再误报为仍可能生成中的解析错误；不回显上游调试信息、`message` 或 `rootCause` 原文。
- 轮询传回最近一次响应中的完整 `turn_state`，不拼造 job ID、服务端文件路径或游标。
- 原代理和并发设置用于全部上游 HTTP 请求；TLS 继续使用原有账号传输配置。入口在 Codex 认证、请求改写和插件路由之前分流。
- 保留外围本站鉴权、分组权限、账号调度与并发限制。手动账号测试和现有定时渠道测试复用同一个 Prism 客户端。
- `stream: true` 等待期间发送心跳，收到完整文本后以标准 SSE 事件返回最终内容。此处不代表上游提供逐 token 增量；首输出延迟是完整文本到达时间。
- 参数校验失败只返回一份 JSON 错误；已经开始流式输出时只发送一次流错误终态，网关不再追加通用失败事件。流内上游失败计入 Ops 失败统计，不因 HTTP 200 被视为成功。
- 成功响应只包含标准文本输出和真实存在的 token 用量，不返回上游沙箱令牌、调试数据、项目路径或文件变化。
- HTTP 重定向被禁止；所有请求目的地固定为 `https://prism.openai.com`。上下游客户端 Cookie、Authorization 不混用。
- 不自动重试或切回 Codex，尤其提交生成后结果不确定时不会重复提交。取消请求会停止本地轮询和连接；未验证上游取消接口，因此不保证终止已提交的上游任务。
- 登录态失效、Cloudflare HTML 403、Server Action 变化等返回脱敏的阶段与错误码。账号认证模式从 Access Token 获取 Prism 会话；OAuth Access Token 可按原 Refresh Token 和客户端配置自动刷新。手动模式仍需要管理员更新失效的 Cookie。没有实现或声称绕过网页登录验证、订阅资格或 Cloudflare 挑战。

未实现的能力在请求/账号能力层明确拒绝，不静默丢弃：客户端自定义工具及工具结果、图片/音频/文件、`previous_response_id` 或服务端 `conversation` 续接、compact、WebSocket、Anthropic Messages、token-count 接口、结构化 JSON 输出及采样/长度限制等未捕获参数。完整 Codex 编程客户端通常携带工具，因此目前不能将此文本通道视为完整 Codex 替代。

## 用量与计费

捕获样本的终态响应没有 token usage。缺失时：

- 客户端返回 `usage: null`；使用记录按现有存储结构记录 0 token、0 费用，并以 `upstream_endpoint=/api/llm/response_with_tools_start` 区分此通道。这里的 0 表示未取得统计，不是上游确认没有消耗。
- 不估算 token、不执行模型按次兜底收费，也不伪造缓存命中。
- 非流式响应的 `X-Sub2api-Usage-Source` 为 `unavailable`；如实际返回合法 usage 则为 `upstream`。流式头为 `pending`，以最终事件的 usage 为准。
- 如果将来上游返回合法的 input/output/total 及缓存 token，保留真实计数并使用现有计费链路；不声称上游响应确认了未回显的实际模型。

## 文件与验证

- `backend/internal/pkg/prism/client.go`：独立网页协议客户端及有限状态轮询。
- `backend/internal/service/prism_account_config.go`：开关、凭证校验和默认模型。
- `backend/internal/service/prism_gateway_adapter.go`：Responses / Chat 请求校验、独立转发、SSE 与安全错误。
- `backend/internal/service/prism_account_connection.go`：账号与定时测试接入。
- `frontend/src/components/account/PrismAccountSettings.vue`：账号编辑配置。

离线测试覆盖初始化、身份/地址校验、最新状态回传、两次请求隔离、Cookie 轮换、重定向防护、取消和超时、异常响应、缺失/真实 usage、账号配置保存/复制/脱敏及调度能力。集成测试覆盖网关与账号测试使用相同路径、代理/并发保留、重试隔离、SSE、上下文回放和不支持参数提前拒绝。

标准对外协议参考：

- https://developers.openai.com/api/reference/resources/responses/methods/create
- https://developers.openai.com/api/docs/guides/streaming-responses

这些文档定义本站响应格式，不证明 Prism 私有端点具备同等能力。没有把 HAR、原始 cURL、私人内容或登录凭证写入代码和测试。
