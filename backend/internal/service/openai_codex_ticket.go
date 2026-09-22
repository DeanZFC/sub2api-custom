package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	openAICodexTicketExtraKeyPrefix  = "codex_turn_ticket:"
	openAICodexAstraMinVersion       = "0.153.4"
	openAICodexTicketStatePrefix     = "gAAAAA"
	openAICodexTicketDefaultModel    = "gpt-6-astra"
	openAICodexTicketDefaultSolModel = "gpt-5.6-sol"
)

// ErrOpenAICodexTicketUnavailable 表示该号该模型没有可用的套餐对应门票，
// 且 fail_closed 禁止裸打业务请求。
var ErrOpenAICodexTicketUnavailable = errors.New("codex turn-state ticket unavailable")

type openAICodexTicket struct {
	AccountID  int64     `json:"account_id"`
	Model      string    `json:"model"`
	State      string    `json:"state"`
	Length     int       `json:"length"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Attempts   int       `json:"attempts"`
}

func openAICodexTicketKey(accountID int64, model string) string {
	return fmt.Sprintf("%d\x00%s", accountID, strings.TrimSpace(model))
}

func openAICodexTicketExtraKey(model string) string {
	return openAICodexTicketExtraKeyPrefix + strings.TrimSpace(model)
}

func normalizeOpenAICodexTicketModel(model string) string {
	return strings.TrimSpace(model)
}

func extractOpenAICodexTicketModel(body []byte) string {
	return normalizeOpenAICodexTicketModel(gjson.GetBytes(body, "model").String())
}

func (s *OpenAIGatewayService) openAICodexTicketConfig() config.OpenAICodexTicketConfig {
	cfg := config.OpenAICodexTicketConfig{}
	if s != nil && s.cfg != nil {
		cfg = s.cfg.Gateway.OpenAICodexTicket
	}
	if cfg.TargetLength <= 0 {
		cfg.TargetLength = 292
	}
	cfg = config.NormalizeOpenAICodexTicketTiming(cfg)
	if cfg.HarvestProbeIntervalSeconds <= 0 {
		cfg.HarvestProbeIntervalSeconds = 6
	}
	if cfg.HarvestAttemptTimeoutSeconds <= 0 {
		cfg.HarvestAttemptTimeoutSeconds = 25
	}
	if cfg.HarvestMaxConcurrent <= 0 {
		cfg.HarvestMaxConcurrent = 8
	}
	if cfg.HarvestAccountConcurrency <= 0 {
		cfg.HarvestAccountConcurrency = 1
	}
	if cfg.HarvestProxyConcurrency <= 0 {
		cfg.HarvestProxyConcurrency = 2
	}
	cfg.HarvestMaxConcurrent = min(cfg.HarvestMaxConcurrent, 128)
	cfg.HarvestAccountConcurrency = min(cfg.HarvestAccountConcurrency, cfg.HarvestMaxConcurrent)
	cfg.HarvestProxyConcurrency = min(cfg.HarvestProxyConcurrency, cfg.HarvestMaxConcurrent)
	if len(cfg.Models) == 0 {
		cfg.Models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	return cfg
}

func (s *OpenAIGatewayService) openAICodexTicketGatedModel(model string) bool {
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" {
		return false
	}
	for _, item := range s.openAICodexTicketConfig().Models {
		if normalizeOpenAICodexTicketModel(item) == model {
			return true
		}
	}
	return false
}

// OpenAICodexTicketStatus 是给管理端看的门票摘要，不含 state blob。
type OpenAICodexTicketStatus struct {
	OpenAICodexTicketDiagnostics
	Model                  string     `json:"model"`
	Length                 int        `json:"length,omitempty"`
	TargetLength           int        `json:"target_length"`
	Ready                  bool       `json:"ready"`
	RemainingSeconds       int64      `json:"remaining_seconds"`
	Blocked                bool       `json:"blocked"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	CookieReady            bool       `json:"cookie_ready"`
	CookieRemainingSeconds int64      `json:"cookie_remaining_seconds"`
}

func OpenAICodexTicketStatuses(account *Account, cfg config.OpenAICodexTicketConfig, now time.Time) []OpenAICodexTicketStatus {
	policy := ResolveOpenAICodexTicketAccountConfig(account, cfg)
	if !policy.Enabled {
		return nil
	}
	models, targetLen := cfg.Models, policy.TargetLength
	if len(models) == 0 {
		models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	if targetLen <= 0 {
		targetLen = 292
	}
	out := make([]OpenAICodexTicketStatus, 0, len(models))
	for _, model := range models {
		model = normalizeOpenAICodexTicketModel(model)
		if model == "" {
			continue
		}
		status := OpenAICodexTicketStatus{Model: model, TargetLength: targetLen}
		status.PlanKnown = codexTicketPlanKnown(account)
		ticket := parseOpenAICodexTicketFromAny(0, model, nil)
		if account != nil && account.Extra != nil {
			ticket = parseOpenAICodexTicketFromAny(account.ID, model, account.Extra[openAICodexTicketExtraKey(model)])
		}
		ticket = limitOpenAICodexTicketLifetime(ticket, cfg)
		cookies := parseOpenAICodexTicketCookies(account)
		if cookies != nil {
			ttl := time.Duration(config.NormalizeOpenAICodexTicketTiming(cfg).TTLSeconds) * time.Second
			cookies.CFLB.ExpiresAt = minCodexTicketTime(cookies.CFLB.ExpiresAt, cookies.CFLB.CapturedAt.Add(ttl))
			cookies.OAILB.ExpiresAt = minCodexTicketTime(cookies.OAILB.ExpiresAt, cookies.OAILB.CapturedAt.Add(ttl))
		}
		status.setCookieStatus(cookies, now)
		if until := openAICodexTicketReadyUntil(ticket, cookies, now, targetLen); !until.IsZero() {
			status.Ready = true
			status.Length = ticket.Length
			remaining := int64(until.Sub(now) / time.Second)
			if remaining < 0 {
				remaining = 0
			}
			status.RemainingSeconds = remaining
			exp := until
			status.ExpiresAt = &exp
		}
		status.Blocked = policy.FailClosed && !status.Ready
		out = append(out, status)
	}
	return out
}

func (s *OpenAIGatewayService) openAICodexTicketEnabled() bool {
	return s.openAICodexTicketEnabledContext(context.Background())
}

func (s *OpenAIGatewayService) openAICodexTicketEnabledContext(ctx context.Context) bool {
	if s == nil {
		return false
	}
	fallback := s.cfg != nil && s.cfg.Gateway.OpenAICodexTicket.Enabled
	if s.settingService != nil {
		return s.settingService.GetOpenAICodexTicketEnabled(ctx, fallback)
	}
	return fallback
}

func (t *openAICodexTicket) valid(now time.Time, targetLen int) bool {
	if t == nil {
		return false
	}
	state := strings.TrimSpace(t.State)
	if len(state) != targetLen || t.Length != targetLen || !strings.HasPrefix(state, openAICodexTicketStatePrefix) {
		return false
	}
	if t.CapturedAt.IsZero() || t.CapturedAt.After(now) || !now.Before(t.expiresAt()) {
		return false
	}
	return true
}

// Old persisted one-hour tickets must obey the short lifetime immediately after
// an upgrade. A missing capture timestamp cannot establish ticket freshness.
func (t *openAICodexTicket) expiresAt() time.Time {
	if t == nil || t.CapturedAt.IsZero() || t.ExpiresAt.IsZero() {
		return time.Time{}
	}
	return minCodexTicketTime(t.ExpiresAt, t.CapturedAt.Add(time.Duration(config.DefaultOpenAICodexTicketTTLSeconds)*time.Second))
}

func minCodexTicketTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func limitOpenAICodexTicketLifetime(ticket *openAICodexTicket, cfg config.OpenAICodexTicketConfig) *openAICodexTicket {
	if ticket == nil {
		return nil
	}
	copy := *ticket
	ttl := time.Duration(config.NormalizeOpenAICodexTicketTiming(cfg).TTLSeconds) * time.Second
	copy.ExpiresAt = minCodexTicketTime(ticket.expiresAt(), ticket.CapturedAt.Add(ttl))
	return &copy
}

func openAICodexTicketReadyUntil(ticket *openAICodexTicket, cookies *openAICodexTicketCookies, now time.Time, target int) time.Time {
	if !ticket.valid(now, target) {
		return time.Time{}
	}
	until := ticket.expiresAt()
	if target == 292 {
		if cookies.header(now) == "" {
			return time.Time{}
		}
		until = minCodexTicketTime(until, cookies.expiresAt())
	}
	return until
}

func (status *OpenAICodexTicketStatus) setCookieStatus(cookies *openAICodexTicketCookies, now time.Time) {
	status.CookieReady = cookies.header(now) != ""
	status.CookieRemainingSeconds = 0
	if status.CookieReady {
		status.CookieRemainingSeconds = int64(cookies.expiresAt().Sub(now) / time.Second)
	}
}

func (t *openAICodexTicket) needsRefresh(now time.Time, refreshBefore time.Duration) bool {
	if t == nil || t.ExpiresAt.IsZero() {
		return true
	}
	return !t.expiresAt().After(now.Add(refreshBefore))
}

func (s *OpenAIGatewayService) lookupOpenAICodexTicket(account *Account, model string) *openAICodexTicket {
	if s == nil || account == nil || account.ID <= 0 {
		return nil
	}
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" {
		return nil
	}
	key := openAICodexTicketKey(account.ID, model)
	targetLen := openAICodexTicketTargetLength(account, s.openAICodexTicketConfig())
	now := time.Now()
	var mem *openAICodexTicket
	if raw, ok := s.openaiCodexTickets.Load(key); ok {
		mem, _ = raw.(*openAICodexTicket)
	}
	var extra *openAICodexTicket
	if account.Extra != nil {
		extra = parseOpenAICodexTicketFromAny(account.ID, model, account.Extra[openAICodexTicketExtraKey(model)])
	}
	mem = limitOpenAICodexTicketLifetime(mem, s.openAICodexTicketConfig())
	extra = limitOpenAICodexTicketLifetime(extra, s.openAICodexTicketConfig())
	if extra.valid(now, targetLen) && (mem == nil || extra.CapturedAt.After(mem.CapturedAt)) {
		s.openaiCodexTickets.Store(key, extra)
		return extra
	}
	if mem.valid(now, targetLen) {
		return mem
	}
	if extra != nil {
		s.openaiCodexTickets.Store(key, extra)
		return extra
	}
	if mem != nil {
		s.openaiCodexTickets.Delete(key)
	}
	return nil
}

func parseOpenAICodexTicketFromAny(accountID int64, model string, raw any) *openAICodexTicket {
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var ticket openAICodexTicket
	if err := json.Unmarshal(b, &ticket); err != nil {
		return nil
	}
	ticket.AccountID = accountID
	if strings.TrimSpace(model) != "" {
		ticket.Model = model
	}
	ticket.State = strings.TrimSpace(ticket.State)
	if ticket.Length == 0 {
		ticket.Length = len(ticket.State)
	}
	if ticket.State == "" {
		return nil
	}
	return &ticket
}

func (s *OpenAIGatewayService) storeOpenAICodexTicket(ctx context.Context, account *Account, ticket *openAICodexTicket) {
	if s == nil || account == nil || ticket == nil || account.ID <= 0 {
		return
	}
	model := normalizeOpenAICodexTicketModel(ticket.Model)
	ticket.Model = model
	ticket.AccountID = account.ID
	s.openaiCodexTickets.Store(openAICodexTicketKey(account.ID, model), ticket)
	if s.accountRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		openAICodexTicketExtraKey(model): ticket,
	}); err != nil {
		logger.L().Warn("openai_codex_ticket persist failed",
			zap.Int64("account_id", account.ID),
			zap.String("model", model),
			zap.Error(err),
		)
	}
}

// applyOpenAICodexTicket 在出站请求上覆盖 x-codex-turn-state。
// 请求路径只注入已捕获的有效门票，不现场打票；无票则返回
// ErrOpenAICodexTicketUnavailable。打票由后台 harvester 完成。
func (s *OpenAIGatewayService) applyOpenAICodexTicket(ctx context.Context, account *Account, model string, h http.Header) error {
	_, err := s.applyOpenAICodexTicketWithResult(ctx, account, model, h)
	return err
}

// The result identifies what was actually injected from one snapshot, so WS
// reuse cannot confuse a concurrently renewed ticket with the sent headers.
func (s *OpenAIGatewayService) applyOpenAICodexTicketWithResult(ctx context.Context, account *Account, model string, h http.Header) (bool, error) {
	if s == nil || h == nil || !isOpenAICodexTicketAccount(account) {
		return false, nil
	}
	policy := s.openAICodexTicketAccountConfig(ctx, account)
	if !policy.Enabled {
		return false, nil
	}
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" || !s.openAICodexTicketGatedModel(model) {
		return false, nil
	}
	ticket := s.lookupOpenAICodexTicket(account, model)
	now := time.Now()
	cookies := s.lookupOpenAICodexTicketCookies(account)
	if !openAICodexTicketReadyUntil(ticket, cookies, now, policy.TargetLength).IsZero() {
		h.Set(openAICodexTurnStateHeader, ticket.State)
		if cookie := cookies.header(now); cookie != "" {
			h.Set("Cookie", cookie)
		}
		return true, nil
	}
	s.recordOpenAICodexTicketInjectMiss(account.ID, model)
	if !policy.FailClosed {
		return false, nil
	}
	return false, ErrOpenAICodexTicketUnavailable
}

// openAICodexTicketOutboundModel 预测本请求真正出站的模型名，也就是
// applyOpenAICodexTicket 注入时读到的 body.model。
//
// 调度门控与注入必须按同一个模型名判定门票。普通请求下二者同源：Forward 的
// upstreamModel 与本函数都走 resolveOpenAIAccountUpstreamModelForRequest，且
// Forward 会把 body.model 改写成该值后才注入。但 /responses/compact 例外——
// Forward 会把出站模型进一步改写为 compact 映射或 gateway.openai_compact_model
// （默认非空），此时若门控仍按客户端原始模型判定，就会把「实际出站是非门控
// 模型、根本不需要票」的 compact 请求整片误拦成不可调度。
func (s *OpenAIGatewayService) openAICodexTicketOutboundModel(account *Account, requestedModel string, requireCompact bool) string {
	model := strings.TrimSpace(requestedModel)
	if account == nil || model == "" {
		return model
	}
	if !account.IsOpenAI() {
		return canonicalOpenAIAccountSchedulingModel(account, model)
	}
	_, upstreamModel := resolveOpenAIForwardMappedModels(account, model, requireCompact)
	if requireCompact {
		// 与 Forward 同序：compact 兜底模型优先于普通/compact 映射结果。
		if compactModel := strings.TrimSpace(s.resolveOpenAICompactFallbackModel(account, model)); compactModel != "" {
			upstreamModel = compactModel
		}
	}
	if upstreamModel = strings.TrimSpace(upstreamModel); upstreamModel != "" {
		return upstreamModel
	}
	return model
}

// outboundModel 必须是真正会发给上游的模型名（openAICodexTicketOutboundModel），
// 不是客户端原始模型：注入侧读的是出站 body.model，两侧口径必须一致。
func (s *OpenAIGatewayService) openAICodexTicketBlocksAccount(account *Account, outboundModel string) bool {
	if s == nil || !isOpenAICodexTicketAccount(account) {
		return false
	}
	policy := s.openAICodexTicketAccountConfig(context.Background(), account)
	if !policy.Enabled || !policy.FailClosed {
		return false
	}
	model := normalizeOpenAICodexTicketModel(outboundModel)
	if !s.openAICodexTicketGatedModel(model) {
		return false
	}
	ticket := s.lookupOpenAICodexTicket(account, model)
	return openAICodexTicketReadyUntil(ticket, s.lookupOpenAICodexTicketCookies(account), time.Now(), policy.TargetLength).IsZero()
}

func (s *OpenAIGatewayService) fireOpenAICodexTicketProbe(ctx context.Context, account *Account, token, model, proxyURL string, attemptTimeout time.Duration) (state string, status int, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	body := []byte(`{"model":` + jsonString(model) + `,"store":false,"stream":true,"instructions":"Reply with exactly: pong","input":[{"role":"user","content":[{"type":"input_text","text":"ping"}]}]}`)
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, newOpenAICodexTicketProbeError("invalid_response", 0)
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAIHarvest))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("session_id", uuid.NewString())
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(attemptCtx, s.accountRepo, req.Header, account); err != nil {
		return "", 0, newOpenAICodexTicketProbeError("auth", 0)
	}
	applyOpenAICodexTicketHarvestIdentity(req.Header, model)

	// Synthetic probes must use the dedicated no-reuse transport even when the
	// production account is bound to a plugin. This also avoids reading pluginManager
	// while handlers are still wiring it during gateway construction.
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return "", 0, classifyOpenAICodexTicketProbeError(err, 0)
	}
	if resp == nil {
		return "", 0, newOpenAICodexTicketProbeError("invalid_response", 0)
	}
	// Successful probes only need headers. Error bodies are bounded diagnostics;
	// neither path reuses a connection or drains a successful generation stream.
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, openAICodexTicketProbeResponseError(resp)
	}
	if attemptCtx.Err() == nil {
		s.captureOpenAICodexTicketCookies(attemptCtx, account, resp.Header, time.Now())
	}
	return extractOpenAICodexTurnState(resp.Header), resp.StatusCode, nil
}

func jsonString(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

func applyOpenAICodexTicketHarvestIdentity(h http.Header, model string) {
	ensureCodexIdentityHeaders(h)
	enforceCodexIdentityHeaders(h)
	version := strings.TrimSpace(h.Get("version"))
	if needsOpenAICodexAstraVersion(model) && (version == "" || CompareVersions(version, openAICodexAstraMinVersion) < 0) {
		h.Set("version", openAICodexAstraMinVersion)
		h.Set("user-agent", buildCodexCLIUserAgent(openAICodexAstraMinVersion))
		h.Set("originator", openai.CodexDefaultOriginator)
	}
}

func needsOpenAICodexAstraVersion(model string) bool {
	m := strings.ToLower(normalizeOpenAICodexTicketModel(model))
	return strings.Contains(m, "gpt-6") || strings.Contains(m, "astra")
}

func (s *OpenAIGatewayService) StartOpenAICodexTicketHarvester() {
	if s == nil {
		return
	}
	s.openaiCodexTicketLifecycleMu.Lock()
	defer s.openaiCodexTicketLifecycleMu.Unlock()
	if s.openaiCodexTicketStopped || s.openaiCodexTicketDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.openaiCodexTicketCancel = cancel
	s.openaiCodexTicketDone = done
	go func() {
		defer close(done)
		s.openAICodexTicketHarvestLoop(ctx)
	}()
	logger.L().Info("openai_codex_ticket harvester started",
		zap.Int("ttl_seconds", s.openAICodexTicketConfig().TTLSeconds),
		zap.Int("default_target_length", s.openAICodexTicketConfig().TargetLength),
		zap.Int("pro_target_length", 292), zap.Int("team_target_length", 332),
		zap.Strings("models", s.openAICodexTicketConfig().Models),
	)
}

func (s *OpenAIGatewayService) StopOpenAICodexTicketHarvester() {
	if s == nil {
		return
	}
	s.openaiCodexTicketLifecycleMu.Lock()
	s.openaiCodexTicketStopped = true
	cancel, done := s.openaiCodexTicketCancel, s.openaiCodexTicketDone
	s.openaiCodexTicketLifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Account snapshots refresh every six seconds; dispatching never waits for probes.
// Per-job completion deadlines govern retries, checked on a one-second dispatch tick.
func (s *OpenAIGatewayService) openAICodexTicketHarvestLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	defer func() { s.cancelOpenAICodexTicketJobs(); s.openaiCodexTicketScheduler.workers.Wait() }()
	var accounts []Account
	var nextSnapshot time.Time
	var snapshotAvailable bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if !s.openAICodexTicketEnabledContext(ctx) {
				s.cancelOpenAICodexTicketJobs()
				accounts = nil
				snapshotAvailable = false
				nextSnapshot = time.Time{}
			} else {
				if s.accountRepo != nil && !time.Now().Before(nextSnapshot) {
					listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					fresh, err := s.accountRepo.ListByPlatform(listCtx, PlatformOpenAI)
					cancel()
					if err == nil {
						accounts = fresh
						snapshotAvailable = true
					} else {
						snapshotAvailable = false
						logger.L().Warn("openai_codex_ticket list accounts failed", zap.Error(err))
					}
					nextSnapshot = time.Now().Add(6 * time.Second)
				}
				if snapshotAvailable {
					s.dispatchOpenAICodexTickets(ctx, accounts)
				}
			}
			timer.Reset(time.Second)
		}
	}
}

// Refresh admits due work without waiting for another account's HTTP request.
func (s *OpenAIGatewayService) refreshOpenAICodexTickets(ctx context.Context) {
	if s == nil || s.accountRepo == nil || ctx.Err() != nil {
		return
	}
	if !s.openAICodexTicketEnabledContext(ctx) {
		s.cancelOpenAICodexTicketJobs()
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	accounts, err := s.accountRepo.ListByPlatform(listCtx, PlatformOpenAI)
	if err != nil {
		return
	}
	s.dispatchOpenAICodexTickets(ctx, accounts)
}

func (s *OpenAIGatewayService) probeOnceOpenAICodexTicket(ctx context.Context, account *Account, model string) {
	if s == nil || !isOpenAICodexTicketAccount(account) || ctx.Err() != nil {
		return
	}
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" {
		return
	}
	cfg := s.openAICodexTicketConfig()
	s.startOpenAICodexTicketProbe(ctx, account, model, cfg, time.Now(), false)
}

// IsOpenAICodexTicketExtraKey identifies server-managed ticket material.
func IsOpenAICodexTicketExtraKey(key string) bool {
	return strings.HasPrefix(key, openAICodexTicketExtraKeyPrefix) || key == openAICodexTicketCookiesExtraKey
}

// MergeOpenAICodexTicketExtra preserves only persisted tickets, never summaries or
// blobs supplied by an account edit. The repository repeats this under the row
// lock so a concurrent harvest cannot be overwritten by a stale admin snapshot.
func MergeOpenAICodexTicketExtra(extra, current map[string]any) map[string]any {
	result := maps.Clone(extra)
	for key := range result {
		if IsOpenAICodexTicketExtraKey(key) {
			delete(result, key)
		}
	}
	for key, value := range current {
		if IsOpenAICodexTicketExtraKey(key) {
			if result == nil {
				result = make(map[string]any)
			}
			result[key] = value
		}
	}
	return result
}

// ValidateOpenAICodexTicketHarvestProxyURL validates only syntax, without making
// a network request or including credentials in validation errors.
func ValidateOpenAICodexTicketHarvestProxyURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("harvest proxy must be an HTTP(S) or SOCKS5(h) URL with a host and no path, query or fragment")
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return errors.New("harvest proxy scheme must be http, https, socks5 or socks5h")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("harvest proxy port must be between 1 and 65535")
		}
	}
	return nil
}

// Credential shadows do not own tickets. Keep their existing forwarding policy
// instead of imposing a gate for a key the harvester never populates.
func isOpenAICodexTicketAccount(account *Account) bool {
	return account != nil && account.IsOpenAIOAuthLike() && !account.IsShadow()
}

// IsOpenAICodexTicketPrivateExtraKey also covers the retired account-level proxy
// override, whose credentials may remain in older account records.
func IsOpenAICodexTicketPrivateExtraKey(key string) bool {
	return IsOpenAICodexTicketExtraKey(key) || key == "codex_harvest_proxy_url" || key == OpenAICodexTicketHarvestProxyIDsExtraKey
}

// RedactOpenAICodexTicketExtra strips ephemeral ticket material from exports
// without changing the source account or unrelated backup fields.
func RedactOpenAICodexTicketExtra(extra map[string]any) map[string]any {
	redacted := maps.Clone(extra)
	for key := range redacted {
		if IsOpenAICodexTicketPrivateExtraKey(key) {
			delete(redacted, key)
		}
	}
	return redacted
}
