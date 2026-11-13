# 当前自定义功能清单

维护日期：2026-11-13。范围为当前工作区的有效实现；发布状态以版本标签和发布产物为准。

| 功能 | 当前能力 | 主要实现 / 使用说明 |
| --- | --- | --- |
| 主动检测执行 | 多分组、多模型、多题目；冻结名单；账号排除；Cron 和时段；失败重试；请求与 Token 预算；健康复测与退避；跨实例执行租约 | backend/internal/service/scheduled_test_runner_service.go；channel-test-strategies.md |
| 主动质量判定 | 糖果、鹈鹕、自定义题、随机能力题、模型元数据一致性；组合条件、全部通过、连续整轮通过；按模型暂停与恢复，全部配置模型失败才暂停账号；账号列表展示各模型状态；通过率、切组、投票及管理员复核 | channel-quality.md；channel-quality-protection.md |
| 独立视觉评审 | 隔离 Chromium 截图；独立裁判账号和预算；失败复判；待复核与人工改判；作品预览和历史 | renderer/；backend/internal/service/scheduled_test_auto_review.go |
| 渠道状态展示 | 完整输入口径缓存率、输出 TPS、近期请求、健康条、分组作品画廊；聚合追赶和迟到数据补算 | frontend/src/features/channel-monitor-v2/；channel-quality.md |
| 路由与容量 | 多代理池及每出口并发；用户与分组并发；严格分层故障转移；分组账号兜底；API Key 同平台兜底 | strict-failover.md；group-unavailable-fallback.md |
| 实际路由计费 | 兜底前重新校验目标组余额、订阅、额度和 RPM；原子换组并发；目标价格在途预留；异步计费快照 | backend/internal/service/api_key_fallback_admission.go；backend/internal/repository/concurrency_group_transfer.go |
| 网关兼容与错误处理 | 歧义模型字段拒绝；上游余额与额度错误识别、脱敏；可配置错误重试；HTTP/SSE/WS、媒体及异步请求重放边界 | upstream-error-message.md；backend/internal/service/upstream_error_retry.go |
| API Key 安全及面板入口 | 哈希存储；创建与轮换时显示原密钥；敏感操作验证；JWT 所属密钥授权的面板图片入口；主组及兜底组快捷选择 | backend/internal/service/api_key_secret.go；backend/internal/server/middleware/panel_api_key_auth.go |
| 价格与渠道管理 | 模型独立价目同步；默认价拆分；手工价格优先；模型价格与别名适配；账号近期请求及分组页签 | frontend/src/views/admin/ChannelsView.vue；backend/internal/service/billing_service.go |
| QQ 聊天与群治理 | 验签去重、持久化队列、成员上下文、站点知识与状态查询、自主接话、看图、生图、改图、意图复核、防刷屏与群策略；排队与生成共享截止时间 | backend/internal/qqbot/；qq-bot.md |
| QQ 账户及运营 | 账户绑定、邮箱确认转账、签到、邀请、抽奖和保底、一次性与周期成长、排行榜、奖励与价格健康通知、成员目录 | backend/internal/qqbot/growth.go；backend/internal/qqbot/lottery.go；qq-bot.md |
| QQ 群信息展示 | 管理端读取群信息、缓存群名及群选择器名称回填 | backend/internal/qqbot/group_info.go；frontend/src/composables/useQQGroupNames.ts |
| GPT 直充 | CTAI API 适配、验码、兑换、订阅与进度；请求幂等、任务凭据加密、奖励结算及后台补结算 | GPT_DIRECT_CN.md；backend/internal/service/gpt_direct_rewards.go |
| 固定 IPv6 出口 | 保留旧绑定与恢复；独立 OVH 分配、预留、账号独占、单审计 Key 独占、退役重试和存储统计；排除轮转池与 BPS 池 | ovh-ipv6-compatibility.md；deploy/ovh-ipv6-egress/ |
| VM Bridge 退休保护 | VM 传输及入口已移除；保留旧绑定审计与停用隔离，需恢复原路由后手动启用 | openai-vm.md |
| 更新与运维 | Fork 版本及来源、不可变镜像 manifest、宿主更新器、镜像及健康核验、持久化完成状态、重启恢复、多实例部署 | AUTO_UPDATE.md；backend/internal/sourceupdater/ |
| 访问及审核策略 | 指定文档路径的大陆访问限制；可信用户 ID 的内容审核白名单及执行前复核 | backend/internal/server/middleware/mainland_web_block.go；backend/internal/service/content_moderation.go |

## 维护约定

仅登记当前有效能力；调整入口、计费或执行语义时同步修改清单与使用说明。数据库历史迁移保持原文件与校验和，升级变更使用新增迁移。功能验证分别记录单元测试、数据库集成测试、发布和安装状态。
