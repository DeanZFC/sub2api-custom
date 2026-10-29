package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const openAIUserAffinityTTL = 24 * time.Hour

// SessionAccountCompareAndSetter is implemented by the shared Redis cache.
// expected=0 means absent; an old in-flight request cannot replace a newer owner.
// Caches without atomic support retain ordinary session routing, rather than
// emulating compare-and-set with a racy read followed by an unconditional write.
type SessionAccountCompareAndSetter interface {
	CompareAndSetSessionAccountID(context.Context, int64, string, int64, int64, time.Duration) (bool, error)
}

type openAIUserAffinityContextKey struct{}

type openAIUserAffinity struct {
	groupID                 int64
	key                     string
	sessionHash             string
	sessionAccountID        int64
	sessionPrimaryAccountID int64
	userAccountID           int64
	migrationFrom           int64
}

func openAIUserAffinityFromContext(ctx context.Context) *openAIUserAffinity {
	if ctx == nil {
		return nil
	}
	affinity, _ := ctx.Value(openAIUserAffinityContextKey{}).(*openAIUserAffinity)
	return affinity
}

func openAIAuthenticatedUserID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	if id, _ := ctx.Value(ctxkey.UserID).(int64); id > 0 {
		return id
	}
	if key, ok := APIKeyForUsageContext(ctx); ok {
		if key.UserID > 0 {
			return key.UserID
		}
		if key.User != nil {
			return key.User.ID
		}
	}
	return 0
}

func openAIUserAffinityKey(userID, groupID int64, model string) string {
	if userID <= 0 || groupID <= 0 || strings.TrimSpace(model) == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(model))
	return fmt.Sprintf("openai:user-affinity:v1:%d:%d:%x", userID, groupID, digest)
}

// A user's preferred account only seeds new sessions. Existing conversations
// and response owners remain authoritative. This is independent of client
// session IDs and never changes the prompt/cache identity sent upstream.
func (s *OpenAIGatewayService) withOpenAIUserAffinity(ctx context.Context, groupID *int64, sessionHash, requestedModel string) (context.Context, string) {
	// Re-selection may start from a previous admission context. Never reuse
	// another selection's group or model scope.
	if openAIUserAffinityFromContext(ctx) != nil {
		ctx = context.WithValue(ctx, openAIUserAffinityContextKey{}, (*openAIUserAffinity)(nil))
	}
	if s == nil || s.cache == nil {
		return ctx, sessionHash
	}
	if _, ok := s.cache.(SessionAccountCompareAndSetter); !ok {
		return ctx, sessionHash
	}
	model := strings.TrimSpace(requestedRoutingModel(ctx, requestedModel))
	key := openAIUserAffinityKey(openAIAuthenticatedUserID(ctx), derefGroupID(groupID), model)
	if key == "" {
		return ctx, sessionHash
	}
	affinity := &openAIUserAffinity{groupID: derefGroupID(groupID), key: key, sessionHash: sessionHash}
	id, err := s.cache.GetSessionAccountID(ctx, affinity.groupID, key)
	if err != nil && !errors.Is(err, ErrStickySessionNotFound) {
		return ctx, sessionHash
	}
	affinity.userAccountID = id
	if sessionHash != "" {
		// Record the primary-key value separately from the effective dual-read
		// value. During the legacy-key migration window a binding may be read
		// from the legacy key while the primary key is absent; CAS must then
		// compare against zero on the primary key and materialize the new owner.
		primaryID, primaryErr := s.cache.GetSessionAccountID(ctx, affinity.groupID, s.openAISessionCacheKey(sessionHash))
		if primaryErr != nil && !errors.Is(primaryErr, ErrStickySessionNotFound) {
			return ctx, sessionHash
		}
		affinity.sessionPrimaryAccountID = primaryID
		id, err = s.getStickySessionAccountID(ctx, groupID, sessionHash)
		if err != nil && !errors.Is(err, ErrStickySessionNotFound) {
			return ctx, sessionHash
		}
		affinity.sessionAccountID = id
	}
	return context.WithValue(ctx, openAIUserAffinityContextKey{}, affinity), sessionHash
}

func (s *OpenAIGatewayService) stickySessionAccountIDForRouting(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	if affinity := openAIUserAffinityFromContext(ctx); affinity != nil && affinity.groupID == derefGroupID(groupID) && affinity.sessionHash == sessionHash {
		if affinity.sessionAccountID > 0 {
			return affinity.sessionAccountID, nil
		}
		return affinity.userAccountID, nil
	}
	return s.getStickySessionAccountID(ctx, groupID, sessionHash)
}

func markOpenAIUserAffinityMigration(ctx context.Context, accountID int64) {
	if affinity := openAIUserAffinityFromContext(ctx); affinity != nil && accountID > 0 {
		affinity.migrationFrom = accountID
	}
}

// Keep the old binding until a replacement passes terminal admission. This
// permits a CAS migration without a window where another request can steal it.
func (s *OpenAIGatewayService) clearOpenAIStickySessionForRouting(ctx context.Context, groupID *int64, sessionHash string) error {
	if affinity := openAIUserAffinityFromContext(ctx); affinity != nil && affinity.groupID == derefGroupID(groupID) {
		owner := affinity.sessionAccountID
		if owner <= 0 {
			owner = affinity.userAccountID
		}
		markOpenAIUserAffinityMigration(ctx, owner)
		return nil
	}
	return s.deleteStickySessionAccountID(ctx, groupID, sessionHash)
}

// bindOpenAIUserAffinityAfterAdmission runs only at the handler's post-slot,
// post-profit gate boundary. First writer wins new-user races; migration is
// conditional on the owner observed at selection and never resurrects an old
// owner after another request moved it. Temporary spillover never calls this.
func (s *OpenAIGatewayService) bindOpenAIUserAffinityAfterAdmission(ctx context.Context, groupID *int64, sessionHash string, accountID int64) (bool, error) {
	affinity := openAIUserAffinityFromContext(ctx)
	if affinity == nil {
		return false, nil
	}
	// Composite/fallback selections may route in another group than the
	// ingress API key. The captured selection scope is authoritative.
	if accountID <= 0 || s == nil || s.cache == nil {
		return true, nil
	}
	cache, ok := s.cache.(SessionAccountCompareAndSetter)
	if !ok {
		return false, nil
	}
	// Profit-only ineligibility remains a temporary route. Confirmed quality or
	// hard eligibility failures explicitly mark migration before getting here.
	owner := affinity.sessionAccountID
	if owner <= 0 {
		owner = affinity.userAccountID
	}
	if gatewayProfitControlGateActive(ctx) && owner > 0 && owner != accountID && affinity.migrationFrom != owner {
		return true, nil
	}
	if affinity.sessionHash != "" {
		updated, err := cache.CompareAndSetSessionAccountID(ctx, affinity.groupID, s.openAISessionCacheKey(affinity.sessionHash), affinity.sessionPrimaryAccountID, accountID, s.openAIWSSessionStickyTTL())
		if err != nil {
			return true, err
		}
		if !updated {
			return true, nil
		}
	}
	if affinity.userAccountID == 0 || affinity.userAccountID == accountID || affinity.migrationFrom == affinity.userAccountID {
		updated, err := cache.CompareAndSetSessionAccountID(ctx, affinity.groupID, affinity.key, affinity.userAccountID, accountID, openAIUserAffinityTTL)
		if err != nil {
			return true, err
		}
		if updated && affinity.userAccountID > 0 && affinity.userAccountID != accountID {
			slog.Info("openai_user_affinity_migrated", "group_id", affinity.groupID, "previous_account_id", affinity.userAccountID, "account_id", accountID)
		}
	}
	return true, nil
}
