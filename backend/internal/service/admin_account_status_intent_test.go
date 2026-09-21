package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminAccountUpdateMarksOnlyExplicitStatusChanges(t *testing.T) {
	for _, status := range []string{"", StatusActive, StatusDisabled} {
		t.Run("status="+status, func(t *testing.T) {
			repo := &accountBillingSettingsAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{
				accounts: map[int64]*Account{41: {ID: 41, Name: "test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusQualityPaused, Schedulable: true}},
			}}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.UpdateAccount(context.Background(), 41, &UpdateAccountInput{Name: "edited", Status: status})
			require.NoError(t, err)
			require.Equal(t, status != "", repo.accounts[41].StatusChanged)
			if status == "" {
				require.Equal(t, StatusQualityPaused, repo.accounts[41].Status)
			} else {
				require.Equal(t, status, repo.accounts[41].Status)
			}
			encoded, err := json.Marshal(repo.accounts[41])
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "StatusChanged", "admin intent must never survive a serialized scheduler snapshot")
		})
	}
}
