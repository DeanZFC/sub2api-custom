package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type PassiveQualityHandler struct {
	svc *service.PassiveQualityService
}

func NewPassiveQualityHandler(svc *service.PassiveQualityService) *PassiveQualityHandler {
	return &PassiveQualityHandler{svc: svc}
}
func (h *PassiveQualityHandler) Capture() gin.HandlerFunc { return h.svc.Capture() }
func passiveError(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "passive quality policy or state not found")
		return
	}
	if errors.Is(err, service.ErrPassiveQualityConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	response.InternalError(c, "passive quality operation failed")
}
func passiveID(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid ID")
		return 0, false
	}
	return id, true
}
func passiveQuery(c *gin.Context, key string) (int64, bool) {
	v := c.Query(key)
	if v == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 0 {
		response.BadRequest(c, "invalid "+key)
		return 0, false
	}
	return id, true
}
func (h *PassiveQualityHandler) Policies(c *gin.Context) {
	out, err := h.svc.Repo.ListPolicies(c.Request.Context())
	if err != nil {
		passiveError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
func (h *PassiveQualityHandler) Save(c *gin.Context) {
	var p service.PassiveQualityPolicy
	if err := c.ShouldBindJSON(&p); err != nil {
		response.BadRequest(c, "invalid policy JSON")
		return
	}
	if c.Param("id") != "" {
		id, ok := passiveID(c, "id")
		if !ok {
			return
		}
		p.ID = id
	} else {
		p.ID = 0
		p.Version = 0
	}
	if err := p.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.svc.Repo.SavePolicy(c.Request.Context(), &p); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, service.ErrPassiveQualityConflict) {
			passiveError(c, err)
		} else {
			response.BadRequest(c, err.Error())
		}
		return
	}
	h.svc.ConfigurationChanged()
	c.JSON(http.StatusOK, p)
}
func (h *PassiveQualityHandler) Delete(c *gin.Context) {
	id, ok := passiveID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Repo.DeletePolicy(c.Request.Context(), id); err != nil {
		passiveError(c, err)
		return
	}
	h.svc.ConfigurationChanged()
	c.Status(http.StatusNoContent)
}
func (h *PassiveQualityHandler) States(c *gin.Context) {
	id, ok := passiveQuery(c, "account_id")
	if !ok {
		return
	}
	out, err := h.svc.Repo.ListStates(c.Request.Context(), id)
	if err != nil {
		passiveError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
func (h *PassiveQualityHandler) Events(c *gin.Context) {
	id, ok := passiveQuery(c, "account_id")
	if !ok {
		return
	}
	before, ok := passiveQuery(c, "before")
	if !ok {
		return
	}
	out, err := h.svc.Repo.ListEvents(c.Request.Context(), id, before)
	if err != nil {
		passiveError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
func (h *PassiveQualityHandler) Act(c *gin.Context) {
	pid, ok := passiveID(c, "id")
	if !ok {
		return
	}
	aid, ok := passiveID(c, "account")
	if !ok {
		return
	}
	var req struct {
		Action  string `json:"action"`
		Seconds int    `json:"seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid action JSON")
		return
	}
	if req.Action != "verify" && req.Action != "exempt" && req.Action != "cancel_exemption" {
		response.BadRequest(c, "unsupported action")
		return
	}
	if req.Action == "exempt" && (req.Seconds < 60 || req.Seconds > 86400) {
		response.BadRequest(c, "exemption must last 60..86400 seconds")
		return
	}
	if err := h.svc.Repo.Act(c.Request.Context(), pid, aid, req.Action, req.Seconds); err != nil {
		passiveError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
