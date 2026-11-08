# 官方主线合并（2026-11-08）

## 来源与范围

- 官方 Wei-Shaw/sub2api main：0eee80ba87aa4f3aff7282bf1695aa6dc0471c36 → 6b4b37db43443b5e88e424fe3c69602cc3810dfb（v0.2.20）。
- 合入两端之间全部 52 条提交的源码增量，涉及 135 个官方路径，包括测试、模型价格资源、赞助信息及多语言 README；提交数含合并提交和版本元信息。
- 本地基线：915cc0ed24e896f404a1fe0918ee055de4acd40c / v0.2.19-custom.15。
- 源码合并阶段保留定制发布编号 v0.2.19-custom.15，未提交或发布；后续发布范围见本文末尾。ranxi 功能分支的首次模型范围、429 更换 2FA、API Key 风险巡检等不属于此次官方主线增量。

## 合入内容

- GPT-6.1 Terra 官方 Codex 描述、500k 默认窗口、effort 与兼容缓存键、OAuth/setup-token 订阅长上下文附加费豁免；API Key 原有长上下文计费保留。
- GPT Image 2.6 原生 complexity、SVG、markup、帧数/帧时长，直接图片路径的降级保护和完整 JSON 转 SSE 完成事件，图片测试参数和官方价格资源更新。
- Responses 完成事件保留消息内容，兼容转换保留缓冲 reasoning，Gemini 工具响应保留媒体，Antigravity 原生 Gemini 流式与 responseJsonSchema 修复。
- 请求体读取超时、断开与非法格式分类；API Key 非法 UTF-8 与客户端断开不再误报 500；Grok Voice 错误分类；413 错误透传规则优先。
- 强制路由跳过额外 Responses 探测，WS HTTP 桥接执行 Codex 指纹收敛，WS 逐轮首 token 指标回归。
- Antigravity 真实 5h/weekly 配额和 Pro Argon 展示、图像 INTERNAL 500 只冷却对应模型、可配置账号惩罚；账号连接测试发送管理员提示词并留出回答空间。
- Anthropic setup-token 无 5h 头时保留周额度，Cloudflare REST messages 去除不支持的 beta 查询参数。
- Codex 与 CC Switch 导出使用 GPT-6.1 Sol 默认模型、API-key 认证、实时搜索与图片配置。

## 定制兼容

- 鉴权同时拒绝非法 UTF-8 与本地存储摘要前缀，保留原有摘要凭据安全边界。
- 413 规则抽取为官方公共方法，同时保留未覆盖消息的定制脱敏/客户提示逻辑及其他定制故障转移分支。
- 图片完整 JSON 转完成事件前仍优先处理余额耗尽；Agent Identity 任务恢复、敏感信息清理和其他图片错误边界保留。
- 本地已有 gpt-image-2.6 基础别名直调，继续支持，并将禁止降级换模型的保护扩展到该别名；没有猜测新增基础别名价格。
- WS 逐轮首 token 沿用本地首个语义输出判定，排除元数据、空 delta 和结束事件，同时运行官方逐轮回归。
- Antigravity 普通连接测试默认 hi / 256 token；定时测试显式输出预算优先，完整终态验证和内容检测提示仍生效。
- 上游 Cloudflare 测试适配本地账号连接测试的提示词参数；增加存储摘要拒绝认证和直接图片流式 JSON 余额错误回归。
- BPS/Excel、Mihomo、打票总开关及账号指标规则保留；已退役账号 HTTP 保护和分组零缓存保护不恢复。

## 数据与本地修改

- 此次官方增量没有新增数据库迁移，430 个既有迁移目录文件逐文件 SHA-256 保持一致。
- 合并前 RecentRequestsCell.vue 的空状态/错误状态右对齐修改逐文件 SHA-256 保持一致。
- 345 个不在官方增量中的 BPS/Mihomo/打票/账号指标相关独立文件与基线逐字节一致；共享网关交汇处按上述约定适配。

## 验证

- 全量后端单元测试通过：go test -p 2 -tags=unit ./...，71 个测试包。
- 全量后端 PostgreSQL/Redis 集成测试通过：go test -p 2 -tags=integration ./...，65 个测试包。
- 前端 476 个测试文件、4105 项测试通过；前端生产构建、修改文件 ESLint 通过。
- 请求体超时、直接图片、鉴权拒绝、WS HTTP 指纹与逐轮语义首字的定向 race 检查通过。首轮 WS race 名称未匹配到测试，已按实际测试名补跑并通过，不将空执行算作覆盖。
- 嵌入最新前端产物的后端构建、go mod verify、修改 Go 文件 gofmt、git diff --check 通过。
- golangci-lint 2.14.0 仍报告 61 项既有问题；与 custom.15 分支 CI 按文件、完整诊断文本、规则及重复数逐项一致，新增和减少均为 0。
- 首轮后端单测遇到 3 处上游测试调用与定制提示词参数不兼容，已适配。扩展基础 gpt-image-2.6 别名回归时未将无动态价格测试套用于缺少官方静态价格的别名，没有为该别名猜测费率。最终全量测试均通过。

原始差异、逐路径覆盖清单、冲突适配脚本、保护文件校验及完整验证日志位于本地 .dev/official-main-merge-20261108/，结构化结果见 verification.json。

## 定制发布 v0.2.20-custom.1

- 按用户“提交本地所有修改，推送更新”要求，将此次官方主线合并和原有 RecentRequestsCell.vue 右对齐修改一并纳入发布。
- FORK_VERSION 与 backend/cmd/server/VERSION 同步为 v0.2.20-custom.1 / 0.2.20-custom.1。
- 发布前逐文件核对已通过上述测试的源码快照；除版本元信息和本段发布说明外，源码保持一致。
- 发布检查范围：远端提交/树/附注标签、五平台安装包字节与 SHA-256、GHCR 双架构及 latest、更新清单和来源更新器；结果保存在本地 .dev/release-v0220-custom1-all/。
- 此次发布不执行生产安装。
