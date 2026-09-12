package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SharedAccountPoolHandler struct {
	repo             service.SharedAccountPoolRepository
	wallet           *service.SharedWalletService
	uploader         *service.SharedAccountUploadService
	settings         *service.SettingService
	oauth            *service.OAuthService
	openaiOAuth      *service.OpenAIOAuthService
	geminiOAuth      *service.GeminiOAuthService
	antigravityOAuth *service.AntigravityOAuthService
	grokOAuth        *service.GrokOAuthService
	concurrency      *service.ConcurrencyService
	sessionMu        sync.Mutex
	sessions         map[string]sharedOAuthSession
}

func NewSharedAccountPoolHandler(repo service.SharedAccountPoolRepository, wallet *service.SharedWalletService, uploader *service.SharedAccountUploadService, settings *service.SettingService, oauth *service.OAuthService, openaiOAuth *service.OpenAIOAuthService, geminiOAuth *service.GeminiOAuthService, antigravityOAuth *service.AntigravityOAuthService, grokOAuth *service.GrokOAuthService, concurrency *service.ConcurrencyService) *SharedAccountPoolHandler {
	return &SharedAccountPoolHandler{repo: repo, wallet: wallet, uploader: uploader, settings: settings, oauth: oauth, openaiOAuth: openaiOAuth, geminiOAuth: geminiOAuth, antigravityOAuth: antigravityOAuth, grokOAuth: grokOAuth, concurrency: concurrency, sessions: make(map[string]sharedOAuthSession)}
}

type sharedUploadRequest struct {
	Name                  string         `json:"name" binding:"required"`
	Platform              string         `json:"platform" binding:"required"`
	Type                  string         `json:"type" binding:"required"`
	Credentials           map[string]any `json:"credentials" binding:"required"`
	Extra                 map[string]any `json:"extra"`
	ExpiresAt             *time.Time     `json:"expires_at"`
	Concurrency           int            `json:"concurrency"`
	ConcurrencyMultiplier float64        `json:"concurrency_multiplier"`
	SellRate              float64        `json:"sell_rate"`
	ProxyURL              string         `json:"proxy_url"`
}

func (h *SharedAccountPoolHandler) Upload(c *gin.Context) {
	if h.settings != nil && !h.settings.IsSharedPoolEnabled(c.Request.Context()) {
		response.NotFound(c, "Shared account pool is disabled")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req sharedUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	item, err := h.uploader.Upload(c.Request.Context(), subject.UserID, service.SharedAccountUploadInput{Name: req.Name, Platform: req.Platform, Type: req.Type, Credentials: req.Credentials, Extra: req.Extra, ExpiresAt: req.ExpiresAt, Concurrency: req.Concurrency, ConcurrencyMultiplier: req.ConcurrencyMultiplier, SellRate: req.SellRate, ProxyURL: req.ProxyURL})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": item.ID, "status": item.Status, "account_id": item.AccountID})
}
func (h *SharedAccountPoolHandler) SetStatus(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid listing id")
		return
	}
	status := c.Param("action")
	if status == "resume" && h.settings != nil && !h.settings.IsSharedPoolEnabled(c.Request.Context()) {
		response.NotFound(c, "Shared account pool is disabled")
		return
	}
	if status != "pause" && status != "resume" {
		response.BadRequest(c, "invalid listing action")
		return
	}
	if status == "pause" {
		status = "paused"
	} else {
		status = "active"
	}
	if err = h.repo.SetListingStatus(c.Request.Context(), subject.UserID, id, status); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": status})
}

func (h *SharedAccountPoolHandler) SetListed(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid listing id")
		return
	}
	var req struct {
		Listed *bool `json:"listed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Listed == nil {
		response.BadRequest(c, "listed is required")
		return
	}
	if err = h.repo.SetListingListed(c.Request.Context(), subject.UserID, id, *req.Listed); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "listed": *req.Listed})
}
func (h *SharedAccountPoolHandler) Delete(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid listing id")
		return
	}
	if err = h.repo.DeleteListing(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "deleted": true})
}

// ListCards returns redacted, card-ready shared accounts and recent calls.
func (h *SharedAccountPoolHandler) ListCards(c *gin.Context) {
	if h.settings != nil && !h.settings.IsSharedPoolEnabled(c.Request.Context()) {
		response.NotFound(c, "Shared account pool is disabled")
		return
	}
	limit, recent := 50, 5
	if v, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil {
		limit = v
	}
	if v, err := strconv.Atoi(c.DefaultQuery("recent_limit", "5")); err == nil {
		recent = v
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if recent <= 0 || recent > 20 {
		recent = 5
	}
	items, err := h.repo.ListPublicCards(c.Request.Context(), c.Query("platform"), limit, recent)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.attachConcurrency(c.Request.Context(), items)
	response.Success(c, gin.H{"items": items, "limit": limit, "recent_limit": recent})
}

// AdminListCards returns the complete moderation view, including unpublished
// and paused listings. The /admin route is protected by AdminAuthMiddleware.
func (h *SharedAccountPoolHandler) AdminListCards(c *gin.Context) {
	adminRepo, ok := h.repo.(service.SharedAccountPoolAdminRepository)
	if !ok {
		response.InternalError(c, "shared pool admin controls unavailable")
		return
	}
	limit, recent := 100, 5
	if v, err := strconv.Atoi(c.DefaultQuery("limit", "100")); err == nil {
		limit = v
	}
	if v, err := strconv.Atoi(c.DefaultQuery("recent_limit", "5")); err == nil {
		recent = v
	}
	var owner *int64
	if raw := c.Query("owner_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "invalid owner_id")
			return
		}
		owner = &id
	}
	items, err := adminRepo.ListAdminCards(c.Request.Context(), c.Query("platform"), c.Query("status"), c.Query("search"), owner, limit, recent)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.attachConcurrency(c.Request.Context(), items)
	response.Success(c, gin.H{"items": items, "limit": limit, "recent_limit": recent})
}

func (h *SharedAccountPoolHandler) AdminListUsers(c *gin.Context) {
	adminRepo, ok := h.repo.(service.SharedAccountPoolAdminRepository)
	if !ok {
		response.InternalError(c, "shared pool admin controls unavailable")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := adminRepo.ListAdminUsers(c.Request.Context(), c.Query("search"), limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "limit": limit})
}

func (h *SharedAccountPoolHandler) AdminSetStatus(c *gin.Context) {
	adminRepo, ok := h.repo.(service.SharedAccountPoolAdminRepository)
	if !ok {
		response.InternalError(c, "shared pool admin controls unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid listing id")
		return
	}
	var req struct {
		Status string `json:"status" binding:"required,oneof=active paused suspended invalid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := adminRepo.SetListingAdminStatus(c.Request.Context(), id, req.Status); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": req.Status})
}

func (h *SharedAccountPoolHandler) AdminSetListed(c *gin.Context) {
	adminRepo, ok := h.repo.(service.SharedAccountPoolAdminRepository)
	if !ok {
		response.InternalError(c, "shared pool admin controls unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid listing id")
		return
	}
	var req struct {
		Listed *bool `json:"listed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Listed == nil {
		response.BadRequest(c, "listed is required")
		return
	}
	if err := adminRepo.SetListingAdminListed(c.Request.Context(), id, *req.Listed); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "listed": *req.Listed})
}

func (h *SharedAccountPoolHandler) AdminSetUserPublishPermission(c *gin.Context) {
	adminRepo, ok := h.repo.(service.SharedAccountPoolAdminRepository)
	if !ok {
		response.InternalError(c, "shared pool admin controls unavailable")
		return
	}
	uid, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || uid <= 0 {
		response.BadRequest(c, "invalid user id")
		return
	}
	var req struct {
		Enabled      *bool      `json:"enabled"`
		Reason       string     `json:"reason"`
		BlockedUntil *time.Time `json:"blocked_until"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		response.BadRequest(c, "enabled is required")
		return
	}
	if err := adminRepo.SetUserSharedPublishPermission(c.Request.Context(), uid, *req.Enabled, req.Reason, req.BlockedUntil); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"user_id": uid, "enabled": *req.Enabled, "reason": req.Reason, "blocked_until": req.BlockedUntil})
}

func (h *SharedAccountPoolHandler) attachConcurrency(ctx context.Context, items []service.SharedAccountCard) {
	if h == nil || h.concurrency == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if item.AccountID > 0 {
			ids = append(ids, item.AccountID)
		}
	}
	if len(ids) == 0 {
		return
	}
	counts, err := h.concurrency.GetAccountConcurrencyBatch(ctx, ids)
	if err != nil {
		return
	}
	for i := range items {
		items[i].CurrentConcurrency = counts[items[i].AccountID]
	}
}

func (h *SharedAccountPoolHandler) parseOwnedAccountID(c *gin.Context) (int64, int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, 0, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account id")
		return 0, 0, false
	}
	return subject.UserID, id, true
}

func (h *SharedAccountPoolHandler) writeOwnedAccount(c *gin.Context, account *service.Account, listing *service.SharedAccountListing) {
	out := dto.AccountFromServiceShallow(account)
	if out != nil && listing != nil {
		out.SharedTotalCallCount = listing.TotalCallCount
		out.SharedListingStatus = listing.Status
	}
	response.Success(c, out)
}

func (h *SharedAccountPoolHandler) GetAccount(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	account, listing, err := h.uploader.GetOwnedDetail(c.Request.Context(), ownerID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeOwnedAccount(c, account, listing)
}

type sharedAccountUpdateRequest struct {
	Name           *string         `json:"name"`
	Credentials    *map[string]any `json:"credentials"`
	Extra          *map[string]any `json:"extra"`
	Concurrency    *int            `json:"concurrency"`
	RateMultiplier *float64        `json:"rate_multiplier"`
	ExpiresAt      *int64          `json:"expires_at"`
	ProxyURL       string          `json:"proxy_url"`
}

func (h *SharedAccountPoolHandler) UpdateAccount(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	var req sharedAccountUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	in := service.SharedAccountUpdateInput{Name: req.Name, Credentials: req.Credentials, Extra: req.Extra, Concurrency: req.Concurrency, SellRate: req.RateMultiplier, ProxyURL: req.ProxyURL}
	if req.ExpiresAt != nil {
		if *req.ExpiresAt <= 0 {
			in.ClearExpiry = true
		} else {
			t := time.Unix(*req.ExpiresAt, 0).UTC()
			in.ExpiresAt = &t
		}
	}
	account, err := h.uploader.UpdateOwned(c.Request.Context(), ownerID, accountID, in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	_, listing, _ := h.uploader.GetOwnedDetail(c.Request.Context(), ownerID, accountID)
	h.writeOwnedAccount(c, account, listing)
}

type sharedApplyOAuthRequest struct {
	Type        string         `json:"type" binding:"required"`
	Credentials map[string]any `json:"credentials" binding:"required"`
	Extra       map[string]any `json:"extra"`
}

func (h *SharedAccountPoolHandler) ApplyOAuthCredentials(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	var req sharedApplyOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	account, err := h.uploader.ApplyOwnedOAuthCredentials(c.Request.Context(), ownerID, accountID, req.Type, req.Credentials, req.Extra)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	_, listing, _ := h.uploader.GetOwnedDetail(c.Request.Context(), ownerID, accountID)
	h.writeOwnedAccount(c, account, listing)
}

func (h *SharedAccountPoolHandler) ClearError(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	account, err := h.uploader.ClearOwnedError(c.Request.Context(), ownerID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	_, listing, _ := h.uploader.GetOwnedDetail(c.Request.Context(), ownerID, accountID)
	h.writeOwnedAccount(c, account, listing)
}

func (h *SharedAccountPoolHandler) writeUpstreamCatalog(c *gin.Context, catalog *service.UpstreamModelCatalog, err error) {
	if err != nil {
		var syncErr *service.UpstreamModelSyncError
		if errors.As(err, &syncErr) {
			switch syncErr.Kind {
			case service.UpstreamModelSyncErrorConfiguration, service.UpstreamModelSyncErrorUnsupported:
				response.BadRequest(c, syncErr.SafeMessage())
			case service.UpstreamModelSyncErrorInternal:
				response.InternalError(c, syncErr.SafeMessage())
			default:
				slog.Warn("shared_sync_upstream_models_failed", "kind", syncErr.Kind)
				response.Error(c, http.StatusBadGateway, syncErr.SafeMessage())
			}
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, catalog)
}

func (h *SharedAccountPoolHandler) GetAvailableModels(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	account, err := h.uploader.GetOwned(c.Request.Context(), ownerID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if ids := service.ConfiguredTestModelIDs(account); len(ids) > 0 {
		response.Success(c, service.TestPickerModels(ids))
		return
	}
	response.Success(c, service.TestPickerModels(nil))
}

type sharedTestAccountRequest struct {
	ModelID      string `json:"model_id"`
	Prompt       string `json:"prompt"`
	Mode         string `json:"mode"`
	ImageDataURL string `json:"image_data_url"`
	AudioDataURL string `json:"audio_data_url"`
}

func (h *SharedAccountPoolHandler) TestAccount(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	account, err := h.uploader.GetOwned(c.Request.Context(), ownerID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req sharedTestAccountRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.uploader.TestOwnedAccount(c, ownerID, account.ID, req.ModelID, req.Prompt, req.Mode, service.AccountTestOptions{ImageDataURL: req.ImageDataURL, AudioDataURL: req.AudioDataURL}); err != nil {
		return
	}
}

func (h *SharedAccountPoolHandler) SyncUpstreamModels(c *gin.Context) {
	ownerID, accountID, ok := h.parseOwnedAccountID(c)
	if !ok {
		return
	}
	catalog, err := h.uploader.SyncOwnedUpstreamModels(c.Request.Context(), ownerID, accountID)
	h.writeUpstreamCatalog(c, catalog, err)
}

func (h *SharedAccountPoolHandler) SyncUpstreamModelsPreview(c *gin.Context) {
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req struct {
		Platform     string            `json:"platform" binding:"required"`
		Type         string            `json:"type" binding:"required"`
		BaseURL      string            `json:"base_url"`
		APIKey       string            `json:"api_key" binding:"required"`
		ModelMapping map[string]string `json:"model_mapping"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	modelMapping := make(map[string]any, len(req.ModelMapping))
	for sourceModel, upstreamModel := range req.ModelMapping {
		modelMapping[sourceModel] = upstreamModel
	}
	catalog, err := h.uploader.SyncUpstreamModelsPreview(c.Request.Context(), &service.Account{
		Platform: req.Platform,
		Type:     req.Type,
		Credentials: map[string]any{
			"api_key":       req.APIKey,
			"base_url":      req.BaseURL,
			"model_mapping": modelMapping,
		},
	})
	h.writeUpstreamCatalog(c, catalog, err)
}

func (h *SharedAccountPoolHandler) MyCards(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	items, err := h.repo.GetOwnerCards(c.Request.Context(), subject.UserID, 200, 10)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.attachConcurrency(c.Request.Context(), items)
	response.Success(c, gin.H{"items": items})
}

func (h *SharedAccountPoolHandler) Wallet(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	wallet, err := h.wallet.Wallet(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, wallet)
}
func (h *SharedAccountPoolHandler) Transfer(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	result, err := h.wallet.Transfer(c.Request.Context(), subject.UserID, c.GetHeader("Idempotency-Key"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
