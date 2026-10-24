package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// The shared slot follows a WS connection's current turn. Usage workers get a
// frozen copy, so a subsequent turn cannot settle an earlier turn's reservation.
type groupQualityTrafficKey struct{}

var ErrGroupQualityTrafficBudget = errors.New("group quality validation input budget is temporarily exhausted")

type groupQualityTrafficSlot struct {
	mu             sync.Mutex
	request        GroupQualityTrafficRequest
	gate           GroupQualityTrafficGate
	cancel         context.CancelCauseFunc
	stopFallback   func()
	fallbackActive bool
}

type GroupQualityTrafficRequest struct {
	Key            string
	GroupID        int64
	EstimatedInput int64
	NonText        bool
	Model          string
	LeaseID        string
	AccountID      int64
}

type GroupQualityTrafficGate interface {
	AcquireGroupQualityTraffic(context.Context, int64, string, string) (bool, bool, error)
	ReleaseGroupQualityTraffic(context.Context, string, bool) error
	SettleGroupQualityTraffic(context.Context, GroupQualityTrafficRequest, int64, int64, int64, bool) error
}

type GroupQualityTrafficSummary struct {
	Enabled                bool    `json:"enabled"`
	Mode                   string  `json:"mode"`
	VerifiedAccounts       int     `json:"verified_accounts"`
	ValidationInputTokens  int64   `json:"validation_input_tokens"`
	TotalInputTokens       int64   `json:"total_input_tokens"`
	ValidationSharePercent float64 `json:"validation_share_percent"`
	ReservedInputTokens    int64   `json:"reserved_input_tokens"`
	BudgetTokens           int64   `json:"budget_tokens"`
	RemainingTokens        int64   `json:"remaining_tokens"`
	LimitedRequests        int64   `json:"limited_requests"`
}

func GroupQualityTrafficBudget(input, read int64, target float64, verified int) (string, int64) {
	input = groupTrafficMax64(input, 0)
	read = min(groupTrafficMax64(read, 0), input)
	if math.IsNaN(target) || math.IsInf(target, 0) || target <= 0 || target > 100 {
		return "conserving", 0
	}
	if verified <= 0 {
		return "bootstrap", 1_000_000
	}
	if input <= 0 || float64(read)*100 <= float64(input)*target {
		return "conserving", groupTrafficMax64(4096, input/100)
	}
	// Reserve half the measured headroom for normal workload variation. Validation
	// is additionally capped at five percent even when the pool looks excellent.
	headroom := float64(read)*100/target - float64(input)
	return "normal", groupTrafficMax64(0, int64(math.Min(float64(input/20), math.Floor(headroom/2))))
}

func groupTrafficMax64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// GroupQualityTrafficContext creates an independent request snapshot. No body,
// authorization header, or cache prefix is retained.
func GroupQualityTrafficContext(ctx context.Context, r GroupQualityTrafficRequest) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	return context.WithValue(ctx, groupQualityTrafficKey{}, &groupQualityTrafficSlot{request: r, cancel: cancel})
}

func GroupQualityTrafficRequestFromContext(ctx context.Context) (GroupQualityTrafficRequest, bool) {
	if ctx == nil {
		return GroupQualityTrafficRequest{}, false
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		return GroupQualityTrafficRequest{}, false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	r := slot.request
	return r, r.Key != "" && r.GroupID > 0 && !r.NonText
}

func beginGroupQualityTrafficTurn(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	ctx := c.Request.Context()
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(ctx)
		slot = &groupQualityTrafficSlot{cancel: cancel}
		c.Request = c.Request.WithContext(context.WithValue(ctx, groupQualityTrafficKey{}, slot))
	}
	slot.mu.Lock()
	path := ""
	if c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	slot.request = GroupQualityTrafficRequest{Key: uuid.NewString(), GroupID: slot.request.GroupID, Model: slot.request.Model, EstimatedInput: 65536, NonText: passiveQualityNonTextEndpoint(path) || strings.Contains(path, "/compact")}
	slot.gate = nil
	slot.stopFallback = nil
	slot.fallbackActive = false
	slot.mu.Unlock()
}

// Called after reading each HTTP body and each WS response.create. Only a size
// estimate is retained; no prompt, headers or credentials enter the ledger.
func PrepareGroupQualityTrafficBody(ctx context.Context, body []byte) {
	if ctx == nil {
		return
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		return
	}
	// UTF-8 bytes are deliberately conservative for text, including tool schemas.
	// Server-side history and external content have no exact local token count.
	estimate := groupTrafficMax64(int64(len(body)), 4096)
	if gjson.GetBytes(body, "previous_response_id").String() != "" || gjson.GetBytes(body, "conversation").Exists() {
		estimate = groupTrafficMax64(estimate, 262144)
	}
	slot.mu.Lock()
	slot.request.EstimatedInput = estimate
	slot.request.NonText = slot.request.NonText || gjson.GetBytes(body, "compaction_trigger").Exists()
	if model := strings.TrimSpace(gjson.GetBytes(body, "model").String()); model != "" {
		slot.request.Model = model
	}
	slot.mu.Unlock()
}

func SetGroupQualityTrafficGroup(c *gin.Context, groupID int64) {
	if c == nil || c.Request == nil {
		return
	}
	SetGroupQualityTrafficGroupContext(c.Request.Context(), groupID)
}

// SetGroupQualityTrafficGroupContext follows the actual selection group before
// admission. API-key fallback can change groups inside the scheduler, before
// the handler sees a selected account or activates the fallback API key.
func SetGroupQualityTrafficGroupContext(ctx context.Context, groupID int64) {
	if ctx == nil {
		return
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	slot.request.GroupID = groupID
	slot.mu.Unlock()
}

// SetGroupQualityTrafficModel tracks an effective public WS model even when a
// follow-up payload omits model after session.update.
func SetGroupQualityTrafficModel(ctx context.Context, model string) {
	if ctx == nil || strings.TrimSpace(model) == "" {
		return
	}
	if slot, ok := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot); ok {
		slot.mu.Lock()
		slot.request.Model = strings.TrimSpace(model)
		slot.mu.Unlock()
	}
}

// ExcludeGroupQualityTraffic marks explicit media-generation requests outside
// the text-quality population. Passive tool declarations are not sufficient.
func ExcludeGroupQualityTraffic(ctx context.Context) {
	if ctx == nil {
		return
	}
	if slot, ok := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot); ok {
		slot.mu.Lock()
		slot.request.NonText = true
		slot.mu.Unlock()
	}
}

func freezeGroupQualityTrafficContext(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent == nil {
		return base
	}
	if slot, _ := parent.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot); slot != nil {
		slot.mu.Lock()
		copy, gate := slot.request, slot.gate
		slot.mu.Unlock()
		base = context.WithValue(base, groupQualityTrafficKey{}, &groupQualityTrafficSlot{request: copy, gate: gate})
	}
	return base
}

func bindGroupQualityTrafficLease(ctx context.Context, gate GroupQualityTrafficGate, aid int64, model, lease string) {
	if ctx == nil {
		return
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.request.LeaseID, slot.request.AccountID, slot.request.Model = lease, aid, model
	slot.gate = gate
	slot.stopFallback = nil
	slot.fallbackActive = false
}

// ReleaseUnusedGroupQualityTraffic refunds only a proven pre-send rejection.
// Ordinary completion/cancellation release must retain the conservative debit.
func ReleaseUnusedGroupQualityTraffic(ctx context.Context, aid int64) {
	if ctx == nil {
		return
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	request, gate, stopFallback := slot.request, slot.gate, slot.stopFallback
	slot.mu.Unlock()
	if gate == nil || request.LeaseID == "" || request.AccountID != aid {
		return
	}
	if stopFallback != nil {
		stopFallback()
	}
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := gate.ReleaseGroupQualityTraffic(release, request.LeaseID, true); err != nil {
		logger.LegacyPrintf("service.group_quality", "refund unused traffic lease failed: %v", err)
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.request.LeaseID == request.LeaseID {
		slot.request.LeaseID = ""
	}
}

func recordGroupQualityTrafficUsage(ctx context.Context, aid, input, read int64, reported bool) {
	r, ok := GroupQualityTrafficRequestFromContext(ctx)
	if !ok || r.AccountID != aid || r.LeaseID == "" {
		return
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	slot.mu.Lock()
	gate := slot.gate
	slot.mu.Unlock()
	if gate != nil {
		if err := gate.SettleGroupQualityTraffic(ctx, r, aid, groupTrafficMax64(0, input), groupTrafficMax64(0, read), reported); err != nil {
			logger.LegacyPrintf("service.group_quality", "settle traffic lease failed: %v", err)
		}
	}
}
