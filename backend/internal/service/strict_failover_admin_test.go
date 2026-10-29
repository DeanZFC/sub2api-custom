//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStrictFailoverAdminUpdate(t *testing.T) {
	for _, mode := range []string{"enable", "preserve", "disable", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			g, accounts := strictFixture()
			repo := &groupRepoStubForAdmin{getByID: g}
			svc := &adminServiceImpl{groupRepo: repo, accountRepo: accounts}
			original := CloneStrictFailoverConfig(g.StrictFailover)
			input := &UpdateGroupInput{}
			switch mode {
			case "enable":
				input.StrictFailover = &StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{2}, {1, 3}}}
			case "disable":
				input.StrictFailover = &StrictFailoverConfig{Enabled: false, Tiers: [][]int64{{999}}}
			case "invalid":
				input.StrictFailover = &StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{999}}}
			}
			got, err := svc.UpdateGroup(context.Background(), g.ID, input)
			if mode == "invalid" {
				require.Error(t, err)
				require.Nil(t, repo.updated)
				require.Equal(t, original, g.StrictFailover)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, repo.updated)
			if input.StrictFailover == nil {
				require.Equal(t, original, got.StrictFailover)
			} else {
				require.Equal(t, *input.StrictFailover, got.StrictFailover)
				input.StrictFailover.Tiers[0][0] = 888
				require.NotEqual(t, int64(888), got.StrictFailover.Tiers[0][0])
			}
		})
	}
}

func TestStrictFailoverDuplicateStartsDisabled(t *testing.T) {
	g, _ := strictFixture()
	duplicate := cloneGroupForDuplicate(g, "strict-copy")
	require.False(t, duplicate.StrictFailover.Enabled)
	require.Empty(t, duplicate.StrictFailover.Tiers)
	require.True(t, g.StrictFailover.Enabled)
}
