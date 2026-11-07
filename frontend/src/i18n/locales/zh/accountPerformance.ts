export default {
 monitoringDisabled: '运维监控已关闭，指标暂停与恢复评估已停止。请先开启运维监控；仍可关闭此规则或手动解除。',
 title: '请求指标与自动暂停', configure: '配置请求指标与自动暂停',
 success_rate: '成功率', cache_rate: '缓存率', ttft: '平均首字', duration: '平均耗时',
 metric: '指标', current: '当前值', pauseMin: '低于时暂停', pauseMax: '高于时暂停', recoveryMin: '恢复下限', recoveryMax: '恢复上限',
 noBound: '不限制', inherit: '沿用暂停阈值', enabled: '启用指标自动暂停（每 30 秒评估）', active: '指标监测中',
 paused: '指标已暂停', trial: '恢复试运行中', trial_timeout: '试运行超时或样本不足',
 minSamples: '触发暂停的最小有效样本数（每项指标）', autoRecover: '启用自动恢复', cooldown: '暂停多久后试运行（分钟）', recoverySamples: '恢复所需新样本数（每项指标）',
 semantics: '常规统计范围为近 1 小时。成功率 = 已记录成功请求 ÷（成功 + 失败）；同账号、同 API Key 的重复请求去重，终态失败优先于部分输出。WS 连接错误不抹去成功轮次。仅计入已有日志，未记录的结果未知。缓存率按有效流式/WS 文本输入 Token 加权，耗时只统计成功请求。',
 boundsNote: '留空的指标不参与；任一指标严格越界且样本足够时暂停，等于阈值不触发。恢复上下限需在暂停阈值的正常范围内，留空则沿用对应暂停阈值。无样本显示 —，不会作为 0 处理。',
 recoveryNote: '冷却结束后每 5 秒最多放行 1 个请求，每轮最多放行恢复样本数的 3 倍。全部选中指标在本轮新样本上达标才恢复；不达标或 10 分钟内样本不足则重新暂停并等待下轮。无流量不会自动恢复。试运行及恢复后的评估从本轮开始计数，最多回看 1 小时。',
 saveNote: '保存会清除此规则自身的暂停并重新评估；手动禁用、限流及其他暂停原因仍然有效。自动恢复也仅解除本规则的暂停。新规则默认关闭。',
 samples: '{count} 个样本', save: '保存规则', release: '解除暂停并关闭规则', refresh: '重新加载',
 loading: '加载中…', unavailable: '指标配置暂不可用', failed: '更新失败；如发生版本冲突，请重新加载后保存。',
 invalid: '请至少配置一项启用指标。百分数为 0–100，耗时为非负毫秒；下限不能超过上限，恢复条件不能比暂停条件更宽松。样本数和暂停时长须为范围内整数。',
 audit: '最近规则事件', noEvents: '暂无事件', auditUnavailable: '事件记录暂不可用',
 configureEvent: '保存规则', pauseEvent: '指标暂停', releaseEvent: '解除并关闭规则', trialEvent: '开始恢复试运行', recoverEvent: '指标达标，自动恢复', retireEvent: '退役旧 HTTP 保护'
}
