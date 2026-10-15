//go:build integration

package qqbot

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActivityAndBasicConfigSavesPreserveEachOther(t *testing.T) {
	ctx := context.Background()
	s := savedBot(t, testDB(t))
	original, err := s.Config(ctx)
	require.NoError(t, err)
	// An already-open basic form contains invitation settings from before the activity edit.
	staleBasic := original.Public()
	invite := InviteConfig{InviteReward: 2.5, InviteDailyLimit: 8,
		RechargeRequirement: RechargeRequirement{MinRechargeTotal: 20, MinRecharge30d: 5}}
	require.NoError(t, s.SaveInviteConfig(ctx, invite))
	staleBasic.ChatModel = "updated-model"
	_, err = s.SaveBasicConfig(ctx, staleBasic)
	require.NoError(t, err)
	gotInvite, err := s.InviteConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, invite, gotInvite)
	invite.InviteReward = .12345678
	require.NoError(t, s.SaveInviteConfig(ctx, invite))
	got, err := s.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, "updated-model", got.ChatModel)
	require.Equal(t, invite.InviteReward, got.InviteReward)
	require.Equal(t, invite.MinRechargeTotal, got.InviteMinRechargeTotal)
	require.Equal(t, invite.MinRecharge30d, got.InviteMinRecharge30d)
	require.Equal(t, original.AppSecret, got.AppSecret)
	require.Equal(t, original.APIKey, got.APIKey)
	require.Equal(t, original.AllowedGroups, got.AllowedGroups)
	require.Equal(t, original.CheckinReward, got.CheckinReward)

	// Both writers must merge after acquiring the shared lock, regardless of save order.
	for i := 1; i <= 5; i++ {
		basic := got.Public()
		basic.ChatModel = fmt.Sprintf("concurrent-model-%d", i)
		nextInvite := InviteConfig{InviteReward: float64(i), InviteDailyLimit: i,
			RechargeRequirement: RechargeRequirement{MinRechargeTotal: float64(i), MinRecharge30d: float64(i) + .5}}
		errors := make(chan error, 2)
		go func() { _, err := s.SaveBasicConfig(ctx, basic); errors <- err }()
		go func() { errors <- s.SaveInviteConfig(ctx, nextInvite) }()
		require.NoError(t, <-errors)
		require.NoError(t, <-errors)
		got, err = s.Config(ctx)
		require.NoError(t, err)
		require.Equal(t, basic.ChatModel, got.ChatModel)
		require.Equal(t, nextInvite.InviteReward, got.InviteReward)
		require.Equal(t, nextInvite.InviteDailyLimit, got.InviteDailyLimit)
		require.Equal(t, nextInvite.MinRechargeTotal, got.InviteMinRechargeTotal)
		require.Equal(t, nextInvite.MinRecharge30d, got.InviteMinRecharge30d)
		require.Equal(t, original.AppSecret, got.AppSecret)
		require.Equal(t, original.APIKey, got.APIKey)
	}
}
