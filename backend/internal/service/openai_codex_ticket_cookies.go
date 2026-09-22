package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const openAICodexTicketCookiesExtraKey = "codex_turn_cookies"

// Only these two routing cookies may leave this store. Empty values are deletion
// tombstones: their timestamps prevent older account snapshots resurrecting them.
type openAICodexTicketCookie struct {
	Value      string    `json:"value,omitempty"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type openAICodexTicketCookies struct {
	AccountID int64                   `json:"account_id"`
	Scope     string                  `json:"scope"`
	UpdatedAt time.Time               `json:"updated_at"`
	CFLB      openAICodexTicketCookie `json:"__cflb"`
	OAILB     openAICodexTicketCookie `json:"__oailb"`
}

// Tokens rotate during OAuth refresh; account identity and the configured exit
// are the stable boundary for sharing routing cookies between model tickets.
func openAICodexTicketScope(account *Account) string {
	if account == nil || account.ID <= 0 {
		return ""
	}
	route := openAICodexTicketProxyRoute(account)
	if !route.valid {
		return ""
	}
	data, _ := json.Marshal(struct {
		AccountID int64
		Identity  string
		UserID    string
		Type      string
		ProxyID   *int64
		Proxy     string
		Source    string
	}{account.ID, account.GetChatGPTAccountID(), account.GetChatGPTUserID(), account.Type, account.ProxyID, route.proxy, route.source})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func validOpenAICodexTicketCookieValue(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		// RFC 6265 cookie-octet; reject values instead of silently sanitizing.
		if b < 0x21 || b > 0x7e || b == '"' || b == ',' || b == ';' || b == '\\' {
			return false
		}
	}
	return true
}

func (cookie openAICodexTicketCookie) expiresAt() time.Time {
	if !validOpenAICodexTicketCookieValue(cookie.Value) || cookie.CapturedAt.IsZero() || !cookie.ExpiresAt.After(cookie.CapturedAt) {
		return time.Time{}
	}
	return minOpenAICodexCookieTime(cookie.ExpiresAt, cookie.CapturedAt.Add(time.Duration(config.DefaultOpenAICodexTicketTTLSeconds)*time.Second))
}

func minOpenAICodexCookieTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (cookies *openAICodexTicketCookies) expiresAt() time.Time {
	if cookies == nil {
		return time.Time{}
	}
	a, b := cookies.CFLB.expiresAt(), cookies.OAILB.expiresAt()
	if a.IsZero() || b.IsZero() {
		return time.Time{}
	}
	return minOpenAICodexCookieTime(a, b)
}

func (cookies *openAICodexTicketCookies) header(now time.Time) string {
	if cookies == nil || !now.Before(cookies.expiresAt()) || now.Before(cookies.CFLB.CapturedAt) || now.Before(cookies.OAILB.CapturedAt) {
		return ""
	}
	return "__cflb=" + cookies.CFLB.Value + "; __oailb=" + cookies.OAILB.Value
}

func parseOpenAICodexTicketCookies(account *Account) *openAICodexTicketCookies {
	if account == nil || account.Extra == nil {
		return nil
	}
	raw, err := json.Marshal(account.Extra[openAICodexTicketCookiesExtraKey])
	if err != nil {
		return nil
	}
	var cookies openAICodexTicketCookies
	if json.Unmarshal(raw, &cookies) != nil || cookies.AccountID != account.ID || cookies.Scope == "" || cookies.Scope != openAICodexTicketScope(account) || cookies.UpdatedAt.IsZero() {
		return nil
	}
	return &cookies
}

func (s *OpenAIGatewayService) lookupOpenAICodexTicketCookies(account *Account) *openAICodexTicketCookies {
	if s == nil || account == nil || account.ID <= 0 {
		return nil
	}
	scope := openAICodexTicketScope(account)
	if scope == "" {
		return nil
	}
	extra := parseOpenAICodexTicketCookies(account)
	var selected *openAICodexTicketCookies
	for {
		raw, exists := s.openaiCodexTicketCookies.Load(account.ID)
		mem, _ := raw.(*openAICodexTicketCookies)
		if mem != nil && mem.Scope == scope && (extra == nil || !extra.UpdatedAt.After(mem.UpdatedAt)) {
			selected = mem
			break
		}
		if extra == nil {
			return nil
		}
		// An old account snapshot must not evict a freshly captured new scope.
		if mem != nil && mem.Scope != scope {
			selected = extra
			break
		}
		if exists {
			if !s.openaiCodexTicketCookies.CompareAndSwap(account.ID, raw, extra) {
				continue
			}
		} else if _, loaded := s.openaiCodexTicketCookies.LoadOrStore(account.ID, extra); loaded {
			continue
		}
		selected = extra
		break
	}
	// Respect a reduced current TTL even for material restored from persistence.
	result := *selected
	ttl := time.Duration(s.openAICodexTicketConfig().TTLSeconds) * time.Second
	result.CFLB.ExpiresAt = minOpenAICodexCookieTime(result.CFLB.ExpiresAt, result.CFLB.CapturedAt.Add(ttl))
	result.OAILB.ExpiresAt = minOpenAICodexCookieTime(result.OAILB.ExpiresAt, result.OAILB.CapturedAt.Add(ttl))
	return &result
}

func openAICodexTicketCookieMatches(cookie *http.Cookie) bool {
	if cookie == nil || (cookie.Name != "__cflb" && cookie.Name != "__oailb") {
		return false
	}
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	// chatgpt.com has no usable parent domain; accepting .com would defeat scope.
	if domain != "" && domain != "chatgpt.com" {
		return false
	}
	path := cookie.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/backend-api/codex"
	}
	const requestPath = "/backend-api/codex/responses"
	if path != requestPath && !(strings.HasPrefix(requestPath, path) && (strings.HasSuffix(path, "/") || requestPath[len(path)] == '/')) {
		return false
	}
	// Invalid Domain/Path attributes are otherwise ignored by net/http. Do not
	// turn a malformed scope into a host-only/default-path cookie by accident.
	for _, attr := range cookie.Unparsed {
		name, _, _ := strings.Cut(attr, "=")
		if strings.EqualFold(strings.TrimSpace(name), "domain") || strings.EqualFold(strings.TrimSpace(name), "path") {
			return false
		}
	}
	return true
}

func (s *OpenAIGatewayService) captureOpenAICodexTicketCookies(ctx context.Context, account *Account, headers http.Header, now time.Time) {
	if s == nil || account == nil || account.ID <= 0 || now.IsZero() {
		return
	}
	scope := openAICodexTicketScope(account)
	if scope == "" {
		return
	}
	updates := make(map[string]openAICodexTicketCookie, 2)
	ttl := s.openAICodexTicketConfig().TTLSeconds
	for _, raw := range headers.Values("Set-Cookie") {
		if strings.ContainsAny(raw, "\r\n\x00") {
			continue
		}
		cookie, err := http.ParseSetCookie(raw)
		if err != nil || !openAICodexTicketCookieMatches(cookie) {
			continue
		}
		entry := openAICodexTicketCookie{CapturedAt: now, ExpiresAt: now}
		if cookie.MaxAge >= 0 && (cookie.Expires.IsZero() || cookie.Expires.After(now)) && cookie.Value != "" {
			if !validOpenAICodexTicketCookieValue(cookie.Value) {
				continue
			}
			entry.Value = cookie.Value
			entry.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
			if cookie.MaxAge > 0 && cookie.MaxAge < ttl {
				entry.ExpiresAt = now.Add(time.Duration(cookie.MaxAge) * time.Second)
			}
			if !cookie.Expires.IsZero() {
				entry.ExpiresAt = minOpenAICodexCookieTime(entry.ExpiresAt, cookie.Expires)
			}
		}
		updates[cookie.Name] = entry
	}
	if len(updates) == 0 {
		return
	}
	// Publish immutable snapshots before persistence; request lookups never wait
	// for database I/O. Serializing writes prevents concurrent model probes from
	// persisting an older partial snapshot after a newer one.
	accountLock, _ := s.openaiCodexTicketCookiesMu.LoadOrStore(account.ID, &sync.Mutex{})
	mu := accountLock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	current := s.lookupOpenAICodexTicketCookies(account)
	next := openAICodexTicketCookies{AccountID: account.ID, Scope: scope}
	if current != nil {
		next = *current
	}
	changed := false
	for name, update := range updates {
		target := &next.CFLB
		if name == "__oailb" {
			target = &next.OAILB
		}
		if update.CapturedAt.Before(target.CapturedAt) || *target == update {
			continue
		}
		*target = update
		changed = true
	}
	if !changed {
		return
	}
	next.UpdatedAt = now
	if current != nil && current.UpdatedAt.After(now) {
		next.UpdatedAt = current.UpdatedAt
	}
	s.openaiCodexTicketCookies.Store(account.ID, &next)
	if s.accountRepo == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.accountRepo.UpdateExtra(persistCtx, account.ID, map[string]any{openAICodexTicketCookiesExtraKey: &next}); err != nil {
		// Repository errors can contain serialized data; never log cookie values.
		logger.L().Warn("openai_codex_ticket cookie persistence failed", zap.Int64("account_id", account.ID))
	}
}
