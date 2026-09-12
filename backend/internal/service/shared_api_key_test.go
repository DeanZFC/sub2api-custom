package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type sharedAPIKeyRepoStub struct {
	items  []SharedAPIKey
	rotKey string
}

func (r *sharedAPIKeyRepoStub) Create(context.Context, *SharedAPIKey) error { return nil }
func (r *sharedAPIKeyRepoStub) ListByUser(context.Context, int64) ([]SharedAPIKey, error) {
	return r.items, nil
}
func (r *sharedAPIKeyRepoStub) GetByID(context.Context, int64, int64) (*SharedAPIKey, error) {
	if len(r.items) == 0 {
		return nil, ErrSharedAPIKeyNotFound
	}
	v := r.items[0]
	return &v, nil
}
func (r *sharedAPIKeyRepoStub) GetByKey(context.Context, string) (*SharedAPIKey, error) {
	return nil, ErrSharedAPIKeyNotFound
}
func (r *sharedAPIKeyRepoStub) Update(context.Context, *SharedAPIKey) error { return nil }
func (r *sharedAPIKeyRepoStub) Delete(context.Context, int64, int64) error  { return nil }
func (r *sharedAPIKeyRepoStub) RotateKey(_ context.Context, _ int64, _ int64, key string) (*SharedAPIKey, error) {
	r.rotKey = key
	return &SharedAPIKey{ID: 1, UserID: 7, Key: key, KeyPreview: previewSharedAPIKey(key)}, nil
}

func TestSharedAPIKeyListIncludesOwnerCredentials(t *testing.T) {
	repo := &sharedAPIKeyRepoStub{items: []SharedAPIKey{{ID: 1, UserID: 7, Key: "sk-shared-secret"}}}
	items, err := NewSharedAPIKeyService(repo).List(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "sk-shared-secret", items[0].Key)
}

func TestSharedAPIKeyRotateGeneratesFreshCredential(t *testing.T) {
	repo := &sharedAPIKeyRepoStub{}
	item, err := NewSharedAPIKeyService(repo).Rotate(context.Background(), 7, 1)
	require.NoError(t, err)
	require.Equal(t, repo.rotKey, item.Key)
	require.True(t, len(item.Key) > len("sk-shared-"))
	require.NotEqual(t, "", item.KeyPreview)
}
