package service

const (
	// SettingKeyOpenAICodexTicketEnabled Codex 292 打票总开关（后台可改、热更新）。
	// 关闭：不打票、不注入 x-codex-turn-state，按原链路转发。
	// 开启：后台打票并在业务请求中覆盖该头。
	SettingKeyOpenAICodexTicketEnabled = "openai_codex_ticket_enabled"
	// SettingKeyOpenAICodexTicketFailClosed controls whether a missing/expired
	// ticket makes an otherwise schedulable account ineligible. Missing defaults
	// to false so ticket harvesting remains an optional enhancement.
	SettingKeyOpenAICodexTicketFailClosed = "openai_codex_ticket_fail_closed"
	// SettingKeyOpenAICodexTicketHarvestProxyURL Codex 292 打票出口（socks5h/http），后台可改、热更新。
	SettingKeyOpenAICodexTicketHarvestProxyURL = "openai_codex_ticket_harvest_proxy_url"
	SettingKeyOpenAICodexTicketStaticProxyURL  = "openai_codex_ticket_static_proxy_url"
	// OpenAICodexTicketHarvestIPPoolURL 是打票出口的哨兵值：存入 harvest_proxy_url 表示
	// “使用 IP 管理里所有 active 代理组成的池，逐次轮换出口”，而非固定单一出口。
	// 它不是可拨号 URL；解析时会按次替换为池内的真实代理，故不落入静态出口记忆。
	OpenAICodexTicketHarvestIPPoolURL = "ippool://active"
	// SettingKeyOpenAICodexTicketModels Codex 292 打票模型列表，JSON 数组格式；缺失时回退配置文件。
	SettingKeyOpenAICodexTicketModels = "openai_codex_ticket_models"
)
