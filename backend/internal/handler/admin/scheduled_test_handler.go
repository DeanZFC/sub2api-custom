package admin

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ScheduledTestHandler handles admin scheduled-test-plan management.
type ScheduledTestHandler struct {
	scheduledTestSvc *service.ScheduledTestService
}

type testDefinitionRequest struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Prompt      string  `json:"prompt"`
	OutputKind  string  `json:"output_kind"`
	// Kind is accepted as a frontend-friendly alias for output_kind.
	Kind      string `json:"kind"`
	Enabled   *bool  `json:"enabled"`
	SortOrder *int   `json:"sort_order"`
}

func (h *ScheduledTestHandler) ListDefinitions(c *gin.Context) {
	// Admins must be able to edit/re-enable disabled definitions. Filtering is
	// opt-in for API clients via enabled_only=true.
	defs, err := h.scheduledTestSvc.ListDefinitions(c.Request.Context(), c.Query("enabled_only") == "true")
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, defs)
}
func (h *ScheduledTestHandler) CreateDefinition(c *gin.Context) {
	var req testDefinitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	outputKind := req.OutputKind
	if outputKind == "" {
		outputKind = req.Kind
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	d := &service.ScheduledTestDefinition{Key: req.Key, Name: req.Name, Description: description, Prompt: req.Prompt, OutputKind: outputKind, Enabled: true}
	if req.SortOrder != nil {
		if *req.SortOrder < 0 {
			response.BadRequest(c, "sort_order must be non-negative")
			return
		}
		d.SortOrder = *req.SortOrder
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	out, err := h.scheduledTestSvc.CreateDefinition(c.Request.Context(), d)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, out)
}
func (h *ScheduledTestHandler) UpdateDefinition(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		response.BadRequest(c, "invalid definition id")
		return
	}
	d, e := h.scheduledTestSvc.GetDefinition(c.Request.Context(), id)
	if e != nil {
		response.NotFound(c, "definition not found")
		return
	}
	var req testDefinitionRequest
	if e = c.ShouldBindJSON(&req); e != nil {
		response.BadRequest(c, e.Error())
		return
	}
	if req.Key != "" {
		d.Key = req.Key
	}
	if req.Name != "" {
		d.Name = req.Name
	}
	// An explicit empty description clears the value; omitted fields preserve
	// the existing value for partial updates.
	if req.Description != nil {
		d.Description = *req.Description
	}
	if req.Prompt != "" {
		d.Prompt = req.Prompt
	}
	if req.OutputKind != "" {
		d.OutputKind = req.OutputKind
	} else if req.Kind != "" {
		d.OutputKind = req.Kind
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if req.SortOrder != nil {
		if *req.SortOrder < 0 {
			response.BadRequest(c, "sort_order must be non-negative")
			return
		}
		d.SortOrder = *req.SortOrder
	}
	out, e := h.scheduledTestSvc.UpdateDefinition(c.Request.Context(), d)
	if e != nil {
		response.BadRequest(c, e.Error())
		return
	}
	c.JSON(http.StatusOK, out)
}
func (h *ScheduledTestHandler) DeleteDefinition(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		response.BadRequest(c, "invalid definition id")
		return
	}
	if e = h.scheduledTestSvc.DeleteDefinition(c.Request.Context(), id); e != nil {
		response.InternalError(c, e.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// NewScheduledTestHandler creates a new ScheduledTestHandler.
func NewScheduledTestHandler(scheduledTestSvc *service.ScheduledTestService) *ScheduledTestHandler {
	return &ScheduledTestHandler{scheduledTestSvc: scheduledTestSvc}
}

type createScheduledTestPlanRequest struct {
	Name             string `json:"name"`
	SortOrder        *int   `json:"sort_order"`
	AccountID        *int64 `json:"account_id"`
	GroupID          *int64 `json:"group_id"`
	TestDefinitionID *int64 `json:"test_definition_id"`
	// test_type_id/type_id are aliases used by older generalized-test clients.
	TestTypeID      *int64 `json:"test_type_id"`
	TypeID          *int64 `json:"type_id"`
	TestType        string `json:"test_type"`
	TargetMode      string `json:"target_mode"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	CronExpression  string `json:"cron_expression" binding:"required"`
	Enabled         *bool  `json:"enabled"`
	MaxResults      int    `json:"max_results"`
	AutoRecover     *bool  `json:"auto_recover"`
}

type updateScheduledTestPlanRequest struct {
	// RawMessage preserves the distinction between an omitted field and an
	// explicit null. The latter is required when switching a plan from an
	// account target to a group target (or vice versa).
	AccountID        json.RawMessage `json:"account_id"`
	Name             *string         `json:"name"`
	SortOrder        *int            `json:"sort_order"`
	GroupID          json.RawMessage `json:"group_id"`
	TestDefinitionID json.RawMessage `json:"test_definition_id"`
	TestTypeID       *int64          `json:"test_type_id"`
	TypeID           *int64          `json:"type_id"`
	TargetMode       string          `json:"target_mode"`
	ModelID          string          `json:"model_id"`
	ReasoningEffort  json.RawMessage `json:"reasoning_effort"`
	CronExpression   string          `json:"cron_expression"`
	Enabled          *bool           `json:"enabled"`
	MaxResults       int             `json:"max_results"`
	AutoRecover      *bool           `json:"auto_recover"`
}

func parseNullableInt64(raw json.RawMessage, field string) (*int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
		if err == nil {
			err = fmt.Errorf("must be positive")
		}
		return nil, fmt.Errorf("%s %w", field, err)
	}
	return &value, nil
}

// ListByAccount GET /admin/accounts/:id/scheduled-test-plans
func (h *ScheduledTestHandler) ListByAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid account id")
		return
	}

	plans, err := h.scheduledTestSvc.ListPlansByAccount(c.Request.Context(), accountID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

func (h *ScheduledTestHandler) ListPlans(c *gin.Context) {
	plans, err := h.scheduledTestSvc.ListPlans(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

// Create POST /admin/scheduled-test-plans
func (h *ScheduledTestHandler) Create(c *gin.Context) {
	var req createScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	plan := &service.ScheduledTestPlan{
		Name:             req.Name,
		AccountID:        req.AccountID,
		GroupID:          req.GroupID,
		TestDefinitionID: req.TestDefinitionID,
		TestType:         req.TestType,
		TargetMode:       req.TargetMode,
		ModelID:          req.ModelID,
		ReasoningEffort:  req.ReasoningEffort,
		CronExpression:   req.CronExpression,
		Enabled:          true,
		MaxResults:       req.MaxResults,
	}
	if req.SortOrder != nil {
		if *req.SortOrder < 0 {
			response.BadRequest(c, "sort_order must be non-negative")
			return
		}
		plan.SortOrder = *req.SortOrder
	}
	if plan.TestDefinitionID == nil {
		if req.TestTypeID != nil {
			plan.TestDefinitionID = req.TestTypeID
		} else {
			plan.TestDefinitionID = req.TypeID
		}
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}

	created, err := h.scheduledTestSvc.CreatePlan(c.Request.Context(), plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}

// Update PUT /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Update(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}

	var req updateScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.ModelID != "" {
		existing.ModelID = req.ModelID
	}
	if req.TargetMode != "" {
		existing.TargetMode = req.TargetMode
	}
	if len(req.ReasoningEffort) > 0 {
		if bytes.Equal(bytes.TrimSpace(req.ReasoningEffort), []byte("null")) {
			existing.ReasoningEffort = ""
		} else {
			var effort string
			if err := json.Unmarshal(req.ReasoningEffort, &effort); err != nil {
				response.BadRequest(c, "reasoning_effort must be a string or null")
				return
			}
			existing.ReasoningEffort = effort
		}
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.SortOrder != nil {
		if *req.SortOrder < 0 {
			response.BadRequest(c, "sort_order must be non-negative")
			return
		}
		existing.SortOrder = *req.SortOrder
	}
	if len(req.AccountID) > 0 {
		value, parseErr := parseNullableInt64(req.AccountID, "account_id")
		if parseErr != nil {
			response.BadRequest(c, parseErr.Error())
			return
		}
		existing.AccountID = value
	}
	if len(req.GroupID) > 0 {
		value, parseErr := parseNullableInt64(req.GroupID, "group_id")
		if parseErr != nil {
			response.BadRequest(c, parseErr.Error())
			return
		}
		existing.GroupID = value
	}
	if len(req.TestDefinitionID) > 0 {
		value, parseErr := parseNullableInt64(req.TestDefinitionID, "test_definition_id")
		if parseErr != nil {
			response.BadRequest(c, parseErr.Error())
			return
		}
		existing.TestDefinitionID = value
	} else if req.TestTypeID != nil {
		existing.TestDefinitionID = req.TestTypeID
	} else if req.TypeID != nil {
		existing.TestDefinitionID = req.TypeID
	}
	if req.CronExpression != "" {
		existing.CronExpression = req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.MaxResults > 0 {
		existing.MaxResults = req.MaxResults
	}
	if req.AutoRecover != nil {
		existing.AutoRecover = *req.AutoRecover
	}

	updated, err := h.scheduledTestSvc.UpdatePlan(c.Request.Context(), existing)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete DELETE /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Delete(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	if err := h.scheduledTestSvc.DeletePlan(c.Request.Context(), planID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListResults GET /admin/scheduled-test-plans/:id/results
func (h *ScheduledTestHandler) ListResults(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	results, err := h.scheduledTestSvc.ListResults(c.Request.Context(), planID, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}

// DeleteResult DELETE /admin/test-results/:id
func (h *ScheduledTestHandler) DeleteResult(c *gin.Context) {
	resultID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || resultID <= 0 {
		response.BadRequest(c, "invalid test result id")
		return
	}
	if err := h.scheduledTestSvc.DeleteResult(c.Request.Context(), resultID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.NotFound(c, "test result not found")
			return
		}
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *ScheduledTestHandler) RunNow(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}
	if e = h.scheduledTestSvc.RunNow(c.Request.Context(), id); e != nil {
		response.BadRequest(c, e.Error())
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "test queued"})
}

// RetryResult POST /admin/test-results/:id/retry
func (h *ScheduledTestHandler) RetryResult(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid test result id")
		return
	}
	result, err := h.scheduledTestSvc.RetryResult(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			response.NotFound(c, "test result or plan not found")
		case errors.Is(err, service.ErrScheduledTestAccountRunning), errors.Is(err, service.ErrScheduledTestResultNotFailed):
			response.Error(c, http.StatusConflict, err.Error())
		default:
			response.BadRequest(c, err.Error())
		}
		return
	}
	c.JSON(http.StatusAccepted, result)
}
