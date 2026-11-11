# 官方 v0.2.21 合并（2026-11-11）

## 来源与范围

- 官方仓库 Wei-Shaw/sub2api 最新正式版 v0.2.21，GitHub Release 发布时间为 2026-11-11 10:21:55（Asia/Shanghai）。
- 合并基线 ee58c0013c76ccd3f36439881bc848d342ec1c9d；目标标签提交 10ba8f9b4dc2e6023d4a9879ab77b21841babd10。通过 Git fetch 获取正式标签，完整三方合入剩余 68 条提交、127 个官方路径。
- 本地起点为 sub2api-custom / 34606511b09ef2c2c97a3e320c4bf368fd98b61c（v0.2.20-custom.8）。最初合并阶段仅调整源码，未提交、推送或发布；后续用户授权的完整发布范围见本文末节。
- 前次已合入的缓存写入 30m/12h TTL、请求体内存预算和 Antigravity 强制刷新不重复应用。

## 本轮更新

- Claude Fable/Mythos 5.2 的模型目录、Anthropic/Bedrock 映射、OpenCode 配置、缓存分时定价和 Fast 支持；修正 Sonnet 5.5 缓存读取兜底价格。
- 修复合成 reasoning effort 的映射、上限及最终校验，保留 GLM 降级语义；保留 encrypted_function_args 与 namespace；过滤不兼容的 CC Switch reasoning 封装。
- WebSocket 搜索激活按完整历史安全重连、后续轮错误禁止误重放首帧、首 token 时间遵循管理员配置；元数据不提前提交输出；原生图片错误状态码不被心跳覆盖。
- 支持 Codex Desktop 页面上下文归一化；拒绝非法 UTF-8；Responses 工具循环保持会话种子稳定。
- Anthropic 兼容入口应用账号错误策略；OpenAI 显式模型冷却覆盖 overload/slow_down 和流内 503，仅匹配实际错误字段。
- Refresh token 原子消费防止并发兑换；修复定价目录 hash 不匹配后的重试；安全审计任务入队立即唤醒 worker。
- Antigravity Pro Argon 独立冷却、并行工具图像序列修复；Gemini 非流式思考摘要不混入正文。
- API 密钥列表异步加载用量及覆盖索引；账号刷新保留行和数值；设置页保存浮标；EasyPay 退款成功码按实例配置；充值倍率最多 6 位小数。

## 定制兼容和并行修改

- 10 个三方冲突路径已处理。兼容入口保留余额耗尽的特殊 failover；保留代理预留校验和已获取槽位的释放条件。
- WS 保留 VM interrupt、逐轮时间、语义首输出默认规则；定制显式重试只使用已验证的当前轮 payload，其他后续轮失败不回放首帧。
- 保留定制 responsesapi.websocket_timing 元数据识别和现有计费规则；响应 tier 仅用于诊断，最终出站 tier 用于计费。
- 保留账号平台/分组面板、同步结果、容量及性能展示；保存浮标继续排除 backup 和 gptDirect。
- 合并前备份 15 个未提交计费文件及完整 Git diff；运行期间另有计费和 ForceOpenAIServiceTier 并行编辑。没有覆盖这些修改；在两个官方交汇文件中复核 effort 校验、GLM 映射和上限逻辑仍保留，见本地 final-compat-review.md。
- 工作区并行修改导致验证过程出现临时字段缺失，后端最终验证改在独立 validation-source 副本执行：起点提交 + 最初计费 WIP + 本次合并候选。测试结论仅覆盖该快照，不自动扩大到后续并行功能。

## 数据迁移

- 上游 249_add_usage_logs_api_key_cost_index_notx.sql 在本地追加为 351_add_usage_logs_api_key_cost_index_notx.sql，SQL 与上游字节一致。runner 的无事务重试修复及测试引用同步改号。
- 在 usage_logs (api_key_id, created_at) 上并发创建 INCLUDE (actual_cost) 的覆盖索引；不修改历史数据。失败遗留的无效索引在重试前清理。
- 合并前既有迁移目录 437 个文件（其中 406 个 SQL）哈希保持一致。
- 真实隔离 PostgreSQL 已确认迁移 checksum 正确、索引 valid/ready 均为 true；未连接或改动生产数据库。

## 验证记录

- 前端全量测试通过：488 个测试文件、4270 项测试。i18n、TypeScript、Vite 生产构建和 20 个修改文件 ESLint 通过。
- 独立快照后端 embed 构建、go mod verify 通过。并行字段生成完成后，当前工作区 embed 构建也重新通过；该构建不代替并行新功能的完整回归。
- golangci-lint 2.14.0 未全部通过：关闭诊断数量上限后共 114 项，与 custom.8 记录按文件、规则、完整诊断和重复数逐项一致；新增/减少均为 0。
- 独立固定快照后端全量单元测试通过：`go test -p 2 -tags=unit ./...`，74 个测试包、退出码 0。
- 独立固定快照 PostgreSQL/Redis 全量集成测试通过：`go test -p 2 -count=1 -tags=integration ./...`，68 个测试包、退出码 0。repository 整包 55.259s，service 整包 167.100s；保留原有依赖外部 DSN 的可选测试跳过条件。
- 原共享目录的一次 vmbridge 测试失败在隔离全套复跑中未复现，未为通过测试修改该模块。
- 修改 Go 文件 gofmt、git diff --check 通过；HEAD 与暂存区保持合并前状态。

原始官方增量、初始 WIP 备份、三方候选、独立验证源码、逐路径差异及日志保存在本地 .dev/official-v0.2.21-merge-20261111/。

## 定制发布 v0.2.21-custom.1

用户确认将本地所有修改一并提交并推送更新。发布范围包括本文的官方增量及下列同期修改，发布验证以 `.dev/release-v0221-custom1-all/` 中的最终记录为准。

- `FORK_VERSION` 与 `backend/cmd/server/VERSION` 同步为 `v0.2.21-custom.1` / `0.2.21-custom.1`。
- 统一按最终出站请求的服务档位计费，响应档位保留为诊断字段；兼容转换中的可计费部分流式响应同样携带正确档位。
- 粘性切换的强制缓存补偿仅作用于最终使用的 OAuth/setup-token 账号，不将按量 API Key、Bedrock 或 Vertex 的普通输入伪装成缓存读取。
- 分组 OpenAI 档位支持不强制、priority 和 ultrafast；保留旧 Fast 布尔 API 和鉴权缓存兼容，贯通管理端、持久化、HTTP/WS 与兼容转发。Excel BPS 不受此强制档位设置影响；免费 Fast 仅适用于 priority。
- SmartWS 设置并入分组编辑表单，保留版本冲突重载、保存失败反馈及部分保存状态提示。
- 额外迁移 `352_group_force_openai_service_tier.sql` 追加档位字段，将旧 `force_openai_fast=true` 映射为 priority，并更新分组鉴权缓存失效触发函数。所有既有 SQL 不改写。

安装时的迁移边界：351 为无事务并发索引；352 为新增列和既有 Fast 值回填，不改写历史用量或账单。升级前应备份数据库，并在同一维护安排内更新共享数据库的服务实例，避免旧实例无法理解 ultrafast 设置。回退旧程序时保留新增列和索引，旧程序只理解原有 Fast 字段，不保证 ultrafast 行为；需恢复旧行为时应先将相关组改回 priority 或不强制。此次发布不执行生产安装。

本次完整发布候选验证：当前工作区 74 个后端单元测试包、68 个 PostgreSQL/Redis 集成测试包、前端 490 个文件 / 4288 项测试通过；前端生产构建、后端 embed 构建、Ent/Wire 一致性、go mod verify、修改文件 ESLint、gofmt 和 diff 检查通过。发布辅助工具 11 项测试通过。清理一处新增测试类型推断告警后，完整静态检查 114 项与 custom.8 逐项一致，新增 0。以上使用全部本地修改后的候选，不再仅限前述合并快照。
