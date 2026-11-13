# SmartWS / Excel BPS 合并评估

范围：静态对比本地 `/Users/dean/Project/sub2api`（v0.2.21-custom.3）与下载版 `/Users/dean/Downloads/sub2api-source`（0.2.21-yoshub.29）。本报告的优化建议来自源码对比；本次另对 VM/IPv6 兼容边界运行本地回归测试，未调用真实上游。SmartWS/BPS 的候选优化尚未实施。

## 结论与实施优先级

不应整体覆盖 SmartWS 或 Excel/BPS。用户版本已有账号隔离、内存预算、独立 Excel 授权、Mihomo/IP 池与故障恢复等定制；下载版整文件移植会回退这些能力。建议后续单独分三个小补丁实施，当前 IPv6/VM 工作不混入这些行为变化。

1. **SmartWS 搜索能力匹配**：连接首次请求未声明 web_search 后，后续需要搜索的请求不应复用该连接。给 connection/lease 增加布尔能力标记，在已有校验后的 response.create 提取标记，在 capacity.go 过滤不兼容空闲连接；容量满时仅回收空闲且不兼容的连接。保留所有预算、身份作用域和分发后禁止重试规则。下载版 `pkg/smartws/search_test.go:20` 的测试覆盖无搜索转搜索、新连接及 perKey=1；本地尚无同类用例。
2. **SmartWS 写入阶段取消**：在 WriteMessage 之前注册 context.AfterFunc 关闭连接，完成写入后停止回调，避免取消发生在写入期间仍等写超时。本地 `smartws.go:541` 的取消观察器在写入后才建立。保留 SentError 和禁止重放。下载版 pool_test 的取消测试在 Try 返回后取消，不能证明写入阶段修复；应补受控阻塞连接测试，验证及时退出、释放活跃槽位、无第二次分发。
3. **BPS 可见输出后禁止未知工具重新生成**：下载版 stream.go 的 visibleContent/clientStream 判断值得局部合并。仅限制现有 unknown-tool 分支的重新生成；不能一刀切禁止已知工具的格式校正。复用下载版 `stream_repair_boundary_test.go` 的文本、refusal、reasoning summary、done/item、空事件和非流式矩阵，并保留本地 `TestToolRepairKeepsOneResponseAndAtomicToolBatch`（先输出 Working. 再修正已知工具）的行为。不引入下载版未知工具 continuation 修复。

## SmartWS 保留边界

- 本地 `pkg/smartws/smartws.go:383-397` 校验正数 ContentLength、请求体内存作用域、4*(ContentLength+1) 预算及精确读取长度，下载版删除了这些保护。
- 本地 key 包含目标、代理、group/user/key 作用域和完整握手头指纹（:437），并要求可靠 session header 才复用。
- 本地仅 OAuth 流式标准 Codex Responses，拒绝 stream:false、previous_response_id/conversation 及 metadata 变体（:628-655）；ticket 绑定请求排除。
- 下载版 AllowTarget/WireMessage 等 Options 扩展连带 API Key、非流式、无可靠 session 的连接复用及大量网关改动，不宜合并整模块。
- 两版 handshake_transport.go 相同；下载版将握手 400 从回退改为 HTTP 错误，须有上游兼容证据后再决定。
- 本地已有 TestPoolReuseAndIdentityIsolation、TestFallbackOnlyBeforeDispatch、TestPoolBudgetAndEligibility、TestAffinityIncludesProxyAndHeaders、TestSmartWSAttemptSurvivesNestedForwarders、TestSmartWSAttemptStopsReplay，必须保留。
- 次要候选：实际内容延迟与连接复用观测、限量故障尾部 usage、cancelled 终态/idle timeout。涉及计费和故障语义，宜独立实施；不能把连接上限提升当作性能验证。

## Excel/BPS 不能整体替换的证据

- 本地 `openai_excel_bps_transport.go:46-80` 有 Mihomo lease/IP 池及 transient scope；下载版 acquireFor 直接返回 nil，并拒绝导入的 Mihomo 配置。本地“确认未发送才可换出口”的 trace 规则也应保留。
- 本地 `openai_excel_oauth.go:385` 取得 Codex 账号上的独立加密 Excel grant，401 仅使本次 Excel grant 失效（openai_excel_bps.go:32-66），不破坏主 Codex 凭据；下载版只接受 Excel OAuth 主凭据。
- 本地 403 移组/停用和持久化自动恢复有 claim/CAS 规则；下载版是不同的路由/模型 cooldown，不可替换。
- 本地 `openai_excel_bps_usage.go:25-26` 对 cache-write-as-input 清理 input/prompt 两类 details 的 ttl_30m_tokens/ttl_12h_tokens，下载版缺失。
- 保留图片上传与请求使用相同绑定代理、全局/账号开关、原计费以及不自动回退原生上游。

## 其他可分批合并的候选

| 候选 | 建议 | 重要边界 |
|---|---|---|
| 本地兼容消息 ID 清理 | 低风险局部合并 history_messages.go 和 history_message_id_test.go | 仅移除 item_ 后正好 24 位十六进制的消息 ID；保留正文、原生/未知 ID 与工具引用约束 |
| 工具 schema 安全诊断 | 合并有限工具名、JSON pointer、已知原因 | 不输出参数值/原始编译器错误；不放宽外部引用规则 |
| upstream/bridge failure 分层及 response.cancelled 终态 | 中风险独立补丁 | SSE 语义 status 不等同实际 HTTP 状态，不触发错误的 401/403 凭据处理 |
| 不完整校正流 usage 保留 | 先查清不同路径再合并 | 本地已有 TestCorrectionDisconnectRetainsReportedUsageWithoutDoubleCounting，不能称完全缺失 |
| 无进度超时 | 默认保持关闭/显式配置后引入 | 默认 120 秒可能误杀长思考；测试碎片数据、纯心跳、下游背压及取消无重放 |
| 图片 warn/auto_compact | 先做默认关闭的 warning-only；自动压缩后置 | 需稳定会话/Redis，压缩改变上下文并额外计费，需真实客户端 checkpoint 回传与计费验收 |
| Redis replay/catalog | 多实例需求成立时独立设计 | 存储原生工具记录，需隔离 namespace/version、CAS、TTL、迁移与 Redis 故障策略 |

不要扩大请求体/图片/内存上限来追求功能对齐；下载版的 1 GiB/64 GiB 等上限不是本机容量依据。未知工具 continuation、agent delegation 同样后置。

## 避免重复修复

本地 image_relay.go:419-450 已有 private no-store、nosniff/no-referrer、nil receiver、读取时检查过期及 owner-local token；下载版新增测试不代表本地缺少修复。关闭模式时立即关闭 relay 可能使已有链接失效，不宜直接移植。

## 验收建议

三个优先补丁逐个实施并运行已有 smartws/相关 service/basispoints 定向测试。每个补丁先增加真正覆盖缺陷的用例，再检查身份隔离、内存预算、取消后不重放、故障 usage、独立 grant 与 IP 池行为。当前静态结论不代表运行或线上兼容性验证。
