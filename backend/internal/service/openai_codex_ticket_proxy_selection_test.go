//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type stableTicketProxyCall struct {
	accountID int64
	proxy     string
	model     string
}

type stableTicketProxyUpstream struct {
	HTTPUpstream
	mu      sync.Mutex
	calls   []stableTicketProxyCall
	outcome string
}

func (u *stableTicketProxyUpstream) Do(req *http.Request, proxy string, accountID int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.calls = append(u.calls, stableTicketProxyCall{accountID, proxy, extractOpenAICodexTicketModel(body)})
	outcome := u.outcome
	u.mu.Unlock()
	switch outcome {
	case "network":
		return nil, errors.New("connection refused")
	case "timeout":
		return nil, context.DeadlineExceeded
	case "proxy_auth":
		return &http.Response{StatusCode: http.StatusProxyAuthRequired, Header: http.Header{}, Body: http.NoBody}, nil
	case "upstream_5xx":
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}, nil
	case "rate_limited":
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: http.NoBody}, nil
	default:
		h := http.Header{}
		h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(312))
		return &http.Response{StatusCode: http.StatusOK, Header: h, Body: http.NoBody}, nil
	}
}

// Simulate elapsed retry/cooldown time without changing account proxy bindings.
func dueStableTicketProbes(s *OpenAIGatewayService) {
	dueTicketJobs(s)
	r := &s.openaiCodexTicketScheduler
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, health := range r.proxies {
		health.cooldown = time.Time{}
	}
}

func TestCodexTicketUsesBusinessProxyOrDirect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bound bool
	}{
		{"account_proxy", true},
		{"direct", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &stableTicketProxyUpstream{}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, u)
			account := activeTicketAccounts(42)[0]
			want, source := "", "direct"
			if tc.bound {
				id := int64(8)
				account.ProxyID = &id
				account.Proxy = &Proxy{ID: id, Protocol: "http", Host: "business.example", Port: 3128, Username: "u", Password: "p"}
				want, source = account.Proxy.URL(), "account"
			}
			for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-6-astra"} {
				dueStableTicketProbes(svc)
				svc.probeOnceOpenAICodexTicket(context.Background(), &account, model)
			}
			require.Len(t, u.calls, 3)
			for _, call := range u.calls {
				require.Equal(t, want, call.proxy)
			}
			statuses := OpenAICodexTicketStatuses(&account, svc.openAICodexTicketConfig(), time.Now())
			svc.EnrichOpenAICodexTicketDiagnostics(&account, statuses)
			require.Equal(t, source, statuses[0].LastProxySource)
			require.Zero(t, statuses[0].LastProxyIndex)
		})
	}
}

func TestCodexTicketStableAccountMissingOrInvalidProxyNeverFallsBack(t *testing.T) {
	for _, proxy := range []*Proxy{
		nil,
		{ID: 8, Protocol: "file", Host: "bad.example", Port: 80},
		{ID: 8, Protocol: "http", Port: 80},
		{ID: 8, Protocol: "http", Host: "bad.example", Port: -1},
		{ID: 9, Protocol: "http", Host: "different.example", Port: 80},
	} {
		u := &stableTicketProxyUpstream{}
		svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, u)
		account := activeTicketAccounts(42)[0]
		id := int64(8)
		account.ProxyID, account.Proxy = &id, proxy
		svc.dispatchOpenAICodexTickets(context.Background(), []Account{account})
		svc.openaiCodexTicketScheduler.workers.Wait()
		require.Empty(t, u.calls, "configured but invalid/missing proxy cannot become a direct route")
		statuses := OpenAICodexTicketStatuses(&account, svc.openAICodexTicketConfig(), time.Now())
		svc.EnrichOpenAICodexTicketDiagnostics(&account, statuses)
		require.Zero(t, statuses[0].Attempts)
		require.Equal(t, "no_proxy", statuses[0].LastErrorCode)
	}
}

func TestCodexTicketAccountExitNeverChangesAfterRepeatedFailures(t *testing.T) {
	for _, outcome := range []string{"network", "timeout", "proxy_auth", "upstream_5xx", "rate_limited", "length_mismatch"} {
		t.Run(outcome, func(t *testing.T) {
			u := &stableTicketProxyUpstream{outcome: outcome}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, u)
			account := ticketTestAccount(42)
			id := int64(8)
			account.ProxyID = &id
			account.Proxy = &Proxy{ID: id, Protocol: "http", Host: "business.example", Port: 3128}
			for i := 0; i < 8; i++ {
				dueStableTicketProbes(svc)
				model := []string{"gpt-6-astra", "gpt-5.6-sol"}[i%2]
				svc.probeOnceOpenAICodexTicket(context.Background(), account, model)
			}
			require.Len(t, u.calls, 8)
			for _, call := range u.calls {
				require.Equal(t, account.Proxy.URL(), call.proxy)
			}
		})
	}
}

func TestCodexTicketAccountExitCapacityAndCooldownDelayOtherModels(t *testing.T) {
	upstream := &ticketScheduledUpstream{requests: make(chan ticketScheduledRequest, 4)}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestAccountConcurrency: 2, HarvestProxyConcurrency: 1}, upstream)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.openaiCodexTicketScheduler.workers.Wait() }()
	account := activeTicketAccounts(42)[0]
	svc.dispatchOpenAICodexTickets(ctx, []Account{account})
	first := nextTicketRequest(t, upstream)
	select {
	case <-upstream.requests:
		t.Fatal("busy pinned proxy must not send another model via a different exit")
	default:
	}
	close(first.done)
	svc.openaiCodexTicketScheduler.workers.Wait()
	r := &svc.openaiCodexTicketScheduler
	r.mu.Lock()
	r.proxies[first.proxy].cooldown = time.Now().Add(time.Minute)
	r.mu.Unlock()
	dueTicketJobs(svc)
	svc.dispatchOpenAICodexTickets(ctx, []Account{account})
	require.Empty(t, upstream.requests, "short cooldown must not rotate to the idle proxy")
	dueStableTicketProbes(svc)
	svc.dispatchOpenAICodexTickets(ctx, []Account{account})
	next := nextTicketRequest(t, upstream)
	require.Equal(t, first.proxy, next.proxy)
	require.NotEqual(t, first.model, next.model, "unattempted model gets the next fair admission")
	close(next.done)
}

func TestCodexTicketStableAccountProxyEditCancelsOldAttemptAndDoesNotInheritFailures(t *testing.T) {
	u := &ticketScheduledUpstream{requests: make(chan ticketScheduledRequest, 4)}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, u)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.openaiCodexTicketScheduler.workers.Wait() }()
	account := activeTicketAccounts(42)[0]
	id := int64(8)
	account.ProxyID = &id
	account.Proxy = &Proxy{ID: id, Protocol: "http", Host: "old.example", Port: 80}
	svc.dispatchOpenAICodexTickets(ctx, []Account{account})
	old := nextTicketRequest(t, u)
	require.Equal(t, "http://old.example:80", old.proxy)
	account.Proxy = &Proxy{ID: id, Protocol: "http", Host: "new.example", Port: 80}
	svc.dispatchOpenAICodexTickets(ctx, []Account{account})
	var updated ticketScheduledRequest
	require.Eventually(t, func() bool {
		dueTicketJobs(svc)
		svc.dispatchOpenAICodexTickets(ctx, []Account{account})
		select {
		case updated = <-u.requests:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.Equal(t, account.Proxy.URL(), updated.proxy)
	close(updated.done)
	svc.openaiCodexTicketScheduler.workers.Wait()
	require.Zero(t, svc.openaiCodexTicketScheduler.proxies[account.Proxy.URL()].failures)
	account.Extra = map[string]any{OpenAICodexTicketEnabledExtraKey: false}
	svc.dispatchOpenAICodexTickets(ctx, []Account{account})
	require.Empty(t, svc.openaiCodexTicketScheduler.bindings)
}

func TestCodexTicketStableConfigurationChangeDrainsOldModelBeforeNewExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding *codexTicketProxyBinding
		route   codexTicketProxyRoute
	}{
		{"account_proxy_edit", &codexTicketProxyBinding{source: "account", proxy: "http://old.example:80"}, codexTicketProxyRoute{source: "account", proxy: "http://new.example:80", valid: true}},
		{"binding_cleared_while_canceling", nil, codexTicketProxyRoute{source: "account", proxy: "http://new.example:80", valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &codexTicketScheduler{}
			r.init()
			const accountID = int64(42)
			// Model A was canceled but is still leaving its old proxy. With
			// account concurrency 2, model B may reach proxy admission meanwhile.
			r.active, r.accountActive[accountID] = 1, 1
			r.bindings[accountID] = tc.binding
			r.proxies["http://old.example:80"] = &codexTicketProxyHealth{active: 1}
			job := &codexTicketJob{accountID: accountID, model: "gpt-5.6-sol"}
			now := time.Now()
			_, next, admitted := r.acquireProxy(job, tc.route, 2, now)
			require.False(t, admitted, "model B must wait for model A to finish cancellation")
			require.True(t, next.After(now))
			require.Same(t, tc.binding, r.bindings[accountID])
			require.NotContains(t, r.proxies, "http://new.example:80")
			r.active, r.accountActive[accountID] = 0, 0
			r.proxies["http://old.example:80"].active = 0
			selection, _, admitted := r.acquireProxy(job, tc.route, 2, next)
			require.True(t, admitted)
			require.Equal(t, "http://new.example:80", selection.proxy)
			require.Equal(t, "account", selection.source)
		})
	}
}
