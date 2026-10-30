# Codex 指纹收敛

指纹、兼容请求缓存键和直连 TLS 行为对齐官方 Sub2API d851a384。新建账号默认关闭指纹收敛。

| 模式 | 行为 |
| --- | --- |
| 关闭 | 使用官方默认身份处理 |
| 设备收敛 | 统一账号设备身份 |
| 会话收敛 | 统一设备和会话，按客户端会话派生线程 |
| 完全收敛 | 统一设备、会话和线程 |

入口：账号管理 → 新建或编辑 OpenAI OAuth 账号 → Codex 指纹收敛；批量编辑支持相同的四种官方模式。服务端管理稳定种子，不接受客户端覆盖。

自定义单机多窗口、Mac Codex TLS、TLS 会话缓存扩展、请求体 conversation_id/session_id 缓存键扩展和用户×分组×模型的 24 小时账号亲和均已移除。官方会话粘性、previous_response_id 归属、指纹模式与缓存键派生保留。

WebSocket 握手兼容键和直接拨号恢复官方指纹/TLS 行为；多代理池的代理隔离、VM 路由及其连接池清理继续保留。普通 TLS 指纹配置功能仍按官方实现运行。

## 升级

迁移 329_retire_custom_routing_identity_and_rate_limit.sql 将旧 single_machine_multi_window 模式设为 off，删除 upstream_billing_rate_limit，并为变化账号发布调度缓存刷新事件。官方四种模式、稳定种子、普通探测/倍率同步开关、代理池、并发、凭据和质量/手动调度状态不变。新导入或提交的旧配置也会在账号边界清理。

旧亲和 Redis 键不再读取，按原 24 小时 TTL 自然过期，无需清空 Redis。历史迁移保持原文件与校验和。
