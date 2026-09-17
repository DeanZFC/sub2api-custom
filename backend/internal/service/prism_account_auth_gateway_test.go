//go:build unit

package service

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func enablePrismAccountAuthFixture(svc *OpenAIGatewayService, account *Account, upstream *prismGatewayUpstream) {
	account.Extra[PrismExtraKey].(map[string]any)["auth_mode"] = PrismAuthModeAccount
	account.Credentials["access_token"] = "synthetic-private-stale-access"
	account.Credentials["chatgpt_user_id"] = "synthetic-user"
	// Keep the old manual Cookie saved, to prove it is ignored in account mode.
	cache := newOpenAITokenCacheStub()
	upstream.accountAuthToken = "synthetic-private-cached-access"
	cache.tokens[OpenAITokenCacheKey(account)] = upstream.accountAuthToken
	svc.openAITokenProvider = NewOpenAITokenProvider(nil, cache, nil)
}

func TestPrismAccountAuthGatewayAndConnectionUseTokenProvider(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat", "connection"} {
		t.Run(endpoint, func(t *testing.T) {
			svc, account, upstream := newPrismGatewayFixture(t)
			enablePrismAccountAuthFixture(svc, account, upstream)
			body := `{"model":"client-model","reasoning":{"effort":"high"},"input":"Synthetic question"}`
			if endpoint == "chat" {
				body = `{"model":"client-model","reasoning_effort":"high","messages":[{"role":"user","content":"Synthetic question"}]}`
			}
			ctx, c, rec := prismGatewayContext(body, "/v1/responses")
			var err error
			switch endpoint {
			case "chat":
				_, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
			case "connection":
				tests := &AccountTestService{httpUpstream: upstream, cfg: svc.cfg, accountRepo: prismGatewayAccountRepo{account: account}, openaiGatewayService: svc}
				c.Request = c.Request.WithContext(withAccountTestReasoningEffort(ctx, "high"))
				err = tests.TestAccountConnection(c, account.ID, "client-model", "Synthetic question", "text")
			default:
				_, err = svc.Forward(ctx, c, account, []byte(body))
			}
			require.NoError(t, err)
			require.Equal(t, 1, upstream.starts)
			require.Contains(t, rec.Body.String(), prismGatewayAnswer)
			prismGatewayAssertPrivateAbsent(t, rec.Body.String())
			require.Equal(t, prismGatewayCookie, account.Credentials[PrismCookieCredentialKey], "saved manual credential must not be overwritten")
			require.Equal(t, "synthetic-private-stale-access", account.Credentials["access_token"], "session cookies must not overwrite account OAuth credentials")
		})
	}
}

func TestPrismAccountAuthRejectionDoesNotSubmitOrFailover(t *testing.T) {
	svc, account, upstream := newPrismGatewayFixture(t)
	enablePrismAccountAuthFixture(svc, account, upstream)
	upstream.rejectAccountAuth = true
	body := `{"model":"client-model","input":"Synthetic question"}`
	ctx, c, rec := prismGatewayContext(body, "/v1/responses")
	_, err := svc.Forward(ctx, c, account, []byte(body))
	require.Error(t, err)
	require.Equal(t, 1, upstream.calls)
	require.Zero(t, upstream.starts)
	require.Equal(t, 502, rec.Code)
	require.Contains(t, rec.Body.String(), "prism_authentication_failed")
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	prismGatewayAssertPrivateAbsent(t, rec.Body.String())
}

func TestPrismAccountAuthSetupTokenExpiryStopsBeforeNetwork(t *testing.T) {
	svc, account, upstream := newPrismGatewayFixture(t)
	enablePrismAccountAuthFixture(svc, account, upstream)
	account.Type = AccountTypeSetupToken
	account.Credentials["expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339)
	body := `{"model":"client-model","input":"Synthetic question"}`
	ctx, c, rec := prismGatewayContext(body, "/v1/responses")
	_, err := svc.Forward(ctx, c, account, []byte(body))
	require.Error(t, err)
	require.Zero(t, upstream.calls)
	require.Contains(t, rec.Body.String(), "prism_account_token_unavailable")
	prismGatewayAssertPrivateAbsent(t, rec.Body.String())
}
