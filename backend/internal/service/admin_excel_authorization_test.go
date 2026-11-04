//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAdminExcelAuthorizationDerivesPendingAfterSanitization(t *testing.T) {
	for _, method := range []string{"update", "extra", "bulk"} {
		t.Run(method, func(t *testing.T) {
			reauth, reader, _, _, _ := newExcelReauthTestService(t)
			account := reader.account
			account.Extra = map[string]any{"unrelated": "keep"}
			repo := &adminExcelBPSGroupRepo{accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}, getByIDsAccounts: []*Account{account}}}
			admin := &adminServiceImpl{accountRepo: repo, excelOAuthReauth: reauth}
			input := map[string]any{"openai_excel_bps": true, ExcelBPSAuthorizationPendingKey: false, ExcelBPSCredentialStateExtraKey: map[string]any{"status": "not_expired"}}
			var err error
			switch method {
			case "update":
				_, err = admin.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Extra: input})
			case "extra":
				err = admin.UpdateAccountExtra(context.Background(), account.ID, input)
			case "bulk":
				_, err = admin.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: input})
			}
			require.NoError(t, err)
			var saved map[string]any
			switch method {
			case "update":
				saved = repo.updatedAccounts[0].Extra
				require.Equal(t, true, saved[ExcelBPSAuthorizationPendingKey])
			case "extra":
				saved = repo.extraUpdates[0]
				require.Equal(t, true, saved[ExcelBPSAuthorizationPendingKey])
			case "bulk":
				saved = repo.lastBulkUpdate.Extra
				require.NotContains(t, saved, ExcelBPSAuthorizationPendingKey, "bulk repository computes each row from its own grant")
			}
			require.NotContains(t, saved, ExcelBPSCredentialStateExtraKey)
		})
	}
}
