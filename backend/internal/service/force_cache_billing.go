package service

// applyForceCacheBilling 保留 OAuth / Setup Token 账号的粘性切换补偿。是否补偿以最终成功的
// 账号为准，避免切换前的标记泄漏到按量付费的 API Key、Bedrock 或 Vertex 账号。
// 按量上游的普通输入仍会产生费用，必须保留其实际输入和缓存分类。
func applyForceCacheBilling(usage *ClaudeUsage, account *Account, requested bool) bool {
	if !requested || usage == nil || account == nil || !account.IsOAuth() || usage.InputTokens <= 0 {
		return false
	}
	usage.CacheReadInputTokens += usage.InputTokens
	usage.InputTokens = 0
	return true
}
