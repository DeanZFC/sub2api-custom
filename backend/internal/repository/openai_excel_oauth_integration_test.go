//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func excelAuthorizationFixture(t *testing.T) (context.Context, *openAIOAuthReauthRepository, *service.Account) {
	t.Helper()
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: fmt.Sprintf("excel-auth-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "synthetic-codex", "chatgpt_account_id": "workspace-a", "email": "fixture@example.test", "plan_type": "plus"},
		Extra:       map[string]any{"openai_excel_bps": true, service.ExcelBPSAuthorizationPendingKey: true},
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	repo := &openAIOAuthReauthRepository{db: integrationDB}
	err := repo.UpsertConfig(ctx, &service.OpenAIOAuthReauthStoredConfig{AccountID: account.ID, LoginEmail: "fixture@example.test", CredentialMode: "password_totp", Engine: "local_worker", ProxySource: "account", PasswordCiphertext: "synthetic-encrypted-login"})
	require.NoError(t, err)
	return ctx, repo, account
}

func TestExcelAuthorizationGrantTransactionPreservesPrimaryAccount(t *testing.T) {
	ctx, repo, account := excelAuthorizationFixture(t)
	accounts := NewAccountRepository(integrationEntClient, integrationDB, nil)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET status='error',schedulable=false WHERE id=$1`, account.ID)
	require.NoError(t, err)
	before, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	task, err := repo.CreateExcelTask(ctx, account.ID, "snapshot")
	require.NoError(t, err)
	_, err = repo.CreateExcelTask(ctx, account.ID, "duplicate")
	require.Error(t, err)
	claimed, err := repo.ClaimNextTaskForEngines(ctx, "excel-fixture-worker", time.Minute, "password_totp", []string{"local_worker", "session_studio"})
	require.NoError(t, err)
	require.Equal(t, task.ID, claimed.ID)
	claimed, started, err := repo.BeginDirectCallback(ctx, task.ID, "excel-fixture-worker")
	require.NoError(t, err)
	require.True(t, started)
	changed, err := repo.ApplyExcelCredentials(ctx, claimed, map[string]any{"access_token": "stale"}, "encrypted-excel")
	require.NoError(t, err)
	require.False(t, changed)
	ciphertext, err := repo.GetExcelCredentials(ctx, account.ID)
	require.NoError(t, err)
	require.Empty(t, ciphertext)
	changed, err = repo.ApplyExcelCredentials(ctx, claimed, before.Credentials, "encrypted-excel", service.ExcelBPSGrantState(time.Now().Add(time.Hour).Unix()))
	require.NoError(t, err)
	require.True(t, changed)
	after, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, before.Credentials, after.Credentials)
	require.Equal(t, before.Schedulable, after.Schedulable)
	require.Equal(t, before.Status, after.Status)
	require.True(t, after.IsExcelBPSEnabled())
	require.NotEqual(t, true, after.Extra[service.ExcelBPSAuthorizationPendingKey])
	completed, err := repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", completed.Status)
	changed, err = repo.ReplaceExcelCredentials(ctx, account.ID, "encrypted-excel", "encrypted-newer")
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repo.DeleteExcelCredentials(ctx, account.ID, "encrypted-excel"))
	ciphertext, err = repo.GetExcelCredentials(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "encrypted-newer", ciphertext)
}

func TestExcelAuthorizationBulkPendingIsPerAccount(t *testing.T) {
	ctx, repo, missing := excelAuthorizationFixture(t)
	_, _, ready := excelAuthorizationFixture(t)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO openai_excel_oauth_credentials(account_id,credentials_ciphertext) VALUES($1,'synthetic-encrypted-grant')`, ready.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||'{"openai_excel_bps_credential_state":{"status":"not_expired"}}'::jsonb WHERE id=$1`, ready.ID)
	require.NoError(t, err)
	accounts := NewAccountRepository(integrationEntClient, integrationDB, nil)
	_, err = accounts.BulkUpdate(ctx, []int64{missing.ID, ready.ID}, service.AccountBulkUpdate{Extra: map[string]any{"openai_excel_bps": true, "unrelated": "keep", service.ExcelBPSAuthorizationPendingKey: false}})
	require.NoError(t, err)
	missingAfter, err := accounts.GetByID(ctx, missing.ID)
	require.NoError(t, err)
	readyAfter, err := accounts.GetByID(ctx, ready.ID)
	require.NoError(t, err)
	require.Equal(t, true, missingAfter.Extra[service.ExcelBPSAuthorizationPendingKey])
	require.False(t, missingAfter.IsExcelBPSEnabled())
	require.NotEqual(t, true, readyAfter.Extra[service.ExcelBPSAuthorizationPendingKey])
	require.True(t, readyAfter.IsExcelBPSEnabled())
	// Simulate an edit prepared before the grant completed. Full Update preserves
	// server diagnosis and must not reintroduce its old preparation marker.
	readyAfter.Extra[service.ExcelBPSCredentialStateExtraKey] = map[string]any{"status": "forged"}
	readyAfter.Extra[service.ExcelBPSAuthorizationPendingKey] = true
	require.NoError(t, accounts.Update(ctx, readyAfter))
	readyAfter, err = accounts.GetByID(ctx, ready.ID)
	require.NoError(t, err)
	require.True(t, readyAfter.IsExcelBPSEnabled())
	require.Equal(t, "not_expired", readyAfter.Extra[service.ExcelBPSCredentialStateExtraKey].(map[string]any)["status"])

	require.Equal(t, "keep", missingAfter.Extra["unrelated"])
	require.Equal(t, "keep", readyAfter.Extra["unrelated"])
	ids, err := repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.Contains(t, ids, missing.ID)
	require.NotContains(t, ids, ready.ID)
	require.NoError(t, accounts.UpdateExtra(ctx, ready.ID, map[string]any{"openai_excel_bps": true, service.ExcelBPSAuthorizationPendingKey: true}))
	readyAfter, err = accounts.GetByID(ctx, ready.ID)
	require.NoError(t, err)
	require.True(t, readyAfter.IsExcelBPSEnabled())
	task, err := repo.CreateExcelTask(ctx, missing.ID, "snapshot")
	require.NoError(t, err)
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, ids, missing.ID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET status='failed',stage='failed' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, ids, missing.ID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET updated_at=NOW()-INTERVAL '31 minutes' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.Contains(t, ids, missing.ID)
}

func TestExcelAuthorizationCompletionHonorsDisabledSwitch(t *testing.T) {
	ctx, repo, account := excelAuthorizationFixture(t)
	task, err := repo.CreateExcelTask(ctx, account.ID, "snapshot")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET status='callback_processing',worker_id='fixture' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	task.WorkerID = "fixture"
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||'{"openai_excel_bps":false}'::jsonb WHERE id=$1`, account.ID)
	require.NoError(t, err)
	changed, err := repo.ApplyExcelCredentials(ctx, task, account.Credentials, "synthetic-encrypted-grant")
	require.NoError(t, err)
	require.True(t, changed)
	accounts := NewAccountRepository(integrationEntClient, integrationDB, nil)
	updated, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.False(t, updated.IsExcelBPSEnabled())
	require.Equal(t, false, updated.Extra["openai_excel_bps"])
	require.NoError(t, repo.PrepareMissingExcelRoutes(ctx))
	updated, err = accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.False(t, updated.IsExcelBPSEnabled())
}

func TestExcelAuthorizationMigrationIsIdempotent(t *testing.T) {
	_ = testEntClient(t)
	raw, err := os.ReadFile("../../migrations/338_excel_oauth_authorization.sql")
	require.NoError(t, err)
	tx, err := integrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(context.Background(), string(raw))
		require.NoError(t, err)
	}
}
