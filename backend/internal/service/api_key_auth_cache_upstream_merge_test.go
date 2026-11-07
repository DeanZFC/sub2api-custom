//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSnapshotUpstreamFieldsPreserveCustomIsolation(t *testing.T) {
	override := 42
	svc := &APIKeyService{userGroupRateRepo: &rpmOverrideRepoStub{override: &override}}
	key := profitAuthTestAPIKey()
	key.Group.RequirePrivacySet = true
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)

	// A repository-owned pointer must not mutate a cached authentication result.
	override = 99
	require.Equal(t, 42, *snapshot.User.UserGroupRPMOverride)
	require.Equal(t, *key.GroupID, snapshot.User.UserGroupRPMOverrideGroupID)

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	var entry APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &entry))
	first, used, err := svc.applyAuthCacheEntry("key", &entry)
	require.NoError(t, err)
	require.True(t, used)
	second, used, err := svc.applyAuthCacheEntry("key", &entry)
	require.NoError(t, err)
	require.True(t, used)
	*first.User.UserGroupRPMOverride = 7
	require.Equal(t, 42, *second.User.UserGroupRPMOverride, "requests must not share mutable override pointers")
	require.Equal(t, 42, *entry.Snapshot.User.UserGroupRPMOverride)
	require.Equal(t, *key.GroupID, second.User.UserGroupRPMOverrideGroupID)
	require.True(t, second.Group.RequirePrivacySet, "the upstream privacy gate must survive L2 serialization")
	require.True(t, second.Group.ProfitControlEnabled, "the custom profit gate must remain intact")
	require.Equal(t, key.Group.ProfitMinMargin, second.Group.ProfitMinMargin)
}

func TestAPIKeyAuthSnapshotRejectsPreRPMGroupCache(t *testing.T) {
	svc := &APIKeyService{}
	snapshot := svc.snapshotFromAPIKey(context.Background(), profitAuthTestAPIKey())
	snapshot.Version = 30
	key, used, err := svc.applyAuthCacheEntry("key", &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.False(t, used, "old custom snapshots lack group-scoped RPM lookup state")
	require.Nil(t, key)
}
