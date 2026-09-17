//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismDuplicateDoesNotCopySessionOrOptIn(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	source := newPrismTestAccount()
	source.Name = "source"
	source.Type = AccountTypeAPIKey // Disabled settings may remain on older account types.
	source.Extra[PrismExtraKey].(map[string]any)["enabled"] = false
	source.Credentials[PrismCookieConfiguredCredentialKey] = true
	require.NoError(t, repo.Create(context.Background(), source))
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	duplicate, err := svc.DuplicateAccount(context.Background(), source.ID, "", "")
	require.NoError(t, err)
	require.NotContains(t, duplicate.Extra, PrismExtraKey)
	require.NotContains(t, duplicate.Credentials, PrismCookieCredentialKey)
	require.NotContains(t, duplicate.Credentials, PrismCookieConfiguredCredentialKey)
	require.Equal(t, prismTestCookie, source.Credentials[PrismCookieCredentialKey])
	require.Contains(t, source.Extra, PrismExtraKey)
}
