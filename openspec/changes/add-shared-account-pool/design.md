# 设计

## 核心实体

- `shared_account_listings`: owner、底层 account、平台、展示名称、状态、代理覆盖、并发限制/倍率、售价倍率。
- `shared_account_usage_ledger`: request_id/usage_log_id 唯一幂等，消费者、提供者、消费金额、抽成快照、提供者收益、冻结时间和冲正状态。
- `shared_account_wallets` 与 `shared_account_wallet_ledger`: pending/available/frozen 收益及转入平台余额流水。
- `shared_account_call_stats`: listing 最近调用记录、累计调用次数、最近调用时间；只保存脱敏的模型、状态、耗时和金额。

## 状态

上传后直接 `active`，没有人工审核步骤；后台健康检查发现异常时转为 `invalid`，用户可 `paused`/`deleted`，管理员可事后 `suspended`。

## 结算

以网关最终扣除的 `actual_cost` 为结算基数。`platform_fee = actual_cost * fee_rate_snapshot`，`owner_pending = actual_cost - platform_fee`。结算写入扣费事务或可靠 outbox，按 request_id 幂等；退款只追加 reverse 流水。冻结期结束后转 available，用户转余额时使用行锁和 users.balance 原子更新。

## 用户体验

共享池页面使用响应式卡片网格，每张卡显示平台、脱敏账号名、状态、有效并发、售价倍率、累计调用次数、最近调用时间和最近若干请求；凭证、代理地址和消费者身份不展示。列表接口同时返回 `recent_calls` 和 `call_count`，避免前端二次请求。

## 隔离与开关

账号使用 account_scope；共享分组使用不可变的 is_shared_pool 标记。分组名称只是展示名称。普通全局、平台、未分组和模型可用性查询排除共享账号。

关闭共享池阻断新上传、重新上线和共享网关调用；个人账号管理和已有收益转余额保留。共享分组当前仅支持余额消费，订阅消费没有定义收益分配规则，因此入口拒绝。

## 免审核的发布时序

底层账号先以不可调度状态创建，绑定分组后，在同一事务里写入 listing、开放调度并写入调度缓存事件。整个过程自动完成，不需要管理员操作。暂停/删除也写入调度缓存事件。已经完成的调用，即使其 listing 随后暂停或删除，仍按照原归属结算。

## 计费倍率与提取

有效并发取 floor(用户并发 × 用户并发倍率)，最小为 1。售价倍率叠加在消费者适用的分组/用户价格上，分账基数始终取消费者最终实际扣款额。例：实际扣款 100，平台比例 10%，平台记 10，提供者记 90。收益默认等待 48 小时，成熟后用户手动转余额。此等待期与管理员审核无关。

详细实现范围与尚未完成项目以 verification.md 为准；本文描述的健康检测、管理员停用及冲正是设计目标，不代表已实现。
