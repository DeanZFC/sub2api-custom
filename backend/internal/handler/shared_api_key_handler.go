package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
)

type SharedAPIKeyHandler struct{ svc *service.SharedAPIKeyService }

func NewSharedAPIKeyHandler(svc *service.SharedAPIKeyService) *SharedAPIKeyHandler {
	return &SharedAPIKeyHandler{svc: svc}
}

type sharedAPIKeyRequest struct {
	Name       string  `json:"name"`
	Platform   string  `json:"platform"`
	ListingIDs []int64 `json:"listing_ids"`
	Status     string  `json:"status"`
}

func userID(c *gin.Context) (int64, bool) {
	s, ok := middleware.GetAuthSubjectFromContext(c)
	return s.UserID, ok
}
func (h *SharedAPIKeyHandler) List(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	v, e := h.svc.List(c, uid)
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"items": v})
}
func (h *SharedAPIKeyHandler) Create(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req sharedAPIKeyRequest
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	v, e := h.svc.Create(c, uid, req.Name, req.Platform, req.ListingIDs)
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, v)
}
func (h *SharedAPIKeyHandler) Update(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}
	var req sharedAPIKeyRequest
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	if e = h.svc.Update(c, uid, id, req.Name, req.Status, req.ListingIDs); e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"updated": true})
}
func (h *SharedAPIKeyHandler) Delete(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}
	if e = h.svc.Delete(c, uid, id); e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
