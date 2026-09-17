package service

// AntiDegradeStrategyProfile is the server-owned description of one account
// protection strategy.  It describes transport and admission controls only;
// it never grants upstream capacity or changes billing.
type AntiDegradeStrategyProfile struct {
	ID             AntiDegradeMode `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Category       string          `json:"category"`
	IdentityMode   string          `json:"identity_mode"`
	TLSProfile     string          `json:"tls_profile"`
	MaxConcurrency int             `json:"max_concurrency"`
	Risk           string          `json:"risk"`
	ApplySupported bool            `json:"apply_supported"`
	RequiresOpenAI bool            `json:"requires_openai_oauth"`
	DiagnosticOnly bool            `json:"diagnostic_only"`
}

var antiDegradeStrategyProfiles = []AntiDegradeStrategyProfile{
	{ID: AntiDegradeModeNative, Name: "原生基线", Description: "不改写账号身份和传输策略。", Category: "常用", IdentityMode: "off", TLSProfile: "account", Risk: "低", DiagnosticOnly: true},
	{ID: AntiDegradeModeGeneric, Name: "通用并发保护", Description: "保留账号当前身份、代理和传输，只为无限并发设置保护上限。", Category: "常用", IdentityMode: "account", TLSProfile: "account", MaxConcurrency: 16, Risk: "低", ApplySupported: true},
	{ID: AntiDegradeModeMinimal, Name: "最小兼容", Description: "固定账号设备身份，保留独立会话和线程。", Category: "常用", IdentityMode: "device", TLSProfile: "standard", MaxConcurrency: 8, Risk: "低", ApplySupported: true, RequiresOpenAI: true},
	{ID: AntiDegradeModeLegacy, Name: "初代兼容", Description: "复用初代稳定会话身份与 Node.js 24 传输。", Category: "常用", IdentityMode: "session", TLSProfile: "nodejs24", MaxConcurrency: 16, Risk: "中", ApplySupported: true},
	{ID: AntiDegradeMode1, Name: "兼容架构 v3", Description: "稳定设备身份、独立会话、标准传输和并发保护。", Category: "常用", IdentityMode: "device", TLSProfile: "standard", MaxConcurrency: 16, Risk: "中", ApplySupported: true, RequiresOpenAI: true},
	{ID: AntiDegradeMode2, Name: "完整收敛", Description: "设备、会话和线程全部收敛，用于对照实验。", Category: "诊断", IdentityMode: "full", TLSProfile: "nodejs22", MaxConcurrency: 8, Risk: "高", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeSession, Name: "会话兼容", Description: "固定设备和账号会话，按客户端会话派生线程。", Category: "诊断", IdentityMode: "session", TLSProfile: "standard", MaxConcurrency: 8, Risk: "中", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeTLSNode24, Name: "Node.js 24 对照", Description: "保持设备身份，单独对照 Node.js 24 TLS。", Category: "诊断", IdentityMode: "device", TLSProfile: "nodejs24", MaxConcurrency: 8, Risk: "中", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeLowConcurrency, Name: "低并发稳定", Description: "会话兼容配合低并发，用于排查限流。", Category: "诊断", IdentityMode: "session", TLSProfile: "standard", MaxConcurrency: 4, Risk: "低", ApplySupported: true, RequiresOpenAI: true, DiagnosticOnly: true},
	{ID: AntiDegradeModeSingleMachine, Name: "单机多窗口", Description: "使用 Mac Codex 传输指纹和稳定的账号窗口身份。", Category: "常用", IdentityMode: "single_machine_multi_window", TLSProfile: "mac_codex", MaxConcurrency: 16, Risk: "中", ApplySupported: true, RequiresOpenAI: true},
}

func ListAntiDegradeStrategyProfiles() []AntiDegradeStrategyProfile {
	out := make([]AntiDegradeStrategyProfile, len(antiDegradeStrategyProfiles))
	copy(out, antiDegradeStrategyProfiles)
	return out
}

func antiDegradeStrategyProfile(mode AntiDegradeMode) AntiDegradeStrategyProfile {
	for _, p := range antiDegradeStrategyProfiles {
		if p.ID == mode {
			return p
		}
	}
	return AntiDegradeStrategyProfile{}
}
