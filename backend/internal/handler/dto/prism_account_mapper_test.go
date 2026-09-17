package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrismAccountMapperRedactsCookieAndComputesConfiguredFlag(t *testing.T) {
	for _, cookie := range []string{"", "prism_oai_access_token=private-access; prism_session_token=private-session"} {
		account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Credentials: map[string]any{service.PrismCookieCredentialKey: cookie, service.PrismCookieConfiguredCredentialKey: cookie == ""}}
		mapped := AccountFromServiceShallow(account)
		require.Equal(t, cookie != "", mapped.Credentials[service.PrismCookieConfiguredCredentialKey])
		require.NotContains(t, mapped.Credentials, service.PrismCookieCredentialKey)
		payload, err := json.Marshal(mapped)
		require.NoError(t, err)
		require.NotContains(t, string(payload), "private-access")
		require.NotContains(t, string(payload), "private-session")
		require.Equal(t, cookie, account.Credentials[service.PrismCookieCredentialKey])
	}
	account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken}
	require.Equal(t, false, AccountFromServiceShallow(account).Credentials[service.PrismCookieConfiguredCredentialKey])
}
