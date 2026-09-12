package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type sharedUploadLimitListings struct {
	sharedUploadListings
	active, recent int
}

type sharedUploadLockListings struct {
	sharedUploadLimitListings
	locks int
}

func (r *sharedUploadLockListings) WithOwnerPublishLock(_ context.Context, _ int64, fn func() error) error {
	r.locks++
	return fn()
}

func (r *sharedUploadLimitListings) IsUserSharedPublishAllowed(context.Context, int64) (bool, error) {
	return true, nil
}

func TestSharedAccountUploadSerializesQuotaAndCreation(t *testing.T) {
	listings := &sharedUploadLockListings{}
	svc := NewSharedAccountUploadService(&sharedUploadAccounts{}, listings, nil, nil)
	_, err := svc.Upload(context.Background(), 42, SharedAccountUploadInput{
		Name: "locked", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, listings.locks)
}
func (r *sharedUploadLimitListings) CountOwnerListings(context.Context, int64) (int, int, error) {
	return r.active, r.recent, nil
}

func TestSharedAccountUploadEnforcesOwnerLimits(t *testing.T) {
	for name, tc := range map[string]struct {
		active, recent int
		reason         string
	}{
		"account cap": {active: 50, reason: "SHARED_ACCOUNT_LIMIT"},
		"hourly cap":  {recent: 10, reason: "SHARED_UPLOAD_RATE_LIMIT"},
	} {
		t.Run(name, func(t *testing.T) {
			listings := &sharedUploadLimitListings{active: tc.active, recent: tc.recent}
			svc := NewSharedAccountUploadService(&sharedUploadAccounts{}, listings, nil, nil)
			_, err := svc.Upload(context.Background(), 42, SharedAccountUploadInput{Name: "x", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}
