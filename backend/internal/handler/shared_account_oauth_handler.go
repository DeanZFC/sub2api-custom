package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type sharedOAuthSession struct {
	ownerID   int64
	platform  string
	expiresAt time.Time
}

type sharedOAuthRequest struct {
	ProxyURL     string `json:"proxy_url"`
	SessionID    string `json:"session_id"`
	Code         string `json:"code"`
	State        string `json:"state"`
	RedirectURI  string `json:"redirect_uri"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	AccessToken  string `json:"access_token"`
	SSOToken     string `json:"sso_token"`
	ProjectID    string `json:"project_id"`
	OAuthType    string `json:"oauth_type"`
	TierID       string `json:"tier_id"`
	Content      string `json:"content"`
	Name         string `json:"name"`
}

// Only token operations are exposed here. Account creation always goes through
// Upload, which fixes ownership, scope, group membership and listing status.
func (h *SharedAccountPoolHandler) Authorize(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.settings != nil && !h.settings.IsSharedPoolEnabled(c.Request.Context()) {
		response.NotFound(c, "Shared account pool is disabled")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var req sharedOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid authorization request")
		return
	}
	platform, action := c.Param("platform"), c.Param("action")
	if !sharedOAuthActionAllowed(platform, action) {
		response.NotFound(c, "Unsupported authorization operation")
		return
	}
	if action == "parse-session" {
		result, err := admin.ParseCodexCredentials(req.Content, req.Name)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, result)
		return
	}
	exchanging := action == "exchange-code" || action == "exchange-setup-token-code"
	if exchanging {
		if strings.TrimSpace(req.Code) == "" || !h.ownsOAuthSession(subject.UserID, platform, req.SessionID) {
			response.BadRequest(c, "Authorization session is invalid or expired; generate a new link")
			return
		}
	}
	ctx := c.Request.Context()
	proxy, err := h.uploader.CreateProxy(ctx, subject.UserID, req.ProxyURL)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	defer h.uploader.DeleteAuthProxy(ctx, proxy)
	var proxyID *int64
	proxyURL := ""
	if proxy != nil {
		proxyID = &proxy.ID
		proxyURL = proxy.URL()
	}
	var result any
	sessionID := ""
	switch action {
	case "generate-auth-url", "generate-setup-token-url":
		switch platform {
		case "anthropic":
			var r *service.GenerateAuthURLResult
			if action == "generate-setup-token-url" {
				r, err = h.oauth.GenerateSetupTokenURL(ctx, proxyID)
			} else {
				r, err = h.oauth.GenerateAuthURL(ctx, proxyID)
			}
			if err == nil {
				result, sessionID = r, r.SessionID
			}
		case "openai":
			r, e := h.openaiOAuth.GenerateAuthURL(ctx, proxyID, req.RedirectURI, service.PlatformOpenAI)
			err = e
			if err == nil {
				result, sessionID = r, r.SessionID
			}
		case "gemini":
			if req.OAuthType == "" {
				req.OAuthType = "code_assist"
			}
			if !validSharedGeminiOAuthType(req.OAuthType) {
				response.BadRequest(c, "Invalid oauth_type")
				return
			}
			r, e := h.geminiOAuth.GenerateAuthURL(ctx, proxyID, sharedGeminiRedirectURI(c), req.ProjectID, req.OAuthType, req.TierID)
			err = e
			if err == nil {
				result, sessionID = r, r.SessionID
			}
		case "antigravity":
			r, e := h.antigravityOAuth.GenerateAuthURL(ctx, proxyID)
			err = e
			if err == nil {
				result, sessionID = r, r.SessionID
			}
		case "grok":
			r, e := h.grokOAuth.GenerateAuthURL(ctx, proxyID, req.RedirectURI)
			err = e
			if err == nil {
				result, sessionID = r, r.SessionID
			}
		}
	case "exchange-code", "exchange-setup-token-code":
		switch platform {
		case "anthropic":
			result, err = h.oauth.ExchangeCode(ctx, &service.ExchangeCodeInput{SessionID: req.SessionID, Code: req.Code, ProxyID: proxyID})
		case "openai":
			result, err = h.openaiOAuth.ExchangeCode(ctx, &service.OpenAIExchangeCodeInput{SessionID: req.SessionID, Code: req.Code, State: req.State, RedirectURI: req.RedirectURI, ProxyID: proxyID})
		case "gemini":
			if req.OAuthType == "" {
				req.OAuthType = "code_assist"
			}
			if !validSharedGeminiOAuthType(req.OAuthType) {
				response.BadRequest(c, "Invalid oauth_type")
				return
			}
			result, err = h.geminiOAuth.ExchangeCode(ctx, &service.GeminiExchangeCodeInput{SessionID: req.SessionID, Code: req.Code, State: req.State, ProxyID: proxyID, OAuthType: req.OAuthType, TierID: req.TierID})
		case "antigravity":
			result, err = h.antigravityOAuth.ExchangeCode(ctx, &service.AntigravityExchangeCodeInput{SessionID: req.SessionID, Code: req.Code, State: req.State, ProxyID: proxyID})
		case "grok":
			result, err = h.grokOAuth.ExchangeCode(ctx, &service.GrokExchangeCodeInput{SessionID: req.SessionID, Code: req.Code, State: req.State, RedirectURI: req.RedirectURI, ProxyID: proxyID})
		}
	case "refresh-token":
		if strings.TrimSpace(req.RefreshToken) == "" {
			response.BadRequest(c, "refresh_token is required")
			return
		}
		switch platform {
		case "openai":
			if req.ClientID == "" {
				req.ClientID, _ = openai.OAuthClientConfigByPlatform(service.PlatformOpenAI)
			}
			result, err = h.openaiOAuth.RefreshTokenWithClientID(ctx, req.RefreshToken, proxyURL, req.ClientID)
		case "antigravity":
			result, err = h.antigravityOAuth.ValidateRefreshToken(ctx, req.RefreshToken, proxyID)
		case "grok":
			result, err = h.grokOAuth.ValidateRefreshToken(ctx, req.RefreshToken, proxyID)
		}
	case "cookie-auth", "setup-token-cookie-auth":
		if strings.TrimSpace(req.Code) == "" {
			response.BadRequest(c, "sessionKey is required")
			return
		}
		scope := "full"
		if action == "setup-token-cookie-auth" {
			scope = "inference"
		}
		result, err = h.oauth.CookieAuth(ctx, &service.CookieAuthInput{SessionKey: req.Code, ProxyID: proxyID, Scope: scope})
	case "validate-pat":
		token, e := h.openaiOAuth.ValidateCodexPersonalAccessToken(ctx, req.AccessToken, proxyURL)
		err = e
		if err == nil {
			result = gin.H{"credentials": h.openaiOAuth.BuildAccountCredentials(token), "extra": gin.H{"email": token.Email}}
		}
	case "validate-sso":
		token, e := h.grokOAuth.ValidateSSOToken(ctx, req.SSOToken, proxyID)
		err = e
		if err == nil {
			result = gin.H{"credentials": h.grokOAuth.BuildAccountCredentials(token), "extra": gin.H{"email": token.Email}}
		}
	}
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if sessionID != "" {
		h.rememberOAuthSession(subject.UserID, platform, sessionID)
	}
	if exchanging {
		h.sessionMu.Lock()
		delete(h.sessions, req.SessionID)
		h.sessionMu.Unlock()
	}
	response.Success(c, result)
}

func sharedOAuthActionAllowed(platform, action string) bool {
	switch platform {
	case "anthropic":
		return action == "generate-auth-url" || action == "generate-setup-token-url" || action == "exchange-code" || action == "exchange-setup-token-code" || action == "cookie-auth" || action == "setup-token-cookie-auth"
	case "openai":
		return action == "generate-auth-url" || action == "exchange-code" || action == "refresh-token" || action == "validate-pat" || action == "parse-session"
	case "gemini":
		return action == "generate-auth-url" || action == "exchange-code"
	case "antigravity":
		return action == "generate-auth-url" || action == "exchange-code" || action == "refresh-token"
	case "grok":
		return action == "generate-auth-url" || action == "exchange-code" || action == "refresh-token" || action == "validate-sso"
	}
	return false
}

func (h *SharedAccountPoolHandler) rememberOAuthSession(ownerID int64, platform, id string) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]sharedOAuthSession)
	}
	now := time.Now()
	count := 0
	oldestID := ""
	oldest := now
	for key, session := range h.sessions {
		if now.After(session.expiresAt) {
			delete(h.sessions, key)
			continue
		}
		if session.ownerID == ownerID {
			count++
			if oldestID == "" || session.expiresAt.Before(oldest) {
				oldestID, oldest = key, session.expiresAt
			}
		}
	}
	if count >= 20 {
		delete(h.sessions, oldestID)
	}
	h.sessions[id] = sharedOAuthSession{ownerID: ownerID, platform: platform, expiresAt: now.Add(15 * time.Minute)}
}
func (h *SharedAccountPoolHandler) ownsOAuthSession(ownerID int64, platform, id string) bool {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	session, ok := h.sessions[id]
	return ok && session.ownerID == ownerID && session.platform == platform && time.Now().Before(session.expiresAt)
}
func validSharedGeminiOAuthType(value string) bool {
	return value == "code_assist" || value == "google_one" || value == "ai_studio"
}
func sharedGeminiRedirectURI(c *gin.Context) string {
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin != "" {
		return strings.TrimRight(origin, "/") + "/auth/callback"
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if v := c.GetHeader("X-Forwarded-Proto"); v != "" {
		scheme = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host := c.Request.Host
	if v := c.GetHeader("X-Forwarded-Host"); v != "" {
		host = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	return fmt.Sprintf("%s://%s/auth/callback", scheme, host)
}
func (h *SharedAccountPoolHandler) OAuthCapabilities(c *gin.Context) {
	if h.settings != nil && !h.settings.IsSharedPoolEnabled(c.Request.Context()) {
		response.NotFound(c, "Shared account pool is disabled")
		return
	}
	switch c.Param("platform") {
	case "gemini":
		response.Success(c, h.geminiOAuth.GetOAuthConfig())
	default:
		response.NotFound(c, "Unsupported authorization platform")
	}
}
