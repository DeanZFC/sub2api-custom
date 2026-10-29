package service

import (
	"context"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// The shared slot follows a WS connection's current turn. Asynchronous evidence
// recording receives a frozen identity and cannot observe a later turn.
type groupQualityTrafficKey struct{}

type groupQualityTrafficSlot struct {
	mu      sync.Mutex
	request GroupQualityTrafficRequest
}

// GroupQualityTrafficRequest carries evidence identity only; it grants no
// routing permission, recovery capacity, or customer traffic reservation.
type GroupQualityTrafficRequest struct {
	Key     string
	GroupID int64
	NonText bool
	Model   string
}

// GroupQualityTrafficContext creates an independent request snapshot. No body,
// authorization header, or cache prefix is retained.
func GroupQualityTrafficContext(ctx context.Context, r GroupQualityTrafficRequest) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, groupQualityTrafficKey{}, &groupQualityTrafficSlot{request: r})
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

func beginGroupQualityTrafficTurn(c *gin.Context, key string) {
	if c == nil || c.Request == nil {
		return
	}
	ctx := c.Request.Context()
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		slot = &groupQualityTrafficSlot{}
		c.Request = c.Request.WithContext(context.WithValue(ctx, groupQualityTrafficKey{}, slot))
	}
	slot.mu.Lock()
	path := ""
	if c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	slot.request = GroupQualityTrafficRequest{Key: key, GroupID: slot.request.GroupID, Model: slot.request.Model, NonText: passiveQualityNonTextEndpoint(path) || strings.Contains(path, "/compact")}
	slot.mu.Unlock()
}

// Called after reading each HTTP body and each WS response.create. Only public
// model identity and text scope are retained; no request content is stored.
func PrepareGroupQualityTrafficBody(ctx context.Context, body []byte) {
	if ctx == nil {
		return
	}
	slot, _ := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot)
	if slot == nil {
		return
	}
	slot.mu.Lock()
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
		copy := slot.request
		slot.mu.Unlock()
		base = context.WithValue(base, groupQualityTrafficKey{}, &groupQualityTrafficSlot{request: copy})
	}
	return base
}
