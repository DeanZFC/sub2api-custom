package service

import (
	"context"
	"errors"
	"maps"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// The protection marker is deliberately stored in accounts.extra so enabling
// it is an atomic account update and existing accounts remain untouched until
// an administrator explicitly applies it.
const (
	AntiDegradeMarkerExtraKey = "anti_degrade"
	AntiDegradationExtraKey   = "anti_degradation"
	ProtectionScopeExtraKey   = "protection_scope"
	AntiDegradeConcurrencyCap = 16
)

type AntiDegradeMode string

const (
	AntiDegradeModeNative         AntiDegradeMode = "native_baseline"
	AntiDegradeModeGeneric        AntiDegradeMode = "generic"
	AntiDegradeModeMinimal        AntiDegradeMode = "minimal_compat"
	AntiDegradeModeLegacy         AntiDegradeMode = "legacy"
	AntiDegradeMode1              AntiDegradeMode = "mode1"
	AntiDegradeMode2              AntiDegradeMode = "mode2"
	AntiDegradeModeSession        AntiDegradeMode = "session_standard"
	AntiDegradeModeTLSNode24      AntiDegradeMode = "tls_node24"
	AntiDegradeModeLowConcurrency AntiDegradeMode = "low_concurrency"
	AntiDegradeModeSingleMachine  AntiDegradeMode = "single_machine_multi_window"
	DefaultAntiDegradeMode        AntiDegradeMode = AntiDegradeModeLegacy
)

var ErrUnknownAntiDegradeMode = infraerrors.BadRequest("UNKNOWN_PROTECTION_STRATEGY", "未知的账号保护策略")

func normalizeAntiDegradeMode(mode AntiDegradeMode) AntiDegradeMode {
	if mode == "" {
		return DefaultAntiDegradeMode
	}
	return mode
}

func defaultAntiDegradeModeForAccount(a *Account) AntiDegradeMode {
	if a != nil && (a.IsOpenAIOAuthLike() || a.IsAnthropicOAuthOrSetupToken()) &&
		!a.IsShadow() && len(a.ProxyIDs) <= 1 && a.Extra["proxy_mode"] != "random" {
		return DefaultAntiDegradeMode
	}
	return AntiDegradeModeGeneric
}

func antiDegradeEnabled(a *Account) bool {
	if a == nil || a.Extra == nil {
		return false
	}
	if enabled, ok := a.Extra[AntiDegradationExtraKey].(bool); ok {
		return enabled
	}
	if marker, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any); ok {
		v, _ := marker["enabled"].(bool)
		return v
	}
	return false
}

func antiDegradeMode(a *Account) AntiDegradeMode {
	if a != nil && a.Extra != nil {
		if m, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any); ok {
			if v, ok := m["mode"].(string); ok && v != "" {
				return AntiDegradeMode(v)
			}
		}
	}
	return DefaultAntiDegradeMode
}

func antiDegradePrevious(a *Account) map[string]any {
	if a == nil || a.Extra == nil {
		return nil
	}
	m, _ := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	if m == nil {
		return nil
	}
	p, _ := m["prev"].(map[string]any)
	return p
}

// AntiDegradationEnabled reports only an explicit persisted protection marker.
func (a *Account) AntiDegradationEnabled() bool { return antiDegradeEnabled(a) }

// Generic protection owns only the concurrency bound; identity and transport
// continue to follow the account's original settings.
func (a *Account) IdentityProtectionEnabled() bool {
	return a.AntiDegradationEnabled() && antiDegradeMode(a) != AntiDegradeModeGeneric
}

func (a *Account) ProtectionScope() string {
	if !a.AntiDegradationEnabled() {
		return "disabled"
	}
	if antiDegradeMode(a) == AntiDegradeModeGeneric {
		return "generic_v1"
	}
	if a.IsOpenAIOAuthLike() {
		if antiDegradeMode(a) == AntiDegradeMode1 {
			return "codex_v3"
		}
		return "codex"
	}
	return "generic"
}

func (a *Account) ProtectionMode() string {
	if !a.AntiDegradationEnabled() {
		return "disabled"
	}
	return string(antiDegradeMode(a))
}

func antiDegradeSettings(mode AntiDegradeMode) (identity codexFingerprintMode, tls string, maxConcurrency int) {
	p := antiDegradeStrategyProfile(normalizeAntiDegradeMode(mode))
	if p.ID == "" {
		return "", "", 0
	}
	identity = codexFingerprintMode(p.IdentityMode)
	if identity == "single_machine_multi_window" {
		identity = codexFingerprintSingleMachineMultiWindow
	}
	return identity, p.TLSProfile, p.MaxConcurrency
}

type AntiDegradeChange struct {
	Key  string `json:"key"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to"`
	Note string `json:"note,omitempty"`
}

type AntiDegradePreview struct {
	ActiveMode    string              `json:"active_mode"`
	AccountID     int64               `json:"account_id"`
	Enabled       bool                `json:"enabled"`
	Eligible      bool                `json:"eligible"`
	IdentityReady bool                `json:"identity_ready"`
	TLSProfile    string              `json:"tls_profile"`
	Reason        string              `json:"reason,omitempty"`
	Changes       []AntiDegradeChange `json:"changes"`
	PolicyVersion int                 `json:"policy_version"`
	Issues        []string            `json:"issues"`
}

// Identity strategies require a durable account identity and one fixed
// outbound route. Applying a strategy never silently changes proxy selection.
func antiDegradeEligibilityIssue(a *Account, mode AntiDegradeMode) string {
	if a == nil {
		return "account not found"
	}
	if mode == AntiDegradeModeGeneric {
		return ""
	}
	if a.IsShadow() {
		return "影子账号请在母账号配置身份保护策略"
	}
	if len(a.ProxyIDs) > 1 || a.Extra["proxy_mode"] == "random" {
		return "请先为账号选择一个固定代理或直连，再启用身份保护策略"
	}
	profile := antiDegradeStrategyProfile(mode)
	if profile.RequiresOpenAI && !a.IsOpenAIOAuthLike() {
		return "该策略仅支持 OpenAI OAuth / Setup Token 账号"
	}
	if mode == AntiDegradeModeLegacy && !a.IsOpenAIOAuthLike() && !a.IsAnthropicOAuthOrSetupToken() {
		return "初代兼容仅支持 OpenAI / Anthropic OAuth 或 Setup Token 账号"
	}
	return ""
}

type AntiDegradeStore interface {
	GetAccount(context.Context, int64) (*Account, error)
	UpdateAccount(context.Context, int64, *UpdateAccountInput) (*Account, error)
}

type AntiDegradeService struct{ admin AntiDegradeStore }

func NewAntiDegradeService(admin AntiDegradeStore) *AntiDegradeService {
	return &AntiDegradeService{admin: admin}
}

func (s *AntiDegradeService) Preview(ctx context.Context, id int64) (AntiDegradePreview, error) {
	return s.PreviewMode(ctx, id, "")
}

func (s *AntiDegradeService) PreviewMode(ctx context.Context, id int64, mode AntiDegradeMode) (AntiDegradePreview, error) {
	if mode != "" && antiDegradeStrategyProfile(mode).ID == "" {
		return AntiDegradePreview{}, ErrUnknownAntiDegradeMode
	}
	if s == nil || s.admin == nil {
		return AntiDegradePreview{}, errors.New("anti-degrade service unavailable")
	}
	a, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return AntiDegradePreview{}, err
	}
	p := AntiDegradePreview{AccountID: id, Enabled: antiDegradeEnabled(a), Changes: []AntiDegradeChange{}, Issues: []string{}}
	if a == nil {
		p.Reason = "account not found"
		return p, nil
	}
	if mode == "" {
		mode = defaultAntiDegradeModeForAccount(a)
	}
	p.ActiveMode = a.ProtectionMode()
	identity, tls, cap := antiDegradeSettings(mode)
	if seed, ok := codexFingerprintSeed(a.Extra); ok {
		p.IdentityReady = seed != ""
	}
	p.TLSProfile = tls
	if m, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any); ok {
		p.PolicyVersion, _ = extraInt(m["policy_version"])
	}
	if mode == AntiDegradeModeNative {
		p.Eligible = p.Enabled
		p.Reason = "原生基线需要还原当前保护配置"
		return p, nil
	}
	if reason := antiDegradeEligibilityIssue(a, mode); reason != "" {
		p.Reason = reason
		return p, nil
	}
	if antiDegradeEnabled(a) && antiDegradeMode(a) == mode {
		if err := validateRegisteredAntiDegrade(a); err != nil {
			p.Reason = "当前保护配置异常，请先还原后重新配置"
			p.Issues = append(p.Issues, err.Error())
			return p, nil
		}
		if mode == AntiDegradeMode1 && isMode1V2ProtectionEnabled(a) {
			p.TLSProfile = antiDegradeAccountTLSProfile(a)
			draft := *a
			draft.Extra = maps.Clone(a.Extra)
			if err := restoreAntiDegradeSnapshot(&draft); err != nil {
				p.Reason = "原始配置快照异常，无法安全升级"
				p.Issues = append(p.Issues, err.Error())
				return p, nil
			}
			p.Eligible = true
			p.Reason = "可显式升级到兼容架构 v3；保留原始还原快照和当前并发配置"
			p.Changes = append(p.Changes,
				AntiDegradeChange{Key: "policy_version", From: 2, To: mode1PolicyVersion},
				AntiDegradeChange{Key: "transport", From: p.TLSProfile, To: "standard"})
			return p, nil
		}
		p.Reason = "already enabled"
		return p, nil
	}
	if p.Enabled {
		p.Eligible = true
		p.Reason = "可切换到所选策略"
		p.Changes = append(p.Changes, AntiDegradeChange{Key: "strategy", From: string(antiDegradeMode(a)), To: string(mode)})
		return p, nil
	}
	p.Eligible = true
	p.Changes = append(p.Changes, AntiDegradeChange{Key: "strategy", To: string(mode)})
	if mode != AntiDegradeModeGeneric && a.IsOpenAIOAuthLike() {
		p.Changes = append(p.Changes, AntiDegradeChange{Key: "codex_fingerprint_mode", From: string(a.GetCodexFingerprintMode()), To: string(identity), Note: "账号级稳定 Codex 身份"})
	}
	if mode != AntiDegradeModeGeneric {
		p.Changes = append(p.Changes, AntiDegradeChange{Key: "transport", From: a.Extra["tls_fingerprint_builtin"], To: tls})
	}
	if a.Concurrency <= 0 {
		p.Changes = append(p.Changes, AntiDegradeChange{Key: "concurrency", From: a.Concurrency, To: cap, Note: "设置并发保护上限"})
	}
	p.Reason = "可应用账号保护"
	return p, nil
}

func (s *AntiDegradeService) Apply(ctx context.Context, id int64) (*Account, error) {
	return s.ApplyAntiDegradeMode(ctx, id, "")
}

func (s *AntiDegradeService) ApplyAntiDegrade(ctx context.Context, id int64) (*Account, error) {
	return s.Apply(ctx, id)
}

func (s *AntiDegradeService) ApplyAntiDegradeMode(ctx context.Context, id int64, mode AntiDegradeMode) (*Account, error) {
	if mode == AntiDegradeModeNative || (mode != "" && antiDegradeStrategyProfile(mode).ID == "") {
		return nil, ErrUnknownAntiDegradeMode
	}
	if s == nil || s.admin == nil {
		return nil, errors.New("anti-degrade service unavailable")
	}
	a, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, errors.New("account not found")
	}
	if mode == "" {
		mode = defaultAntiDegradeModeForAccount(a)
	}
	if reason := antiDegradeEligibilityIssue(a, mode); reason != "" {
		return nil, infraerrors.BadRequest("PROTECTION_NOT_ELIGIBLE", reason)
	}
	upgradeMode1 := mode == AntiDegradeMode1 && isMode1V2ProtectionEnabled(a)
	if antiDegradeEnabled(a) && antiDegradeMode(a) == mode {
		if err := validateRegisteredAntiDegrade(a); err != nil || !upgradeMode1 {
			return a, err
		}
	}
	// Plan the complete switch in memory and persist once. Every strategy keeps
	// the first unprotected snapshot, so A -> B -> off restores the baseline
	// rather than resurrecting strategy A.
	baseline := *a
	baseline.Extra = maps.Clone(a.Extra)
	if antiDegradeEnabled(a) {
		if err := restoreAntiDegradeSnapshot(&baseline); err != nil {
			return nil, err
		}
	}
	prev := snapshotAntiDegrade(&baseline)
	if upgradeMode1 {
		prev = maps.Clone(antiDegradePrevious(a))
	} else if mode == AntiDegradeModeGeneric {
		prev = map[string]any{"concurrency": baseline.Concurrency}
	}
	extra := maps.Clone(baseline.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	identity, tls, cap := antiDegradeSettings(mode)
	if mode != AntiDegradeModeGeneric && a.IsOpenAIOAuthLike() && identity != "" {
		extra[codexFingerprintModeExtraKey] = string(identity)
	}
	// Only legacy/node profile is explicitly persisted. The normal path's Mac
	// Codex profile is selected by resolveCodexMacTLSProfile for single-machine.
	if mode == AntiDegradeModeGeneric {
		// Preserve the original provider transport and any custom TLS template.
	} else if tls != "" && tls != "standard" && tls != "mac_codex" {
		extra["enable_tls_fingerprint"] = true
		extra["tls_fingerprint_builtin"] = tls
	} else if tls == "standard" {
		delete(extra, "tls_fingerprint_builtin")
		extra["enable_tls_fingerprint"] = false
	} else if tls == "mac_codex" {
		delete(extra, "tls_fingerprint_builtin")
		extra["enable_tls_fingerprint"] = false
	}
	if mode != AntiDegradeModeGeneric {
		delete(extra, "tls_fingerprint_profile_id")
	}
	concurrency := baseline.Concurrency
	if upgradeMode1 {
		concurrency = a.Mode1EffectiveConcurrency()
	}
	if concurrency <= 0 {
		concurrency = cap
	}
	marker := map[string]any{"enabled": true, "mode": string(mode), "max_concurrency": concurrency, "applied_concurrency": concurrency, "applied_at": time.Now().UTC().Format(time.RFC3339), "prev": prev}
	if upgradeMode1 {
		applied, ok := extraInt(mode1Marker(a)["applied_concurrency"])
		if !ok {
			applied, _ = extraInt(mode1Marker(a)["max_concurrency"])
		}
		if applied > 0 {
			marker["applied_concurrency"] = applied
		}
	}
	// policy_version identifies the mode-1 integrity contract only. Legacy and
	// diagnostic profiles must never be mistaken for a mode-1 policy.
	if mode == AntiDegradeMode1 {
		marker["policy_version"] = mode1PolicyVersion
	}
	extra[AntiDegradeMarkerExtraKey] = marker
	extra[AntiDegradationExtraKey] = true
	protected := *a
	protected.Extra = extra
	extra[ProtectionScopeExtraKey] = protected.ProtectionScope()
	// Identity/transport selection is independent from adaptive concurrency and
	// account health. Preserve their explicit administrator settings.
	ctx = withProtectionWriteExpectation(ctx, a)
	return s.admin.UpdateAccount(ctx, id, &UpdateAccountInput{Extra: extra, Concurrency: &concurrency, AllowProtectionManagedUpdates: true})
}

func snapshotAntiDegrade(a *Account) map[string]any {
	p := map[string]any{}
	if a == nil {
		return p
	}
	p["concurrency"] = a.Concurrency
	for _, key := range antiDegradeConfigurationKeys {
		if v, ok := a.Extra[key]; ok {
			p[key] = v
		} else {
			p[key] = nil
		}
	}
	return p
}

var antiDegradeConfigurationKeys = []string{codexFingerprintModeExtraKey, "enable_tls_fingerprint", "tls_fingerprint_builtin", "tls_fingerprint_profile_id", "proxy_mode"}

func restoreAntiDegradeSnapshot(a *Account) error {
	prev := antiDegradePrevious(a)
	if prev == nil {
		return infraerrors.BadRequest("PROTECTION_SNAPSHOT_MISSING", "原始配置快照缺失，无法安全还原")
	}
	originalConcurrency, ok := extraInt(prev["concurrency"])
	if !ok {
		return infraerrors.BadRequest("PROTECTION_SNAPSHOT_INVALID", "原始并发配置无效，无法安全还原")
	}
	marker, _ := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	appliedConcurrency, ok := extraInt(marker["applied_concurrency"])
	if !ok {
		// Compatibility for snapshots written before the applied ceiling was
		// separated from the administrator's editable runtime ceiling.
		appliedConcurrency, _ = extraInt(marker["max_concurrency"])
	}
	// Preserve an administrator's later manual concurrency edit.
	if a.Concurrency == appliedConcurrency {
		a.Concurrency = originalConcurrency
	}
	for _, key := range antiDegradeConfigurationKeys {
		if v, exists := prev[key]; exists {
			if v == nil {
				delete(a.Extra, key)
			} else {
				a.Extra[key] = v
			}
		}
	}
	delete(a.Extra, AntiDegradeMarkerExtraKey)
	a.Extra[AntiDegradationExtraKey] = false
	a.Extra[ProtectionScopeExtraKey] = "disabled"
	return nil
}

func (s *AntiDegradeService) Revert(ctx context.Context, id int64) (*Account, error) {
	return s.RevertAntiDegrade(ctx, id)
}

func (s *AntiDegradeService) RevertAntiDegrade(ctx context.Context, id int64) (*Account, error) {
	if s == nil || s.admin == nil {
		return nil, errors.New("anti-degrade service unavailable")
	}
	a, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil || !antiDegradeEnabled(a) {
		return a, nil
	}
	draft := *a
	draft.Extra = maps.Clone(a.Extra)
	if err := restoreAntiDegradeSnapshot(&draft); err != nil {
		return nil, err
	}
	ctx = withProtectionWriteExpectation(ctx, a)
	return s.admin.UpdateAccount(ctx, id, &UpdateAccountInput{Extra: draft.Extra, Concurrency: &draft.Concurrency, AllowProtectionManagedUpdates: true})
}

func (s *AntiDegradeService) SetProtection(ctx context.Context, id int64, enabled, confirmDisable bool) (*Account, error) {
	if !enabled && !confirmDisable {
		return nil, infraerrors.BadRequest("PROTECTION_CONFIRM_REQUIRED", "关闭防降智模式需要管理员明确确认")
	}
	if enabled {
		return s.Apply(ctx, id)
	}
	return s.Revert(ctx, id)
}

// ProtectionManagedKeys are preserved by ordinary account saves to prevent a
// stale modal response from silently disabling an applied policy.
func ProtectionManagedKeys(a *Account) []string {
	keys := []string{AntiDegradeMarkerExtraKey, AntiDegradationExtraKey, ProtectionScopeExtraKey}
	if !antiDegradeEnabled(a) {
		return keys
	}
	if antiDegradeMode(a) == AntiDegradeModeGeneric {
		return keys
	}
	return append(keys, antiDegradeConfigurationKeys...)
}

func PreserveAccountProtection(ctx context.Context, current *Account, incoming map[string]any) map[string]any {
	if current == nil {
		return incoming
	}
	if ProtectionManagedWrite(ctx) {
		return incoming
	}
	result := maps.Clone(incoming)
	if result == nil {
		result = map[string]any{}
	}
	for _, key := range ProtectionManagedKeys(current) {
		if v, ok := current.Extra[key]; ok {
			result[key] = v
		} else {
			delete(result, key)
		}
	}
	if seed, ok := codexFingerprintSeed(current.Extra); ok {
		result[codexFingerprintSeedExtraKey] = seed
	}
	return result
}

// Creation never implicitly enables protection. Imported full account objects
// cannot transplant another account's marker or restoration snapshot.
func PrepareNewAccountProtection(a *Account) {
	if a == nil || a.Extra == nil {
		return
	}
	a.Extra = maps.Clone(a.Extra)
	delete(a.Extra, AntiDegradeMarkerExtraKey)
	delete(a.Extra, AntiDegradationExtraKey)
	delete(a.Extra, ProtectionScopeExtraKey)
}
