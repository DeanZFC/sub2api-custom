package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountExcelCredentialStateIsSafeAndIncludedInLists(t *testing.T) {
	a := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true,
		Extra: map[string]any{"openai_excel_bps": true, service.ExcelBPSCredentialStateExtraKey: map[string]any{"status": "not_expired", "expires_at": time.Now().Add(-time.Hour), "error_code": "PRIVATE_ERROR", "access_token": "PRIVATE_TOKEN"}}}
	account := AccountFromServiceShallow(a)
	require.Equal(t, "expired", account.ExcelBPSCredentialState.Status)
	require.Empty(t, account.ExcelBPSCredentialState.ErrorCode)
	require.NotContains(t, account.Extra, service.ExcelBPSCredentialStateExtraKey)
	raw, err := json.Marshal(account)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "PRIVATE_")
}
