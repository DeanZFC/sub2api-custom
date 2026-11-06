package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// CheckGroupAntiDistillation uses the effective customer billing group and
// never changes the upstream account state or routes to a more expensive group.
func (s *APIKeyService) CheckGroupAntiDistillation(ctx context.Context, key *APIKey) (time.Duration, error) {
	if s == nil || s.groupAntiDistillation == nil || key == nil || key.Group == nil || key.Group.Platform != PlatformOpenAI {
		return 0, nil
	}
	return s.groupAntiDistillation.Check(ctx, key.UserID, key.Group.ID)
}

// WithGroupAntiDistillationAdmission rechecks a pause immediately before forwarding.
type groupProtectionAdmissionKey struct{}

func WithGroupAntiDistillationAdmission(ctx context.Context, check func(context.Context) (time.Duration, error)) context.Context {
	return context.WithValue(ctx, groupProtectionAdmissionKey{}, check)
}
func CheckGroupAntiDistillationAdmission(ctx context.Context) error {
	check, _ := ctx.Value(groupProtectionAdmissionKey{}).(func(context.Context) (time.Duration, error))
	if check == nil {
		return nil
	}
	remaining, err := check(ctx)
	if err == nil && remaining > 0 {
		return infraerrors.New(429, "group_anti_distillation_paused", "零缓存保护已暂停此分组的新请求，请在暂停结束后重试")
	}
	return nil
}
