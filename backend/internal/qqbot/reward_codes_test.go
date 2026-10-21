package qqbot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRewardCodeEntropyNormalizationAndRedaction(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		code, err := generateRewardCode()
		require.NoError(t, err)
		require.Len(t, strings.ReplaceAll(code, "-", ""), 32)
		hash := rewardCodeHash(code)
		require.Len(t, hash, 64)
		require.False(t, seen[hash])
		seen[hash] = true
		require.Equal(t, hash, rewardCodeHash("  "+strings.ToLower(code)+"  "))
		require.Equal(t, hash, rewardCodeHash(strings.ReplaceAll(code, "-", "")))
	}
	for _, raw := range []string{"", "1234", "00000000000000000000000000000000", strings.Repeat("A", 65)} {
		require.Equal(t, "invalid", rewardCodeHash(raw))
	}
	code, err := generateRewardCode()
	require.NoError(t, err)
	e := Event{ID: "m", GroupID: "g", Content: "<@bot> /福利 兑换 " + code, MsgElements: []MessageElement{{Content: code}}, Attachments: []Attachment{{URL: code}}, MessageScene: MessageScene{Ext: []string{code}}}
	e.Author.MemberID = "member"
	clean, hash := redactRewardCommand(e)
	data, err := json.Marshal(clean)
	require.NoError(t, err)
	require.NotContains(t, string(data), code)
	require.Equal(t, rewardCodeHash(code), hash)
	require.Equal(t, []string{"bot"}, groupMentionIDs(clean))
	require.True(t, isBuiltinOrCustomCommand(clean, nil))
	require.Error(t, validateCustomCommands([]CustomCommand{{Command: "/福利 兑换", Reply: "override"}}))
}

func TestRewardCodeValidation(t *testing.T) {
	base := RewardCodeCreate{BatchName: "launch", RewardType: "balance", Count: 1, Amount: 10}
	require.NoError(t, base.Validate())
	for _, edit := range []func(*RewardCodeCreate){
		func(r *RewardCodeCreate) { r.Count = 201 }, func(r *RewardCodeCreate) { r.Count = 0 },
		func(r *RewardCodeCreate) { r.Amount = 0.001 }, func(r *RewardCodeCreate) { r.Amount = -1 },
		func(r *RewardCodeCreate) { r.Quantity = 1 }, func(r *RewardCodeCreate) { r.RewardType = "admin_balance" },
		func(r *RewardCodeCreate) { past := time.Now().Add(-time.Second); r.ExpiresAt = &past },
		func(r *RewardCodeCreate) { r.BatchName = " " },
	} {
		r := base
		edit(&r)
		require.ErrorIs(t, r.Validate(), ErrRewardCodeRequest)
	}
	for _, kind := range []string{"chances", "physical", "other"} {
		r := RewardCodeCreate{BatchName: "prize", RewardType: kind, Count: 1, Quantity: 3, PrizeName: "cup"}
		require.NoError(t, r.Validate())
		r.Quantity = 1001
		require.Error(t, r.Validate())
	}
}
