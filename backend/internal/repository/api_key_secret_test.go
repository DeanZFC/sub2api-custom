package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeySecretStorageRotationAndOwnership(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "secret-owner@test.com")
	const raw = "sk-original-credential-1234567890"
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	key := &service.APIKey{UserID: owner.ID, Key: raw, Name: "private", Status: service.StatusActive, Quota: 40, QuotaUsed: 12, RateLimit5h: 10, IPWhitelist: []string{"127.0.0.1"}, ExpiresAt: &expires}
	require.NoError(t, repo.Create(ctx, key))
	require.Equal(t, raw, key.Key)
	stored, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyStoredValue(raw), stored.Key)
	require.NotContains(t, stored.Key, raw)
	_, err = repo.GetByKeyForAuth(ctx, raw)
	require.NoError(t, err)
	_, err = repo.GetByKeyForAuth(ctx, stored.Key)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	_, err = svc.Regenerate(ctx, key.ID, owner.ID+1)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	_, err = svc.GetForOwnerAuth(ctx, key.ID, owner.ID+1)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	selected, err := svc.GetForOwnerAuth(ctx, key.ID, owner.ID)
	require.NoError(t, err)
	require.Empty(t, selected.Key)
	rotated, err := svc.Regenerate(ctx, key.ID, owner.ID)
	require.NoError(t, err)
	require.NotEqual(t, raw, rotated.Key)
	after, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyStoredValue(rotated.Key), after.Key)
	require.Equal(t, stored.Name, after.Name)
	require.Equal(t, stored.Status, after.Status)
	require.Equal(t, stored.QuotaUsed, after.QuotaUsed)
	require.Equal(t, stored.Quota, after.Quota)
	require.Equal(t, stored.RateLimit5h, after.RateLimit5h)
	require.Equal(t, stored.IPWhitelist, after.IPWhitelist)
	require.Equal(t, stored.ExpiresAt, after.ExpiresAt)
	require.Equal(t, stored.CreatedAt, after.CreatedAt)
	_, err = svc.GetByKey(ctx, raw)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	_, err = svc.GetByKey(ctx, rotated.Key)
	require.NoError(t, err)
	require.Error(t, repo.RotateSecret(ctx, key.ID, owner.ID, stored.Key, "sk-concurrent-rotation"))
	require.NoError(t, repo.Delete(ctx, key.ID))
	matched, err := repo.CredentialMatches(ctx, key.ID, rotated.Key)
	require.NoError(t, err)
	require.False(t, matched)
}

func TestAPIKeySearchDoesNotExposeDigest(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "key-search@test.com")
	raw := "sk-mask-only-search-8675309"
	key := &service.APIKey{UserID: owner.ID, Key: raw, Name: "safe name", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))
	params := pagination.PaginationParams{Page: 1, PageSize: 20}
	for _, tc := range []struct {
		query string
		count int
	}{
		{"safe name", 1}, {"sk-mas", 1}, {"5309", 1}, {service.APIKeyCacheDigest(raw), 0}, {"%", 0}, {"mask-only-search", 0},
	} {
		rows, _, err := repo.ListByUserID(ctx, owner.ID, params, service.APIKeyListFilters{Search: tc.query})
		require.NoError(t, err)
		require.Len(t, rows, tc.count, tc.query)
	}
}
