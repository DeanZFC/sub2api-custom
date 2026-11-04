package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestExcelAuthorizationConfigKeepsSecretsPrivateAndExplicitlyClears(t *testing.T) {
	svc, _, repo, _, _ := newExcelReauthTestService(t)
	ctx := context.Background()
	beforePassword, beforeTOTP := repo.config.PasswordCiphertext, repo.config.TOTPSecretCiphertext
	view, err := svc.SaveCredentialConfig(ctx, 42, OpenAIOAuthReauthConfigInput{LoginEmail: "user@example.com"})
	require.NoError(t, err)
	require.True(t, view.Configured)
	require.Equal(t, beforePassword, repo.config.PasswordCiphertext)
	require.Equal(t, beforeTOTP, repo.config.TOTPSecretCiphertext)
	safe, err := svc.GetStatus(ctx, 42)
	require.NoError(t, err)
	raw, err := json.Marshal(safe)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "password-secret")
	require.NotContains(t, string(raw), "totp-secret")
	require.NotContains(t, string(raw), "encrypted:")
	view, err = svc.SaveCredentialConfig(ctx, 42, OpenAIOAuthReauthConfigInput{LoginEmail: "user@example.com", ClearPassword: true, ClearTOTP: true})
	require.NoError(t, err)
	require.False(t, view.Configured)
	require.Empty(t, repo.config.PasswordCiphertext)
	require.Empty(t, repo.config.TOTPSecretCiphertext)
	_, err = svc.CreateTaskForProfile(ctx, 42, "excel")
	require.Error(t, err)
}

func TestExcelAuthorizationAcceptsPinnedWorkerCapabilitiesOnlyLocalTasks(t *testing.T) {
	svc, _, _, _, _ := newExcelReauthTestService(t)
	_, err := svc.SaveCredentialConfig(context.Background(), 42, OpenAIOAuthReauthConfigInput{LoginEmail: "user@example.com", Engine: "session_studio"})
	require.Error(t, err)
	_, err = svc.CreateTaskForProfile(context.Background(), 42, "excel")
	require.NoError(t, err)
	claim, err := svc.ClaimTaskWithEngines(context.Background(), "worker", []string{"local_worker", "session_studio"})
	require.NoError(t, err)
	require.Equal(t, "excel", claim.OAuthProfile)
	require.Equal(t, "local_worker", claim.Engine)
}

func TestExcelManualOAuthUsesAccessWorkspaceNotSessionID(t *testing.T) {
	creds := excelTestCredentials("manual", time.Now().Add(time.Hour))
	info := &OpenAITokenInfo{AccessToken: reauthMapString(creds, "access_token"), IDToken: reauthMapString(creds, "id_token"), ClientID: openai.ExcelClientID, ChatGPTAccountID: "wrong-session-id"}
	require.NoError(t, applyExcelAccessIdentity(info))
	require.Equal(t, "acct-1", info.ChatGPTAccountID)
	require.Equal(t, "user-1", info.ChatGPTUserID)
	info.AccessToken = reauthTestJWT(map[string]any{"client_id": openai.ClientID, "exp": time.Now().Add(time.Hour).Unix()})
	require.Error(t, applyExcelAccessIdentity(info))
}

func TestExcelAuthorizationEligibilityAndPendingRoute(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}, Extra: map[string]any{"openai_excel_bps": true}}
	require.True(t, account.IsExcelBPSEnabled())
	account.Extra[ExcelBPSAuthorizationPendingKey] = true
	require.False(t, account.IsExcelBPSEnabled())
	delete(account.Extra, ExcelBPSAuthorizationPendingKey)
	account.Credentials["plan_type"] = " free "
	require.False(t, account.IsExcelBPSEnabled())
	clean := SanitizeExcelBPSAuthorizationExtra(map[string]any{ExcelBPSAuthorizationPendingKey: false, ExcelBPSCredentialStateExtraKey: "forged", "unrelated": true})
	require.Equal(t, map[string]any{"unrelated": true}, clean)
}
