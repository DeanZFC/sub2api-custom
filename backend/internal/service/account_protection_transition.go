package service

import (
	"context"
	"maps"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrProtectionConflict = infraerrors.Conflict("PROTECTION_CONFLICT", "账号配置已变化，请刷新后重试")
var ErrProtectedProxyModeChange = infraerrors.BadRequest("PROTECTION_PROXY_CONFLICT", "身份保护账号必须使用固定代理或直连，请先关闭保护再配置随机或多个代理")

type protectionManagedWriteKey struct{}
type protectionWriteExpectationKey struct{}
type ProtectionWriteExpectation struct {
	AccountID int64
	UpdatedAt time.Time
}

func ProtectionManagedWrite(ctx context.Context) bool {
	return ctx != nil && ctx.Value(protectionManagedWriteKey{}) == true
}

func withProtectionManagedWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, protectionManagedWriteKey{}, true)
}

func withProtectionWriteExpectation(ctx context.Context, account *Account) context.Context {
	ctx = withProtectionManagedWrite(ctx)
	return context.WithValue(ctx, protectionWriteExpectationKey{}, ProtectionWriteExpectation{account.ID, account.UpdatedAt})
}

func GetProtectionWriteExpectation(ctx context.Context) (ProtectionWriteExpectation, bool) {
	if ctx == nil {
		return ProtectionWriteExpectation{}, false
	}
	v, ok := ctx.Value(protectionWriteExpectationKey{}).(ProtectionWriteExpectation)
	return v, ok
}

func RegisteredProtectionModes() []string {
	var modes []string
	for _, profile := range ListAntiDegradeStrategyProfiles() {
		if profile.ApplySupported {
			modes = append(modes, string(profile.ID))
		}
	}
	return modes
}

func ProtectedProxyModeConflict(current *Account, incoming map[string]any) bool {
	if current == nil || !current.IdentityProtectionEnabled() {
		return false
	}
	mode, _ := incoming["proxy_mode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "random")
}

func ProtectedProxyPoolConflict(current *Account, proxyIDs []int64) bool {
	return current != nil && current.IdentityProtectionEnabled() && len(proxyIDs) > 1
}

// The editable account field remains the ceiling. Mirror it into the marker
// so a later lower limit is used immediately and diagnostics never show stale
// admission settings. Do not enable or change adaptive concurrency here.
func BoundAccountProtectionConcurrency(a *Account) {
	if a == nil || !a.AntiDegradationEnabled() {
		return
	}
	if a.Concurrency <= 0 {
		a.Concurrency = AntiDegradeConcurrencyCap
	}
	if marker := mode1Marker(a); marker != nil {
		a.Extra = maps.Clone(a.Extra)
		copy := maps.Clone(marker)
		copy["max_concurrency"] = a.Concurrency
		a.Extra[AntiDegradeMarkerExtraKey] = copy
	}
}

func ValidateAccountProtectionConfiguration(a *Account) error {
	if a == nil {
		return nil
	}
	if err := validateRequestIntegrityExtra(a.Extra); err != nil {
		return err
	}
	return validateRegisteredAntiDegrade(a)
}
