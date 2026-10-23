//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type secretRevocationRepo struct {
	*authRepoStub
	matches bool
	err     error
}

func (r *secretRevocationRepo) RotateSecret(context.Context, int64, int64, string, string) error {
	return nil
}
func (r *secretRevocationRepo) CredentialMatches(context.Context, int64, string) (bool, error) {
	return r.matches, r.err
}

func TestAPIKeySecretDigestAndMask(t *testing.T) {
	raw := "sk-secret-1234567890-abcdef"
	sum := sha256.Sum256([]byte(raw))
	digest := hex.EncodeToString(sum[:])
	stored := APIKeyStoredValue(raw)
	require.Equal(t, "$sha256$"+digest+"$sk-sec...cdef", stored)
	require.Equal(t, digest, APIKeyCacheDigest(stored))
	require.Equal(t, digest, APIKeyCacheDigest(raw))
	require.Equal(t, "sk-sec...cdef", MaskAPIKey(stored))
	require.Equal(t, "***", MaskAPIKey("short"))
}

func TestAPIKeySecretStaleCacheCannotAuthenticateAfterRevocation(t *testing.T) {
	ctx := context.Background()
	raw := "sk-revoked-1234567890"
	repo := &secretRevocationRepo{authRepoStub: &authRepoStub{}, matches: true}
	cfg := &config.Config{}
	cfg.APIKeyAuth.L2TTLSeconds = 120
	cache := &authCacheStub{}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	snapshot := svc.snapshotFromAPIKey(ctx, &APIKey{ID: 7, UserID: 3, Key: raw, Status: StatusActive, User: &User{ID: 3, Status: StatusActive}})
	cache.getAuthCache = func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
		return &APIKeyAuthCacheEntry{Snapshot: snapshot}, nil
	}
	_, err := svc.GetByKey(ctx, raw)
	require.NoError(t, err)
	repo.matches = false
	_, err = svc.GetByKey(ctx, raw)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	repo.err = errors.New("database unavailable")
	_, err = svc.GetByKey(ctx, raw)
	require.ErrorIs(t, err, repo.err, "database errors must fail closed")
	_, err = svc.GetByKey(ctx, APIKeyStoredValue(raw))
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}
