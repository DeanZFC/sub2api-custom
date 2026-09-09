package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SharedAccountPoolHandler struct {
	repo     service.SharedAccountPoolRepository
	wallet   *service.SharedWalletService
	uploader *service.SharedAccountUploadService
	settings *service.SettingService
}

func NewSharedAccountPoolHandler(repo service.SharedAccountPoolRepository, wallet *service.SharedWalletService, uploader *service.SharedAccountUploadService, settings *service.SettingService) *SharedAccountPoolHandler {
	return &SharedAccountPoolHandler{repo: repo, wallet: wallet, uploader: uploader, settings: settings}
}

type sharedUploadRequest struct {
	Name                  string         `json:"name" binding:"required"`
	Platform              string         `json:"platform" binding:"required"`
	Type                  string         `json:"type" binding:"required"`
	Credentials           map[string]any `json:"credentials" binding:"required"`
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
	item, err := h.uploader.Upload(c.Request.Context(), subject.UserID, service.SharedAccountUploadInput{Name: req.Name, Platform: req.Platform, Type: req.Type, Credentials: req.Credentials, Concurrency: req.Concurrency, ConcurrencyMultiplier: req.ConcurrencyMultiplier, SellRate: req.SellRate, ProxyURL: req.ProxyURL})
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
	response.Success(c, gin.H{"items": items, "limit": limit, "recent_limit": recent})
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
