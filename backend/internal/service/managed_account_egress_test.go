package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type retiredRouteAccountRepo struct {
	AccountRepository
	blocked   bool
	err       error
	checkedID int64
}

func (r *retiredRouteAccountRepo) HasRetiredVMRoute(_ context.Context, id int64) (bool, error) {
	r.checkedID = id
	return r.blocked, r.err
}

type retiredRouteProxyRepo struct {
	ProxyRepository
	proxy   *Proxy
	blocked bool
	err     error
}

func (r *retiredRouteProxyRepo) IsRetiredVMProxy(context.Context, int64) (bool, error) {
	return r.blocked, r.err
}
func (r *retiredRouteProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return r.proxy, r.err
}

func TestRetiredVMRouteRejectsStaleAccountBeforeAnyTransport(t *testing.T) {
	ctx := context.Background()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, nil)
	require.NoError(t, err)
	account := &Account{ID: 123, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, tt := range []struct {
		name string
		repo *retiredRouteAccountRepo
	}{
		{"authoritative binding", &retiredRouteAccountRepo{blocked: true}},
		{"lookup fails closed", &retiredRouteAccountRepo{err: errors.New("database unavailable")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately omit all outgoing clients: each boundary must stop
			// before it can dispatch through HTTP, plugins, BPS or model probes.
			gateway := &OpenAIGatewayService{accountRepo: tt.repo}
			response, err := gateway.doOpenAIUpstream(request, "", account)
			require.Error(t, err)
			require.Nil(t, response)
			testService := &AccountTestService{accountRepo: tt.repo}
			response, err = testService.doOpenAIAccountTestUpstream(request, "", account, false)
			require.Error(t, err)
			require.Nil(t, response)
			response, err = testService.doUpstreamModelsRequest(request, "", account)
			require.Error(t, err)
			require.Nil(t, response)
			_, err = gateway.fetchOpenAIImageURLBase64(ctx, account, "https://example.com/image.png")
			require.Error(t, err)
			_, err = (&AccountUsageService{accountRepo: tt.repo}).probeOpenAICodexSnapshot(ctx, account)
			require.Error(t, err)
			_, err = (&OpenAITokenRefresher{accountRepo: tt.repo}).Refresh(ctx, account)
			require.Error(t, err)
			require.Equal(t, account.ID, tt.repo.checkedID)
		})
	}
}

func TestRetiredVMRouteMarkersDisableSchedulingAndShadowCredentials(t *testing.T) {
	for _, marker := range []string{"openai_vm_bound", "openai_vm_retired"} {
		for _, value := range []any{true, "true"} {
			account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{marker: value}}
			require.True(t, account.IsRetiredVMRoute())
			require.False(t, account.IsSchedulable())
			require.False(t, account.IsCredentialUsableForShadow())
			require.False(t, (&OpenAITokenRefresher{}).CanRefresh(account))
			require.ErrorIs(t, rejectRetiredVMAccount(context.Background(), nil, account), errRetiredVMRoute)
			_, err := resolveOpenAIAccountProxyURL(context.Background(), nil, account)
			require.ErrorIs(t, err, errRetiredVMRoute)
		}
	}
}

func TestManagedEgressRetirementPreservesOrdinaryRoutes(t *testing.T) {
	ctx := context.Background()
	account := &Account{ID: 12, Extra: map[string]any{"openai_vm_bound": false}}
	require.NoError(t, rejectRetiredVMAccount(ctx, &retiredRouteAccountRepo{}, account))
	proxyURL, err := resolveOpenAIAccountProxyURL(ctx, nil, account)
	require.NoError(t, err)
	require.Empty(t, proxyURL, "explicit direct route remains direct")
	id := int64(4)
	account.ProxyID = &id
	proxy := &Proxy{ID: id, Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Status: StatusActive}
	proxyURL, err = resolveOpenAIAccountProxyURL(ctx, &retiredRouteProxyRepo{proxy: proxy}, account)
	require.NoError(t, err)
	require.Equal(t, proxy.URL(), proxyURL)
	_, err = resolveOpenAIAccountProxyURL(ctx, &retiredRouteProxyRepo{proxy: proxy, blocked: true}, account)
	require.ErrorIs(t, err, errRetiredVMRoute, "managed proxy binding wins over a stale account snapshot")
}
