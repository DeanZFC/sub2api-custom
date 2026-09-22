package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketShortLifetimeRejectsLegacyOneHourState(t *testing.T) {
	now := time.Now()
	ticket := &openAICodexTicket{State: fakeCodexTicketState(292), Length: 292, CapturedAt: now.Add(-239 * time.Second), ExpiresAt: now.Add(time.Hour)}
	require.True(t, ticket.valid(now, 292))
	require.False(t, ticket.valid(now.Add(time.Second), 292))
	require.True(t, ticket.needsRefresh(now, time.Minute))
	ticket.CapturedAt = time.Time{}
	require.False(t, ticket.valid(now, 292), "legacy material without an age must be reacquired")
}

func TestCodexTicketCookieRequirementSharedAcrossModelsAndFailClosed(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	account := ticketTestAccount(901)
	for _, model := range svc.openAICodexTicketConfig().Models {
		svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{Model: model, State: fakeCodexTicketState(292), Length: 292, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
		h := http.Header{"Cookie": []string{"__cflb=client; __oailb=client"}}
		require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, model, h), ErrOpenAICodexTicketUnavailable)
		require.Empty(t, h.Get(openAICodexTurnStateHeader))
		require.True(t, svc.openAICodexTicketBlocksAccount(account, model))
	}
	seedCodexTicketCookies(svc, account)
	cookieDeadline := svc.lookupOpenAICodexTicketCookies(account).expiresAt()
	for _, model := range svc.openAICodexTicketConfig().Models {
		h := http.Header{"Cookie": []string{"__cflb=client; __oailb=client; unrelated=client"}}
		require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, model, h))
		require.Equal(t, "__cflb=test-cflb; __oailb=test-oailb", h.Get("Cookie"))
		require.False(t, svc.openAICodexTicketBlocksAccount(account, model))
	}
	require.Equal(t, cookieDeadline, svc.lookupOpenAICodexTicketCookies(account).expiresAt(), "injection cannot renew cookies")
	// Reusing the ticket material under a different account still has no routing cookies.
	other := ticketTestAccount(902)
	svc.storeOpenAICodexTicket(context.Background(), other, &openAICodexTicket{Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
	require.True(t, svc.openAICodexTicketBlocksAccount(other, "gpt-6-astra"))
	svc.captureOpenAICodexTicketCookies(context.Background(), account, http.Header{"Set-Cookie": []string{"__oailb=; Max-Age=0; Path=/"}}, time.Now())
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	statuses := OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now())
	svc.EnrichOpenAICodexTicketDiagnostics(account, statuses)
	require.False(t, statuses[0].Ready)
	require.False(t, statuses[0].CookieReady)
	require.True(t, statuses[0].Blocked)
}

func TestCodexTicketMissingCookieProbeRetriesAndReportsSafeReason(t *testing.T) {
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: h, Body: http.NoBody}, codexTicketResponse()}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, upstream)
	account := ticketTestAccount(903)
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	events := svc.OpenAICodexTicketHistory(context.Background(), account, "")
	require.Equal(t, "cookie_missing", events[0].ErrorCode)
	require.Equal(t, "failure", events[0].Outcome)
	dueTicketJobs(svc)
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	ticket := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.Equal(t, 240*time.Second, ticket.ExpiresAt.Sub(ticket.CapturedAt))
	next := svc.openaiCodexTicketScheduler.jobs[openAICodexTicketKey(account.ID, "gpt-6-astra")].NextRetryAt
	require.WithinDuration(t, ticket.CapturedAt.Add(180*time.Second), *next, time.Second)
}

func TestCodexTicketCookieExpirySchedulesEarlyRefreshAndFailureKeepsTicket(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 503, Header: http.Header{}, Body: http.NoBody}}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, upstream)
	account := ticketTestAccount(904)
	now := time.Now()
	h := http.Header{"Set-Cookie": []string{"__cflb=c; Max-Age=90; Path=/", "__oailb=o; Max-Age=90; Path=/"}}
	svc.captureOpenAICodexTicketCookies(context.Background(), account, h, now)
	ticket := &openAICodexTicket{Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, CapturedAt: now, ExpiresAt: now.Add(240 * time.Second)}
	svc.storeOpenAICodexTicket(context.Background(), account, ticket)
	cfg := svc.openAICodexTicketConfig()
	require.False(t, svc.startOpenAICodexTicketProbe(context.Background(), account, "gpt-6-astra", cfg, now, false))
	job := svc.openaiCodexTicketScheduler.jobs[openAICodexTicketKey(account.ID, "gpt-6-astra")]
	require.WithinDuration(t, now.Add(30*time.Second), *job.NextRetryAt, time.Millisecond)
	statuses := OpenAICodexTicketStatuses(account, cfg, now)
	svc.EnrichOpenAICodexTicketDiagnostics(account, statuses)
	require.LessOrEqual(t, statuses[0].RemainingSeconds, int64(90))
	require.GreaterOrEqual(t, statuses[0].RemainingSeconds, int64(89))
	require.True(t, svc.startOpenAICodexTicketProbe(context.Background(), account, "gpt-6-astra", cfg, now.Add(31*time.Second), false))
	require.Equal(t, "upstream_5xx", job.LastErrorCode)
	require.Equal(t, ticket.State, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").State)
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"), "refresh failure must keep a still-live pair usable")
}

func TestCodexTicketTeamCookieRequirementIsNotInferred(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	account := ticketTestAccount(905)
	account.Credentials["plan_type"] = "team"
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{Model: "gpt-6-astra", State: fakeCodexTicketState(332), Length: 332, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(240 * time.Second)})
	headers := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Empty(t, headers.Get("Cookie"))
	require.Len(t, headers.Get(openAICodexTurnStateHeader), 332)
}

func TestCodexTicketShortCookieHonorsCompletionCooldown(t *testing.T) {
	h := http.Header{"Set-Cookie": []string{"__cflb=a; Max-Age=20", "__oailb=b; Max-Age=20"}}
	h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: h, Body: http.NoBody}}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, upstream)
	account := ticketTestAccount(906)
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	job := svc.openaiCodexTicketScheduler.jobs[openAICodexTicketKey(account.ID, "gpt-6-astra")]
	require.WithinDuration(t, time.Now().Add(6*time.Second), job.minNextAttempt, time.Second)
	// Model a request that began long before its response arrived.
	started := time.Now().Add(-10 * time.Second)
	job.LastAttemptAt = &started
	require.False(t, svc.startOpenAICodexTicketProbe(context.Background(), account, "gpt-6-astra", svc.openAICodexTicketConfig(), time.Now(), false))
	require.Len(t, upstream.requests, 1)
}

func TestCodexTicketShorterConfiguredLifetimeAppliesAfterRestart(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, TTLSeconds: 60}, nil)
	account := ticketTestAccount(907)
	account.Credentials["plan_type"] = "team"
	now := time.Now()
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): &openAICodexTicket{Model: "gpt-6-astra", State: fakeCodexTicketState(332), Length: 332, CapturedAt: now.Add(-120 * time.Second), ExpiresAt: now.Add(120 * time.Second)}}
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	status := OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), now)
	require.False(t, status[0].Ready)
	require.True(t, status[0].Blocked)
}
