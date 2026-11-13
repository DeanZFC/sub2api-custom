//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestOVHIPv6EligibilityPreservesSeparateTransports(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account Account
		allowed bool
	}{
		{"native", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, true},
		{"api key", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false},
		{"other platform", Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, false},
		{"excel oauth", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ExcelClientID}}, false},
		{"independent bps", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_excel_bps": true}}, false},
		{"agent", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModeAgentIdentity}}, false},
		{"legacy ipv6", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_ipv6_bound": true}}, false},
		{"retired vm", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_vm_retired": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOVHIPv6Eligibility(&tc.account)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestManagedIPv6AccountEditsPreserveOrdinaryAndUnchangedRoutes(t *testing.T) {
	ctx := context.Background()
	id := int64(12)
	require.NoError(t, (&adminServiceImpl{}).validateManagedIPv6AccountChange(ctx, &Account{}, &UpdateAccountInput{ProxyID: &id, Name: "ordinary"}))
	p := &Proxy{ID: id, Username: "ovh6_1", Status: StatusActive}
	repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: p}
	svc := &adminServiceImpl{proxyRepo: repo}
	a := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, Proxy: p, Status: StatusActive}
	require.NoError(t, svc.validateManagedIPv6AccountChange(ctx, a, &UpdateAccountInput{ProxyID: &id, Name: "rename"}))
	require.Error(t, svc.validateManagedIPv6AccountChange(ctx, a, &UpdateAccountInput{Extra: map[string]any{"openai_excel_bps": true}}))
	next := int64(13)
	require.Error(t, svc.validateManagedIPv6AccountChange(ctx, a, &UpdateAccountInput{ProxyID: &next, Status: StatusActive}))
	pool := []int64{id, next}
	require.Error(t, svc.validateManagedIPv6AccountChange(ctx, a, &UpdateAccountInput{ProxyIDs: &pool}))
}

func TestManagedIPv6CreationCannotCopyCredentialsOrJoinPool(t *testing.T) {
	svc := &adminServiceImpl{}
	for _, username := range []string{"ovh6_1", "v6-sub2-existing"} {
		_, err := svc.CreateProxy(context.Background(), &CreateProxyInput{Username: username, Password: "secret"})
		require.Error(t, err)
	}
	_, err := svc.createAccountProxyMode(context.Background(), &CreateAccountInput{ProxyMode: "ovh_ipv6", ProxyIDs: []int64{1, 2}})
	require.Error(t, err)
	_, err = svc.createAccountProxyMode(context.Background(), &CreateAccountInput{ProxyMode: "ovh_ipv6", Platform: PlatformAnthropic, Type: AccountTypeOAuth})
	require.Error(t, err)
}
